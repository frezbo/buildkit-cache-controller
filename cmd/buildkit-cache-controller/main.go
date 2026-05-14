// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// buildkit-cache-controller reconciles CachedBuild resources, materializing
// them as buildkit Pods + named PVCs for cache-aware CI builds.
package main

import "github.com/siderolabs/buildkit-cache-controller/cmd/buildkit-cache-controller/cmd"

func main() {
	cmd.Execute()
}
