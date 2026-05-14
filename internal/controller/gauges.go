// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package controller

import (
	"context"
	"fmt"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	apiv1alpha1 "github.com/siderolabs/buildkit-cache-controller/api/v1alpha1"
	cbmetrics "github.com/siderolabs/buildkit-cache-controller/internal/metrics"
)

// GaugeRefresher is a manager.Runnable that periodically rescans buildkit
// Deployments + PVCs and updates the per-tier gauges. Decoupled from
// Reconcile so the work is O(1) per interval rather than O(numCRs) per
// reconcile cycle. Runs on every replica (not leader-gated) because each
// pod serves its own metrics endpoint.
type GaugeRefresher struct {
	Client   client.Client
	Interval time.Duration
}

var (
	_ manager.Runnable               = &GaugeRefresher{}
	_ manager.LeaderElectionRunnable = &GaugeRefresher{}
)

// NeedLeaderElection returns false: gauges are scraped from each replica
// independently, so every pod should refresh its own counters.
func (*GaugeRefresher) NeedLeaderElection() bool { return false }

// Start implements manager.Runnable; loops until ctx is canceled.
func (g *GaugeRefresher) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("gauge-refresher")

	if g.Interval <= 0 {
		g.Interval = 30 * time.Second
	}

	// One refresh up front so metrics aren't empty until the first tick.
	if err := g.refresh(ctx); err != nil {
		logger.Error(err, "initial gauge refresh failed")
	}

	ticker := time.NewTicker(g.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := g.refresh(ctx); err != nil {
				logger.Error(err, "gauge refresh failed")
			}
		}
	}
}

func (g *GaugeRefresher) refresh(ctx context.Context) error {
	var deps appsv1.DeploymentList
	if err := g.Client.List(ctx, &deps, client.MatchingLabels{"app": "buildkit-cache"}); err != nil {
		return fmt.Errorf("list deployments: %w", err)
	}

	var pvcs corev1.PersistentVolumeClaimList
	if err := g.Client.List(ctx, &pvcs, client.MatchingLabels{"app": "buildkit-cache"}); err != nil {
		return fmt.Errorf("list pvcs: %w", err)
	}

	cbmetrics.ActiveDeployments.Reset()

	for _, d := range deps.Items {
		tier := d.Labels[apiv1alpha1.CacheGroupLabel]
		arch := d.Labels["ci.siderolabs.com/arch"]
		ready := strconv.FormatBool(d.Status.ReadyReplicas > 0)
		cbmetrics.ActiveDeployments.WithLabelValues(tier, arch, ready).Inc()
	}

	cbmetrics.ActivePVCs.Reset()
	cbmetrics.StorageRequestBytes.Reset()

	for _, p := range pvcs.Items {
		tier := p.Labels[apiv1alpha1.CacheGroupLabel]
		cbmetrics.ActivePVCs.WithLabelValues(tier).Inc()

		if q, ok := p.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			cbmetrics.StorageRequestBytes.WithLabelValues(tier).Add(float64(q.Value()))
		}
	}

	return nil
}
