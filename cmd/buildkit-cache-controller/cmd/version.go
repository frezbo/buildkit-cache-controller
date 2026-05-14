// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/siderolabs/buildkit-cache-controller/internal/version"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Prints buildkit-cache-controller version.",
	Long:  `Prints buildkit-cache-controller version.`,
	Args:  cobra.NoArgs,
	Run: func(*cobra.Command, []string) {
		fmt.Printf("%s version %s (%s)\n", version.Name, version.Tag, version.SHA)
	},
}
