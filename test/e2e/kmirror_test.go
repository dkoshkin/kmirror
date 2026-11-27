// Copyright 2026 Dimitri Koshkin. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
	coordinationv1 "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	capkNamespace  = "capk-system"
	capkDeployment = "capk-controller-manager"
	// capkLease is the LeaderElectionID of the CAPK manager.
	capkLease = "kubemark-manager-leader-election-capi"

	// The CAPK capd flavor creates its MachineDeployment in the default namespace.
	clusterNamespace = "default"
	clusterName      = "kmirror-e2e"
)

var _ = Describe("kmirror", func() {
	It(
		"runs a local CAPK manager that creates a Kubemark-backed cluster, then hands control back",
		func(ctx SpecContext) {
			By("starting kmirror with a local CAPK manager")
			cmd := exec.Command(kmirrorBinary, "-n", capkNamespace, "-d", capkDeployment, capkManager, "--", "--v=2")
			session, err := gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func(ctx SpecContext) {
				if session.ExitCode() == -1 {
					session.Interrupt().Wait(2 * time.Minute)
					session.Kill()
				}
				By("deleting the workload cluster")
				_ = run(ctx, nil, "kubectl", "--kubeconfig", kubeconfig, "-n", clusterNamespace,
					"delete", "cluster", clusterName, "--ignore-not-found", "--timeout=10m")
			}, NodeTimeout(15*time.Minute))

			By("waiting for kmirror to take over the Lease and start the local manager")
			Eventually(session.Err, 5*time.Minute).Should(gbytes.Say(`starting .* under mirrord`))
			Expect(leaseHolder(ctx)).To(HavePrefix("kmirror-"))
			Eventually(session.Err, 3*time.Minute).Should(gbytes.Say(`Starting workers`))
			Expect(session.ExitCode()).To(Equal(-1), "kmirror exited early")

			By("creating a cluster with a CAPD control plane and Kubemark workers")
			manifest, err := output(ctx, "clusterctl", "generate", "cluster", clusterName,
				"--config", clusterctlConfig,
				"--kubeconfig", kubeconfig,
				"--target-namespace", clusterNamespace,
				"--infrastructure", "kubemark:"+capkVersion,
				"--flavor", "capd",
				"--kubernetes-version", kubernetesVersion,
				"--control-plane-machine-count", "1",
				"--worker-machine-count", "1",
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(manifest).NotTo(BeEmpty())
			Expect(run(ctx, manifest, "kubectl", "--kubeconfig", kubeconfig, "apply", "-f", "-")).To(Succeed())

			By("waiting for the Kubemark Machines to join the cluster as Nodes")
			Eventually(func(g Gomega) {
				machines := &unstructured.UnstructuredList{}
				machines.SetAPIVersion("cluster.x-k8s.io/v1beta2")
				machines.SetKind("MachineList")
				g.Expect(k8sClient.List(ctx, machines, client.InNamespace(clusterNamespace), client.MatchingLabels{
					"cluster.x-k8s.io/deployment-name": clusterName + "-kubemark-md-0",
				})).To(Succeed())
				g.Expect(machines.Items).NotTo(BeEmpty())
				for i := range machines.Items {
					nodeName, _, _ := unstructured.NestedString(machines.Items[i].Object, "status", "nodeRef", "name")
					g.Expect(nodeName).NotTo(BeEmpty(), "Machine %s has no Node yet", machines.Items[i].GetName())
				}
			}).WithContext(ctx).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

			By("checking the local manager did the work while holding the Lease")
			Expect(session.ExitCode()).To(Equal(-1), "kmirror exited early")
			Expect(leaseHolder(ctx)).To(HavePrefix("kmirror-"))

			By("stopping kmirror and handing control back to the in-cluster manager")
			session.Interrupt()
			Eventually(session, 2*time.Minute).Should(gexec.Exit())
			Expect(session.Err).To(gbytes.Say(`released Lease`))
			Eventually(
				leaseHolder,
			).WithContext(ctx).
				WithTimeout(2 * time.Minute).
				Should(HavePrefix(capkDeployment + "-"))
		},
		NodeTimeout(30*time.Minute),
	)
})

// leaseHolder returns the holder identity of the CAPK leader-election Lease.
func leaseHolder(ctx context.Context) (string, error) {
	l := &coordinationv1.Lease{}
	if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: capkNamespace, Name: capkLease}, l); err != nil {
		return "", err
	}
	if l.Spec.HolderIdentity == nil {
		return "", nil
	}
	return *l.Spec.HolderIdentity, nil
}

// run runs a command with stdin, sending its output to the GinkgoWriter.
func run(ctx context.Context, stdin []byte, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // Test helper for fixed commands.
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Stdout, cmd.Stderr = GinkgoWriter, GinkgoWriter
	return cmd.Run()
}

// output runs a command and returns its stdout.
func output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // Test helper for fixed commands.
	cmd.Stderr = GinkgoWriter
	return cmd.Output()
}
