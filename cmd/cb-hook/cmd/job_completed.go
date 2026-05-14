// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var jobCompletedCmd = &cobra.Command{
	Use:     "job-completed",
	Short:   "Refresh the CachedBuild lastActivity annotation so idle-TTL starts now.",
	Args:    cobra.ArbitraryArgs,
	PreRunE: requireEnabled,
	RunE: func(*cobra.Command, []string) error {
		ctx, cancel := hookContext()
		defer cancel()

		return runCompleted(ctx)
	},
}

func runCompleted(ctx context.Context) error {
	c, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s job-completed: %v\n", logPrefix, err)

		return nil
	}

	ns, _, cacheKey, err := getValues()
	if err != nil {
		return err
	}

	ts := nowTS()

	if err := patchLastActivity(ctx, c, ns, cacheKey, ts); err != nil {
		fmt.Fprintf(os.Stderr, "%s lastActivity patch failed (CR=%s): %v\n", logPrefix, cacheKey, err)
	}

	fmt.Fprintf(os.Stderr, "%s job completed; lastActivity=%s\n", logPrefix, ts)

	return nil
}
