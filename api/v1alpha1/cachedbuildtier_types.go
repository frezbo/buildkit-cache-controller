// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CacheGroupLabel is set on every buildkit pod so anti-affinity selectors
// can target fellow pods of the same tier.
const CacheGroupLabel = "ci.siderolabs.com/cache-group"

// CachedBuildTierSpec is the desired state of a tier.
type CachedBuildTierSpec struct {
	// Affinity is copied verbatim onto the buildkit pod spec. Tier authors
	// typically express hostname-based pod anti-affinity here, selecting
	// pods labeled `ci.siderolabs.com/cache-group: <tier-name>`.
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// CacheRetention, when set, opts the tier into whole-cache garbage
	// collection: any CachedBuild whose lastActivity is older than this
	// duration has its PVC and CR deleted (the buildkit pod cascade-deletes
	// via ownerRef). When nil (the default), the cache is retained
	// indefinitely; buildkit-internal GC governed by BuildkitdConfigMap
	// keeps within-PVC content trimmed, but the PVC itself is never
	// reclaimed by the controller. Operators enable this when host disk
	// pressure warrants stricter cleanup of stale projects.
	// +optional
	CacheRetention *v1.Duration `json:"cacheRetention,omitempty"`

	// Privileged controls whether the buildkit container runs as
	// privileged. Default true: upstream buildkitd needs privileged for
	// the OCI worker (overlayfs, mknod, mount). Set false when pairing
	// with a rootless buildkit image / runtime — PodSecurity-restricted
	// namespaces can then host the tier.
	// +kubebuilder:default=true
	Privileged *bool `json:"privileged,omitempty"`

	// Storage is the requested capacity for the per-cache PVC. Mirrors
	// PersistentVolume.spec.capacity.storage.
	// +kubebuilder:validation:Required
	Storage resource.Quantity `json:"storage"`

	// StorageClassName is the StorageClass used to provision the per-cache
	// PVC. Required; the chosen class should support WaitForFirstConsumer
	// so PVCs bind to the node where the pod first schedules and stay there
	// across reawakens.
	// +kubebuilder:validation:MinLength=1
	StorageClassName string `json:"storageClassName"`

	// Image is the buildkit container image used for CachedBuilds bound to
	// this tier. Required: pin a tag explicitly so cache behavior is
	// reproducible across tier-image upgrades.
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// BuildkitdConfigMap names a ConfigMap (in the CachedBuild's namespace)
	// holding a buildkitd.toml with the daemon configuration — typically
	// GC policies tuned for the tier's PVC capacity. When set, the
	// ConfigMap is mounted read-only at /etc/buildkit/ on the buildkit
	// pod. When unset, buildkit uses its built-in defaults.
	// +optional
	BuildkitdConfigMap string `json:"buildkitdConfigMap,omitempty"`

	// Resources is applied to the buildkit pod.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Platforms lists the buildkit worker platforms provisioned for every
	// CachedBuild that uses this tier. Each entry produces one Pod and
	// Service named bk-<cacheKey>-<arch>, and one PVC named
	// bk-cache-<cacheKey>-<arch>.
	// +kubebuilder:validation:MinItems=1
	Platforms []string `json:"platforms"`

	// IdleTTL is the duration of inactivity (no LastActivityAnnotation
	// updates) after which the controller tears down the buildkit pods.
	// PVCs are retained so a subsequent reapply reawakens the cache on
	// the same node.
	// +kubebuilder:default="30m"
	IdleTTL v1.Duration `json:"idleTTL,omitempty"`
}

// CachedBuildTierStatus is the observed state of a tier.
type CachedBuildTierStatus struct {
	// Conditions surfaces validation state.
	// +optional
	Conditions []v1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=cbt
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CachedBuildTier defines the resource profile and scheduling affinity
// applied to CachedBuilds that reference this tier by name. Lives in the
// same namespace as the CachedBuilds that reference it.
//
//nolint:govet // fieldalignment: k8s convention dictates TypeMeta/ObjectMeta first.
type CachedBuildTier struct {
	v1.TypeMeta   `json:",inline"`
	v1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CachedBuildTierSpec   `json:"spec,omitempty"`
	Status CachedBuildTierStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CachedBuildTierList is a collection of CachedBuildTier objects.
type CachedBuildTierList struct {
	v1.TypeMeta `json:",inline"`
	v1.ListMeta `json:"metadata,omitempty"`
	Items       []CachedBuildTier `json:"items"`
}
