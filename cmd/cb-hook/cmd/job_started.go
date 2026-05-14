// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1alpha1 "github.com/siderolabs/buildkit-cache-controller/api/v1alpha1"
)

var jobStartedCmd = &cobra.Command{
	Use:     "job-started",
	Short:   "Create/refresh the CachedBuild CR and export buildkit endpoints to GITHUB_ENV.",
	Args:    cobra.ArbitraryArgs,
	PreRunE: requireEnabled,
	RunE: func(*cobra.Command, []string) error {
		ctx, cancel := hookContext()
		defer cancel()

		return runStarted(ctx)
	},
}

func runStarted(ctx context.Context) error {
	c, err := newClient()
	if err != nil {
		return err
	}

	ns, tier, cacheKey, err := getValues()
	if err != nil {
		return err
	}

	ts := nowTS()

	fmt.Fprintf(os.Stderr, "%s tier=%s key=%s cr=%s\n", logPrefix, tier, cacheKey, cacheKey)

	cb := &apiv1alpha1.CachedBuild{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cacheKey,
			Namespace: ns,
			Annotations: map[string]string{
				apiv1alpha1.LastActivityAnnotation: ts,
			},
		},
		Spec: apiv1alpha1.CachedBuildSpec{
			Tier: tier,
		},
	}

	if createErr := c.Create(ctx, cb); createErr != nil && !apierrors.IsAlreadyExists(createErr) {
		return fmt.Errorf("create CachedBuild: %w", createErr)
	}

	// Refresh annotation whether we just created the CR or it already
	// existed (multiple parallel jobs converge on one CR).
	if patchErr := patchLastActivity(ctx, c, ns, cacheKey, ts); patchErr != nil {
		return patchErr
	}

	fmt.Fprintf(os.Stderr, "%s waiting for %s to be Ready\n", logPrefix, cacheKey)

	endpoints, err := waitReady(ctx, c, ns, cacheKey)
	if err != nil {
		return err
	}

	return writeEnv(endpoints)
}
