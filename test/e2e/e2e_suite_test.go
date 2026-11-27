// Copyright 2026 Dimitri Koshkin. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Environment set by `make e2e-test`.
var (
	kubeconfig        = os.Getenv("KUBECONFIG")
	capkManager       = os.Getenv("CAPK_MANAGER_BINARY")
	kubernetesVersion = os.Getenv("E2E_KUBERNETES_VERSION")
	capkVersion       = os.Getenv("CAPK_VERSION")
	clusterctlConfig  = os.Getenv("CLUSTERCTL_CONFIG")
)

var (
	kmirrorBinary string
	k8sClient     client.Client
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "kmirror e2e suite")
}

var _ = SynchronizedBeforeSuite(func() []byte {
	for name, value := range map[string]string{
		"KUBECONFIG":             kubeconfig,
		"CAPK_MANAGER_BINARY":    capkManager,
		"E2E_KUBERNETES_VERSION": kubernetesVersion,
		"CAPK_VERSION":           capkVersion,
		"CLUSTERCTL_CONFIG":      clusterctlConfig,
	} {
		Expect(value).NotTo(BeEmpty(), "%s must be set, run the tests with `make e2e-test`", name)
	}
	binary, err := gexec.Build("github.com/dkoshkin/kmirror/cmd/kmirror")
	Expect(err).NotTo(HaveOccurred())
	return []byte(binary)
}, func(binary []byte) {
	kmirrorBinary = string(binary)

	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	Expect(err).NotTo(HaveOccurred())
	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	k8sClient, err = client.New(restConfig, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
})

var _ = SynchronizedAfterSuite(func() {}, func() {
	gexec.CleanupBuildArtifacts()
})
