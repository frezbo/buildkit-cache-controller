// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package metrics declares the controller's custom Prometheus collectors
// and registers them with controller-runtime's shared registry. The
// registry is exposed on the manager's metrics endpoint (default :8080).
package metrics

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const subsystem = "cb"

var (
	// Provisioned counts every Pending/Provisioning/Idle → Ready transition,
	// not just the first one per CR. Cold = the controller had to create
	// the PVC for this transition; warm = the PVC already existed (cache
	// survived an idle teardown or a CR delete/recreate). Per-transition
	// counting is what makes the warm/cold ratio a useful hit-rate KPI.
	Provisioned = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: subsystem,
			Name:      "provisioned_total",
			Help:      "Buildkit provisioning events (every 0→Ready transition), labeled by tier and whether the PVC was warm at this transition.",
		},
		[]string{"tier", "warm"},
	)

	// PhaseTransition tracks every observed phase change. Drives reawaken
	// cadence and idle-teardown frequency dashboards.
	PhaseTransition = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: subsystem,
			Name:      "phase_transition_total",
			Help:      "CachedBuild phase transitions, labeled by tier and from/to phase.",
		},
		[]string{"tier", "from", "to"},
	)

	// ProvisioningDuration measures the time between CR creation and the
	// first transition to Ready. Cold and warm distributions diverge.
	ProvisioningDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Subsystem: subsystem,
			Name:      "provisioning_duration_seconds",
			Help:      "Seconds from CachedBuild creation to first Ready, by tier and warm/cold.",
			Buckets:   prometheus.ExponentialBucketsRange(1, 600, 12),
		},
		[]string{"tier", "warm"},
	)

	// IdleTeardown counts pod teardowns triggered by the idle-TTL gate.
	IdleTeardown = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: subsystem,
			Name:      "idle_teardown_total",
			Help:      "Buildkit pods torn down for inactivity, by tier.",
		},
		[]string{"tier"},
	)

	// TierLookupFailure counts CachedBuilds whose referenced tier could
	// not be resolved (typo, deleted, missing manifest).
	TierLookupFailure = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: subsystem,
			Name:      "tier_lookup_failure_total",
			Help:      "CachedBuilds that referenced a nonexistent tier, by tier name.",
		},
		[]string{"tier"},
	)

	// ActiveDeployments is the live count of buildkit Deployments labeled
	// by tier, arch (the short form: amd64 / arm64 — what we store on the
	// Deployment label), and a ready flag (true once ReadyReplicas > 0).
	// Refreshed periodically by the GaugeRefresher manager Runnable.
	ActiveDeployments = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "active_deployments",
			Help:      "Live buildkit Deployment count, by tier, arch, and ready state.",
		},
		[]string{"tier", "arch", "ready"},
	)

	// ActivePVCs is the live PVC count by tier. Persists across pod idle
	// teardowns; only drops when CacheRetention GCs them.
	ActivePVCs = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "active_pvcs",
			Help:      "Live PVC count, by tier.",
		},
		[]string{"tier"},
	)

	// StorageRequestBytes is the sum of PVC requests.storage by tier — a
	// ceiling on disk reservation, not actual usage.
	StorageRequestBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "storage_request_bytes",
			Help:      "Sum of PVC spec.requests.storage by tier.",
		},
		[]string{"tier"},
	)
)

// WarmLabel formats the bool label value used by Provisioned and
// ProvisioningDuration so call sites stay one-liner.
func WarmLabel(warm bool) string {
	return strconv.FormatBool(warm)
}

func init() {
	ctrlmetrics.Registry.MustRegister(
		Provisioned,
		PhaseTransition,
		ProvisioningDuration,
		IdleTeardown,
		TierLookupFailure,
		ActiveDeployments,
		ActivePVCs,
		StorageRequestBytes,
	)
}
