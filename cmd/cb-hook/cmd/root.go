// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

const logPrefix = "[cached-build-hook]"

var rootCmd = &cobra.Command{
	Use:   "cb-hook",
	Short: "actions/runner hook for the buildkit-cache-controller.",
	Long: `cb-hook is the actions/runner JOB_STARTED / JOB_COMPLETED hook for the
buildkit-cache-controller. Invoked via job-started.sh / job-completed.sh
shell shims (.sh suffix required by the runner). Honors CB_ENABLED ("true"
= active); silent no-op otherwise.`,
}

func init() {
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(jobStartedCmd)
	rootCmd.AddCommand(jobCompletedCmd)
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, logPrefix, err)

		os.Exit(1)
	}
}
