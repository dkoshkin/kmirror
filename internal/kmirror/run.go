// Copyright 2026 Dimitri Koshkin. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package kmirror swaps an in-cluster controller for a local binary running under mirrord.
package kmirror

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"slices"
	"strings"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/dkoshkin/kmirror/internal/discover"
	"github.com/dkoshkin/kmirror/internal/lease"
	"github.com/dkoshkin/kmirror/internal/linkage"
	"github.com/dkoshkin/kmirror/internal/mirrord"
)

const (
	// leaseDuration is how long the Lease stays valid without a renew,
	// so the in-cluster controller takes over again soon after kmirror is killed.
	leaseDuration = 60 * time.Second
	// stepDownTimeout is how long to wait for the in-cluster controller to lose leadership and restart.
	stepDownTimeout = 3 * time.Minute
	// readyTimeout is how long to wait for the restarted controller to be Ready as a standby.
	readyTimeout = 3 * time.Minute
	pollInterval = 2 * time.Second
)

// Options configure a kmirror run.
type Options struct {
	Namespace  string
	Deployment string
	// Binary is the locally built controller binary.
	Binary string
	// ExtraArgs are appended after the inherited args.
	ExtraArgs []string
	// Log receives progress messages.
	Log io.Writer
}

// Run takes over the Deployment's leader Lease and runs the binary under mirrord against the leader Pod
// until the binary exits or ctx is canceled, then releases the Lease. It returns the binary's exit code.
func Run(ctx context.Context, opts *Options) (int, error) {
	logf := func(format string, args ...any) { _, _ = fmt.Fprintf(opts.Log, "kmirror: "+format+"\n", args...) }

	if err := checkBinary(opts.Binary); err != nil {
		return 1, err
	}
	if _, err := exec.LookPath("mirrord"); err != nil {
		return 1, errors.New("mirrord not found in PATH, see https://github.com/metalbear-co/mirrord")
	}

	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{},
	)
	restConfig, err := loader.ClientConfig()
	if err != nil {
		return 1, fmt.Errorf("loading kubeconfig: %w", err)
	}
	rawConfig, err := loader.RawConfig()
	if err != nil {
		return 1, fmt.Errorf("loading kubeconfig: %w", err)
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return 1, err
	}

	// Discover the container, ports, files, leader Lease and command line.
	deploy, err := client.AppsV1().Deployments(opts.Namespace).Get(ctx, opts.Deployment, metav1.GetOptions{})
	if err != nil {
		return 1, fmt.Errorf("getting Deployment %s/%s: %w", opts.Namespace, opts.Deployment, err)
	}
	container, err := discover.SelectContainer(&deploy.Spec.Template.Spec)
	if err != nil {
		return 1, err
	}
	selector, err := metav1.LabelSelectorAsSelector(deploy.Spec.Selector)
	if err != nil {
		return 1, err
	}
	pods, err := client.CoreV1().Pods(opts.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return 1, err
	}
	leases, err := client.CoordinationV1().Leases(opts.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return 1, err
	}
	leader, err := discover.FindLeader(leases.Items, pods.Items)
	if err != nil {
		return 1, fmt.Errorf("%s/%s: %w", opts.Namespace, opts.Deployment, err)
	}
	stealPorts := discover.StealPorts(container)
	args := discover.InheritArgs(container, leader.Pod, opts.ExtraArgs)

	logf("target:      %s/%s (container %s)", opts.Namespace, leader.Pod.Name, container.Name)
	logf("lease:       %s/%s (held by %s)", leader.Lease.Namespace, leader.Lease.Name, leader.Pod.Name)
	logf("probe ports: %s (left to the Pod)", formatPorts(discover.ProbePorts(container)))
	logf("steal ports: %s", formatPorts(stealPorts))
	if len(args.Dropped) > 0 {
		logf("dropped:     %s", strings.Join(args.Dropped, " "))
	}
	for _, arg := range args.Unresolved {
		logf("warning: unresolved env reference in %q, override it after --", arg)
	}
	logf("command:     %s %s", opts.Binary, strings.Join(args.Args, " "))

	work, err := os.MkdirTemp("", "kmirror-")
	if err != nil {
		return 1, err
	}
	defer func() { _ = os.RemoveAll(work) }()
	kubeconfigPath, err := mirrord.WriteKubeconfig(&rawConfig, work)
	if err != nil {
		return 1, err
	}
	cfg := mirrord.NewConfig(opts.Namespace, leader.Pod.Name, container.Name, stealPorts,
		discover.RemoteReadPaths(&deploy.Spec.Template.Spec, container))
	cfg.Kubeconfig = kubeconfigPath
	cfgPath, err := cfg.Write(work)
	if err != nil {
		return 1, err
	}

	// Take over leadership. The in-cluster manager fails to renew, exits and restarts as a standby.
	holder := &lease.Holder{
		Client:    client.CoordinationV1(),
		Namespace: leader.Lease.Namespace,
		Name:      leader.Lease.Name,
		Identity:  identity(),
		Duration:  leaseDuration,
	}
	restarts, err := restartCount(ctx, client, leader.Pod.Namespace, leader.Pod.Name, container.Name)
	if err != nil {
		return 1, err
	}
	if err := holder.Acquire(ctx); err != nil {
		return 1, fmt.Errorf("taking over Lease: %w", err)
	}
	logf("took over Lease as %s", holder.Identity)
	defer release(ctx, holder, logf)

	renewCtx, stopRenew := context.WithCancel(context.WithoutCancel(ctx))
	defer stopRenew()
	go holder.Renew(renewCtx, func(err error) { logf("warning: %v", err) })

	logf("waiting for %s to lose leadership and restart...", leader.Pod.Name)
	if err := waitForRestart(ctx, client, leader.Pod, container.Name, restarts); err != nil {
		return 1, err
	}
	logf("starting %s under mirrord...", opts.Binary)
	return runMirrord(ctx, cfgPath, opts.Binary, args.Args)
}

func checkBinary(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s is not an executable file", path)
	}
	return linkage.Check(path)
}

// identity returns the Lease holder identity, e.g. kmirror-alice-laptop.
func identity() string {
	name := "dev"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	host, _ := os.Hostname()
	host, _, _ = strings.Cut(host, ".")
	return strings.ToLower("kmirror-" + name + "-" + host)
}

// release releases the Lease, even when ctx is already canceled by an interrupt.
func release(ctx context.Context, holder *lease.Holder, logf func(string, ...any)) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	released, err := holder.Release(ctx)
	switch {
	case err != nil:
		logf("warning: releasing Lease: %v (it expires in %s)", err, leaseDuration)
	case released:
		logf("released Lease; the in-cluster manager resumes leadership")
	}
}

func restartCount(ctx context.Context, client kubernetes.Interface, namespace, pod, container string) (int32, error) {
	p, err := client.CoreV1().Pods(namespace).Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		return 0, err
	}
	if s := containerStatus(p, container); s != nil {
		return s.RestartCount, nil
	}
	return 0, nil
}

// waitForRestart waits for the leader container to restart after losing leadership and to be Ready again,
// so mirrord attaches to the restarted container.
func waitForRestart(
	ctx context.Context, client kubernetes.Interface, pod *corev1.Pod, container string, before int32,
) error {
	get := func(ctx context.Context) (*corev1.ContainerStatus, error) {
		p, err := client.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return containerStatus(p, container), nil
	}
	err := wait.PollUntilContextTimeout(ctx, pollInterval, stepDownTimeout, true,
		func(ctx context.Context) (bool, error) {
			s, err := get(ctx)
			return err == nil && s != nil && s.RestartCount > before, nil
		})
	if err != nil {
		return fmt.Errorf("%s did not lose leadership and restart within %s: %w", pod.Name, stepDownTimeout, err)
	}
	err = wait.PollUntilContextTimeout(ctx, pollInterval, readyTimeout, true,
		func(ctx context.Context) (bool, error) {
			s, err := get(ctx)
			return err == nil && s != nil && s.Ready && s.State.Running != nil, nil
		})
	if err != nil {
		return fmt.Errorf("%s did not become Ready within %s: %w", pod.Name, readyTimeout, err)
	}
	return nil
}

func containerStatus(p *corev1.Pod, container string) *corev1.ContainerStatus {
	for i := range p.Status.ContainerStatuses {
		if p.Status.ContainerStatuses[i].Name == container {
			return &p.Status.ContainerStatuses[i]
		}
	}
	return nil
}

// runMirrord runs the binary under mirrord. When ctx is canceled it interrupts mirrord and waits for it to exit,
// so the local manager shuts down gracefully before the Lease is released.
func runMirrord(ctx context.Context, cfgPath, binary string, args []string) (int, error) {
	argv := append([]string{"exec", "-f", cfgPath, "--", binary}, args...)
	cmd := exec.Command("mirrord", argv...) //nolint:gosec // Runs the user's binary by design.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Without KUBECONFIG the local manager falls through to the in-cluster config:
	// the Pod's env (KUBERNETES_SERVICE_*) and ServiceAccount token, read remotely by mirrord.
	cmd.Env = slices.DeleteFunc(os.Environ(), func(e string) bool { return strings.HasPrefix(e, "KUBECONFIG=") })
	if err := cmd.Start(); err != nil {
		return 1, fmt.Errorf("starting mirrord: %w", err)
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Signal(syscall.SIGINT)
		case <-done:
		}
	}()
	err := cmd.Wait()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

func formatPorts(ports []int32) string {
	if len(ports) == 0 {
		return "none"
	}
	s := make([]string, 0, len(ports))
	for _, p := range ports {
		s = append(s, fmt.Sprint(p))
	}
	return strings.Join(s, ",")
}
