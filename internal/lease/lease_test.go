// Copyright 2026 Dimitri Koshkin. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package lease_test

import (
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	"github.com/dkoshkin/kmirror/internal/lease"
	"github.com/dkoshkin/kmirror/test/helpers"
)

func TestHolder(t *testing.T) {
	t.Parallel()

	env, err := helpers.NewTestEnvironmentConfiguration().Build()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })
	client := kubernetes.NewForConfigOrDie(env.GetConfig()).CoordinationV1()
	ctx := t.Context()

	_, err = client.Leases("default").Create(ctx, &coordinationv1.Lease{
		Name: "manager-lock",
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       new("manager-abc_1234"),
			LeaseDurationSeconds: ptr.To[int32](15),
			LeaseTransitions:     ptr.To[int32](2),
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	holder := &lease.Holder{
		Client: client, Namespace: "default", Name: "manager-lock", Identity: "kmirror-test", Duration: time.Minute,
	}
	if err := holder.Acquire(ctx); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	l, err := client.Leases("default").Get(ctx, "manager-lock", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if *l.Spec.HolderIdentity != "kmirror-test" || *l.Spec.LeaseDurationSeconds != 60 ||
		*l.Spec.LeaseTransitions != 3 || l.Spec.RenewTime == nil || l.Spec.AcquireTime == nil {
		t.Errorf("Lease after Acquire() = %+v", l.Spec)
	}

	// Another holder takes it: Release must leave it alone.
	l.Spec.HolderIdentity = new("manager-abc_5678")
	if _, err := client.Leases("default").Update(ctx, l, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if released, err := holder.Release(ctx); err != nil || released {
		t.Errorf("Release() of a Lease held by someone else = %v, %v; want false, nil", released, err)
	}

	if err := holder.Acquire(ctx); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if released, err := holder.Release(ctx); err != nil || !released {
		t.Errorf("Release() = %v, %v; want true, nil", released, err)
	}
	if _, err := client.Leases("default").Get(ctx, "manager-lock", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("Lease still exists after Release(): %v", err)
	}
	if released, err := holder.Release(ctx); err != nil || released {
		t.Errorf("Release() of a missing Lease = %v, %v; want false, nil", released, err)
	}
}
