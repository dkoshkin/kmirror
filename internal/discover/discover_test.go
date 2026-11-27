// Copyright 2026 Dimitri Koshkin. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package discover_test

import (
	"slices"
	"testing"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/dkoshkin/kmirror/internal/discover"
)

// kubebuilderManager is a container laid out like the default kubebuilder scaffold.
func kubebuilderManager() *corev1.Container {
	return &corev1.Container{
		Name:    "manager",
		Command: []string{"/manager"},
		Args: []string{
			"--leader-elect",
			"--metrics-bind-address=:8443",
			"--health-probe-bind-address", ":8081",
			"--namespace=$(POD_NAMESPACE)",
			"--feature-gates=Foo=true",
			"--name", "$(MISSING)",
		},
		Env: []corev1.EnvVar{{
			Name:      "POD_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"}},
		}},
		Ports: []corev1.ContainerPort{
			{Name: "webhook-server", ContainerPort: 9443},
			{Name: "metrics", ContainerPort: 8443},
			{Name: "healthz", ContainerPort: 8081},
			{Name: "grpc", ContainerPort: 9000},
			{Name: "udp", ContainerPort: 9001, Protocol: corev1.ProtocolUDP},
		},
		LivenessProbe: &corev1.Probe{
			HTTPGet: &corev1.HTTPGetAction{Port: intstr.FromString("healthz")},
		},
		ReadinessProbe: &corev1.Probe{
			TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(9000)},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "cert", MountPath: "/tmp/k8s-webhook-server/serving-certs/"},
			{Name: "cache", MountPath: "/cache"},
		},
	}
}

func TestSelectContainer(t *testing.T) {
	t.Parallel()

	spec := &corev1.PodSpec{Containers: []corev1.Container{{Name: "kube-rbac-proxy"}, {Name: "manager"}}}
	c, err := discover.SelectContainer(spec)
	if err != nil || c.Name != "manager" {
		t.Fatalf("SelectContainer() = %v, %v; want manager", c, err)
	}

	spec = &corev1.PodSpec{Containers: []corev1.Container{{Name: "controller"}, {Name: "sidecar"}}}
	c, err = discover.SelectContainer(spec)
	if err != nil || c.Name != "controller" {
		t.Fatalf("SelectContainer() = %v, %v; want controller", c, err)
	}

	if _, err := discover.SelectContainer(&corev1.PodSpec{}); err == nil {
		t.Fatal("SelectContainer() of an empty spec should fail")
	}
}

func TestPorts(t *testing.T) {
	t.Parallel()

	c := kubebuilderManager()
	if got, want := discover.ProbePorts(c), []int32{8081, 9000}; !slices.Equal(got, want) {
		t.Errorf("ProbePorts() = %v, want %v", got, want)
	}
	if got, want := discover.StealPorts(c), []int32{9443}; !slices.Equal(got, want) {
		t.Errorf("StealPorts() = %v, want %v", got, want)
	}
}

func TestRemoteReadPaths(t *testing.T) {
	t.Parallel()

	spec := &corev1.PodSpec{Volumes: []corev1.Volume{
		{Name: "cert", Secret: &corev1.SecretVolumeSource{SecretName: "cert"}},
		{Name: "cache", EmptyDir: &corev1.EmptyDirVolumeSource{}},
	}}
	got := discover.RemoteReadPaths(spec, kubebuilderManager())
	want := []string{`^/tmp/k8s-webhook-server/serving-certs(/.*)?$`, "^/var/run/secrets/.*"}
	if !slices.Equal(got, want) {
		t.Errorf("RemoteReadPaths() = %v, want %v", got, want)
	}
}

func TestFindLeader(t *testing.T) {
	t.Parallel()

	pods := []corev1.Pod{
		{Name: "manager-abc"},
		{Name: "manager-def"},
	}
	leases := []coordinationv1.Lease{
		{Name: "other", Spec: coordinationv1.LeaseSpec{
			HolderIdentity: new("other-pod_1234"),
		}},
		{Name: "empty"},
		{Name: "manager-lock", Spec: coordinationv1.LeaseSpec{
			HolderIdentity: new("manager-def_5678"),
		}},
	}

	leader, err := discover.FindLeader(leases, pods)
	if err != nil {
		t.Fatalf("FindLeader() error = %v", err)
	}
	if leader.Lease.Name != "manager-lock" || leader.Pod.Name != "manager-def" {
		t.Errorf("FindLeader() = %s held by %s, want manager-lock held by manager-def",
			leader.Lease.Name, leader.Pod.Name)
	}

	if _, err := discover.FindLeader(leases[:2], pods); err == nil {
		t.Error("FindLeader() without a matching Lease should fail")
	}
}

func TestInheritArgs(t *testing.T) {
	t.Parallel()

	pod := &corev1.Pod{Name: "manager-abc", Namespace: "capk-system"}
	got := discover.InheritArgs(kubebuilderManager(), pod, []string{"--v=4"})

	wantArgs := []string{
		"--namespace=capk-system",
		"--feature-gates=Foo=true",
		"--name", "$(MISSING)",
		"--leader-elect=false",
		"--v=4",
	}
	if !slices.Equal(got.Args, wantArgs) {
		t.Errorf("Args = %q, want %q", got.Args, wantArgs)
	}
	wantDropped := []string{"--leader-elect", "--metrics-bind-address=:8443", "--health-probe-bind-address", ":8081"}
	if !slices.Equal(got.Dropped, wantDropped) {
		t.Errorf("Dropped = %q, want %q", got.Dropped, wantDropped)
	}
	if want := []string{"$(MISSING)"}; !slices.Equal(got.Unresolved, want) {
		t.Errorf("Unresolved = %q, want %q", got.Unresolved, want)
	}
}
