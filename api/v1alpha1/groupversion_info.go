// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package v1alpha1 contains API Schema definitions for the ci v1alpha1 API
// group, namely the CachedBuild custom resource.
//
// +kubebuilder:object:generate=true
// +groupName=ci.siderolabs.com
package v1alpha1

//go:generate go tool controller-gen object paths=./...
//go:generate go tool controller-gen crd paths=./... output:crd:dir=../../deploy/manifests
//go:generate go tool controller-gen crd paths=./... output:crd:dir=../../deploy/helm/buildkit-cache-controller/crds

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupVersion is the API Group Version used by this package.
var GroupVersion = schema.GroupVersion{Group: "ci.siderolabs.com", Version: "v1alpha1"}

// SchemeBuilder registers our types into a runtime.Scheme using the
// minimal-deps apimachinery builder (controller-runtime's scheme.Builder
// is deprecated for api packages).
var SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

// AddToScheme adds the types in this group-version to the given scheme.
var AddToScheme = SchemeBuilder.AddToScheme

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&CachedBuild{}, &CachedBuildList{},
		&CachedBuildTier{}, &CachedBuildTierList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)

	return nil
}
