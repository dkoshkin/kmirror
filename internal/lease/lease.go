// Copyright 2026 Dimitri Koshkin. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package lease takes over a leader-election Lease so the in-cluster controller stops reconciling.
package lease

import (
	"context"
	"fmt"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coordinationclient "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
)

// Holder claims a Lease under its own identity and keeps renewing it.
type Holder struct {
	Client    coordinationclient.LeasesGetter
	Namespace string
	Name      string
	// Identity is written to the Lease's holderIdentity.
	Identity string
	// Duration is the Lease duration while held. It is renewed every Duration/4.
	Duration time.Duration
}

// Acquire writes the holder identity to the Lease, regardless of the current holder.
// The current holder fails its next renew and steps down.
func (h *Holder) Acquire(ctx context.Context) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		l, err := h.Client.Leases(h.Namespace).Get(ctx, h.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		now := metav1.NewMicroTime(time.Now())
		if l.Spec.HolderIdentity == nil || *l.Spec.HolderIdentity != h.Identity {
			l.Spec.AcquireTime = &now
			l.Spec.LeaseTransitions = new(ptr.Deref(l.Spec.LeaseTransitions, 0) + 1)
		}
		l.Spec.HolderIdentity = new(h.Identity)
		l.Spec.LeaseDurationSeconds = new(int32(h.Duration.Seconds()))
		l.Spec.RenewTime = &now
		_, err = h.Client.Leases(h.Namespace).Update(ctx, l, metav1.UpdateOptions{})
		return err
	})
}

// Renew refreshes the Lease every Duration/4 until ctx is done.
// Failures are reported to onError and retried on the next tick.
func (h *Holder) Renew(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(h.Duration / 4)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := h.Acquire(ctx); err != nil && ctx.Err() == nil {
				onError(fmt.Errorf("renewing Lease %s/%s: %w", h.Namespace, h.Name, err))
			}
		}
	}
}

// Release deletes the Lease if it is still held by this holder, so the in-cluster controller
// acquires it immediately instead of waiting for it to expire. It reports whether it deleted the Lease.
func (h *Holder) Release(ctx context.Context) (bool, error) {
	l, err := h.Client.Leases(h.Namespace).Get(ctx, h.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !h.holds(l) {
		return false, nil
	}
	err = h.Client.Leases(h.Namespace).Delete(ctx, h.Name, metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{ResourceVersion: &l.ResourceVersion},
	})
	if apierrors.IsNotFound(err) || apierrors.IsConflict(err) {
		return false, nil
	}
	return err == nil, err
}

func (h *Holder) holds(l *coordinationv1.Lease) bool {
	return l.Spec.HolderIdentity != nil && *l.Spec.HolderIdentity == h.Identity
}
