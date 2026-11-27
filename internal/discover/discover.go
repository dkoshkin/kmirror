// Copyright 2026 Dimitri Koshkin. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package discover works out how to run a controller locally from its Deployment, Pods and Leases.
package discover

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// DefaultContainerName is the container picked when a Pod has more than one.
const DefaultContainerName = "manager"

// droppedFlags are inherited flags that make no sense when running locally.
// Local defaults apply instead, and extra args can add any of them back.
var droppedFlags = []string{
	// The local manager must use the in-cluster ServiceAccount config.
	"kubeconfig",
	// kmirror holds the Lease, so local leader election is off.
	"leader-elect",
	"leader-elect-lease-duration",
	"leader-elect-renew-deadline",
	"leader-elect-retry-period",
	"leader-election-id",
	"leader-election-namespace",
	// Metrics, health and profiler endpoints bind on the local machine.
	"metrics-bind-address",
	"metrics-bind-addr",
	"metrics-secure",
	"metrics-cert-path",
	"metrics-cert-name",
	"metrics-cert-key",
	"health-probe-bind-address",
	"health-addr",
	"diagnostics-address",
	"insecure-diagnostics",
	"profiler-address",
}

// nonStealablePortName matches container port names that are never stolen.
var nonStealablePortName = regexp.MustCompile(`(?i)metric|health|probe|pprof|debug`)

// envReference matches a $(VAR) reference in a container arg.
var envReference = regexp.MustCompile(`\$\(([A-Za-z_][A-Za-z0-9_]*)\)`)

// SelectContainer returns the "manager" container, or the first container.
func SelectContainer(spec *corev1.PodSpec) (*corev1.Container, error) {
	if len(spec.Containers) == 0 {
		return nil, errors.New("pod spec has no containers")
	}
	for i := range spec.Containers {
		if spec.Containers[i].Name == DefaultContainerName {
			return &spec.Containers[i], nil
		}
	}
	return &spec.Containers[0], nil
}

// ProbePorts returns the ports used by the container's probes, with named ports resolved.
func ProbePorts(c *corev1.Container) []int32 {
	var ports []int32
	for _, probe := range []*corev1.Probe{c.LivenessProbe, c.ReadinessProbe, c.StartupProbe} {
		if probe == nil {
			continue
		}
		var port *intstr.IntOrString
		switch {
		case probe.HTTPGet != nil:
			port = &probe.HTTPGet.Port
		case probe.TCPSocket != nil:
			port = &probe.TCPSocket.Port
		case probe.GRPC != nil:
			ports = append(ports, probe.GRPC.Port)
			continue
		default:
			continue
		}
		if resolved, ok := resolvePort(c, port); ok {
			ports = append(ports, resolved)
		}
	}
	slices.Sort(ports)
	return slices.Compact(ports)
}

func resolvePort(c *corev1.Container, port *intstr.IntOrString) (int32, bool) {
	if port.Type == intstr.Int {
		return port.IntVal, true
	}
	for _, p := range c.Ports {
		if p.Name == port.StrVal {
			return p.ContainerPort, true
		}
	}
	return 0, false
}

// StealPorts returns the container's TCP ports minus probe ports and metrics or debug ports.
// These are the ports that receive inbound traffic the local process should answer, e.g. webhooks.
func StealPorts(c *corev1.Container) []int32 {
	probes := ProbePorts(c)
	var ports []int32
	for _, p := range c.Ports {
		if p.Protocol != "" && p.Protocol != corev1.ProtocolTCP {
			continue
		}
		if nonStealablePortName.MatchString(p.Name) || slices.Contains(probes, p.ContainerPort) {
			continue
		}
		ports = append(ports, p.ContainerPort)
	}
	slices.Sort(ports)
	return slices.Compact(ports)
}

// RemoteReadPaths returns regexes for files the local process must read from the remote Pod:
// Secret and projected volume mounts (e.g. webhook serving certs) and the ServiceAccount token.
func RemoteReadPaths(spec *corev1.PodSpec, c *corev1.Container) []string {
	secretVolumes := map[string]bool{}
	for i := range spec.Volumes {
		if v := &spec.Volumes[i]; v.Secret != nil || v.Projected != nil {
			secretVolumes[v.Name] = true
		}
	}
	paths := []string{"^/var/run/secrets/.*"}
	for _, m := range c.VolumeMounts {
		if secretVolumes[m.Name] {
			paths = append(paths, "^"+regexp.QuoteMeta(strings.TrimSuffix(m.MountPath, "/"))+"(/.*)?$")
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// Leader is the Lease held by one of a Deployment's Pods.
type Leader struct {
	Lease *coordinationv1.Lease
	Pod   *corev1.Pod
}

// FindLeader returns the Lease whose holder identity ("<pod>_<uuid>" for controller-runtime)
// names one of the given Pods.
func FindLeader(leases []coordinationv1.Lease, pods []corev1.Pod) (*Leader, error) {
	for i := range leases {
		holder := leases[i].Spec.HolderIdentity
		if holder == nil || *holder == "" {
			continue
		}
		podName, _, _ := strings.Cut(*holder, "_")
		for j := range pods {
			if pods[j].Name == podName && pods[j].DeletionTimestamp == nil {
				return &Leader{Lease: &leases[i], Pod: &pods[j]}, nil
			}
		}
	}
	return nil, errors.New("no Lease is held by a Pod of the Deployment (is leader election enabled?)")
}

// Args is the command line for the local binary.
type Args struct {
	// Args are the arguments to pass to the local binary.
	Args []string
	// Dropped are the inherited arguments that were removed.
	Dropped []string
	// Unresolved are arguments that still contain a $(VAR) reference.
	Unresolved []string
}

// InheritArgs builds the local command line from the container's command[1:] and args.
// It drops flags that make no sense locally (and their value when passed as a separate argument),
// expands $(VAR) references from the container's env, adds --leader-elect=false and appends extra.
func InheritArgs(c *corev1.Container, pod *corev1.Pod, extra []string) Args {
	var inherited []string
	if len(c.Command) > 1 {
		inherited = append(inherited, c.Command[1:]...)
	}
	inherited = append(inherited, c.Args...)

	env := envValues(c, pod)
	var result Args
	for i := 0; i < len(inherited); i++ {
		arg := inherited[i]
		if name, hasValue, ok := flagName(arg); ok && slices.Contains(droppedFlags, name) {
			result.Dropped = append(result.Dropped, arg)
			if !hasValue && i+1 < len(inherited) && !strings.HasPrefix(inherited[i+1], "-") {
				i++
				result.Dropped = append(result.Dropped, inherited[i])
			}
			continue
		}
		expanded := envReference.ReplaceAllStringFunc(arg, func(ref string) string {
			if v, ok := env[envReference.FindStringSubmatch(ref)[1]]; ok {
				return v
			}
			return ref
		})
		if envReference.MatchString(expanded) {
			result.Unresolved = append(result.Unresolved, expanded)
		}
		result.Args = append(result.Args, expanded)
	}
	result.Args = append(result.Args, "--leader-elect=false")
	result.Args = append(result.Args, extra...)
	return result
}

// flagName returns the name of a "-name", "--name" or "--name=value" argument.
func flagName(arg string) (name string, hasValue, ok bool) {
	if !strings.HasPrefix(arg, "-") || arg == "-" || arg == "--" {
		return "", false, false
	}
	name, _, hasValue = strings.Cut(strings.TrimLeft(arg, "-"), "=")
	return name, hasValue, true
}

// envValues returns the container's env vars with literal values or fieldRefs resolvable from the Pod.
func envValues(c *corev1.Container, pod *corev1.Pod) map[string]string {
	values := map[string]string{}
	for _, e := range c.Env {
		switch {
		case e.ValueFrom == nil:
			values[e.Name] = e.Value
		case e.ValueFrom.FieldRef != nil && pod != nil:
			if v, err := fieldValue(pod, e.ValueFrom.FieldRef.FieldPath); err == nil {
				values[e.Name] = v
			}
		}
	}
	return values
}

func fieldValue(pod *corev1.Pod, path string) (string, error) {
	switch path {
	case "metadata.name":
		return pod.Name, nil
	case "metadata.namespace":
		return pod.Namespace, nil
	case "metadata.uid":
		return string(pod.UID), nil
	case "spec.nodeName":
		return pod.Spec.NodeName, nil
	case "spec.serviceAccountName":
		return pod.Spec.ServiceAccountName, nil
	case "status.podIP":
		return pod.Status.PodIP, nil
	case "status.hostIP":
		return pod.Status.HostIP, nil
	default:
		return "", fmt.Errorf("unsupported fieldRef %q", path)
	}
}
