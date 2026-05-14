// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package v1alpha1

import (
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LastActivityAnnotation carries a RFC3339 timestamp written by the runner
// hooks on every job-started/job-completed event. The reconciler reads it to
// decide when to tear down idle buildkit pods.
const LastActivityAnnotation = "ci.siderolabs.com/last-activity"

// CachedBuildPhase represents the lifecycle stage of a CachedBuild.
type CachedBuildPhase string

const (
	PhasePending      CachedBuildPhase = "Pending"
	PhaseProvisioning CachedBuildPhase = "Provisioning"
	PhaseReady        CachedBuildPhase = "Ready"
	PhaseIdle         CachedBuildPhase = "Idle"
	PhaseFailed       CachedBuildPhase = "Failed"
)

// CachedBuildSpec is the desired state of a CachedBuild. The CR's
// metadata.name is the stable cache identity (and the PVC/Deployment/
// Service naming root); the tier governs every other property
// (platforms, resources, affinity, idle behavior, image).
type CachedBuildSpec struct {
	// Tier names a CachedBuildTier (in the same namespace) whose spec
	// drives placement, resources, platforms, and idle behavior.
	// +kubebuilder:validation:MinLength=1
	Tier string `json:"tier"`
}

// CachedBuildEndpoint exposes the buildkit endpoint for a single platform.
type CachedBuildEndpoint struct {
	Platform string `json:"platform"`
	Endpoint string `json:"endpoint"`
}

// CachedBuildStatus is the observed state of a CachedBuild.
type CachedBuildStatus struct {
	Phase CachedBuildPhase `json:"phase,omitempty"`

	// Endpoints lists the active per-platform buildkit endpoints, suitable
	// for `buildx --driver=remote`.
	// +optional
	Endpoints []CachedBuildEndpoint `json:"endpoints,omitempty"`

	// PVCs lists the PVC names backing this cache (one per platform).
	// +optional
	PVCs []string `json:"pvcs,omitempty"`

	// Deployments lists the buildkit Deployment names currently serving
	// this cache (one per platform). When the CR is Idle the Deployments
	// exist but are scaled to zero replicas.
	// +optional
	Deployments []string `json:"deployments,omitempty"`

	// LastActivity reflects the most recent LastActivityAnnotation value
	// observed by the controller. Drives idle-TTL teardown.
	// +optional
	LastActivity *v1.Time `json:"lastActivity,omitempty"`

	// CacheSize is a humanized total of CacheUsage across all platforms
	// ("1.2Gi"). Surfaced as a kubectl printColumn for at-a-glance reads.
	// +optional
	CacheSize string `json:"cacheSize,omitempty"`

	// CacheUsage reports the current on-disk cache size as observed via
	// buildkit's DiskUsage gRPC, per platform.
	// +optional
	CacheUsage []CacheUsageEntry `json:"cacheUsage,omitempty"`

	// Conditions surfaces detailed reconcile state.
	// +optional
	Conditions []v1.Condition `json:"conditions,omitempty"`
}

// CacheUsageEntry reports the on-disk cache size for one platform pod.
type CacheUsageEntry struct {
	ObservedAt v1.Time `json:"observedAt,omitempty"`
	Platform   string  `json:"platform"`
	Bytes      int64   `json:"bytes"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=cb
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Tier",type=string,JSONPath=`.spec.tier`
// +kubebuilder:printcolumn:name="Size",type=string,JSONPath=`.status.cacheSize`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CachedBuild represents one buildkit cache identity backed by a named PVC
// and an ephemeral, on-demand buildkit Pod.
type CachedBuild struct {
	v1.TypeMeta   `json:",inline"`
	v1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CachedBuildSpec   `json:"spec,omitempty"`
	Status CachedBuildStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CachedBuildList is a collection of CachedBuild objects.
type CachedBuildList struct {
	v1.TypeMeta `json:",inline"`
	v1.ListMeta `json:"metadata,omitempty"`
	Items       []CachedBuild `json:"items"`
}
