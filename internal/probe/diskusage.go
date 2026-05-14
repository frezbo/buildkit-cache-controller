// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package probe queries buildkit pods for runtime state (currently disk
// usage). Talks gRPC over the per-platform Service; no pod-exec needed.
package probe

import (
	"context"
	"fmt"

	"github.com/moby/buildkit/client"
)

// DiskUsage returns the total on-disk cache size in bytes reported by the
// buildkit daemon at endpoint. Uses a fresh client per call — buildkit's
// gRPC connection is cheap; pooling adds complexity for no measurable win
// at our reconcile cadence.
func DiskUsage(ctx context.Context, endpoint string) (int64, error) {
	c, err := client.New(ctx, endpoint)
	if err != nil {
		return 0, fmt.Errorf("dial %s: %w", endpoint, err)
	}
	defer c.Close() //nolint:errcheck

	records, err := c.DiskUsage(ctx)
	if err != nil {
		return 0, fmt.Errorf("disk usage %s: %w", endpoint, err)
	}

	var total int64
	for _, r := range records {
		total += r.Size
	}

	return total, nil
}
