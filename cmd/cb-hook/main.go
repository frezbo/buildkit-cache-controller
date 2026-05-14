// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// cb-hook is the actions/runner JOB_STARTED / JOB_COMPLETED hook for the
// buildkit-cache-controller. The runner rejects hook paths that don't end
// in .sh/.ps1/.js, so tiny shell shims (job-started.sh / job-completed.sh)
// exec this binary with the matching subcommand. Honors CB_ENABLED
// ("true" = active); silent no-op otherwise.
package main

import "github.com/siderolabs/buildkit-cache-controller/cmd/cb-hook/cmd"

func main() {
	cmd.Execute()
}
