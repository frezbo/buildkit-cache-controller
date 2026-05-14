// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1alpha1 "github.com/siderolabs/buildkit-cache-controller/api/v1alpha1"
)

const (
	defaultWaitTimeout = 5 * time.Minute
	pollInterval       = 2 * time.Second
)

var keySanitize = regexp.MustCompile(`[^a-z0-9-]+`)

// requireEnabled is a PreRunE that gates job-started/job-completed on
// CB_ENABLED=true. Silent no-op (exit 0) otherwise — runners always invoke
// the configured hook even when the feature is off.
func requireEnabled(*cobra.Command, []string) error {
	if os.Getenv("CB_ENABLED") != "true" {
		fmt.Fprintln(os.Stderr, logPrefix, "CB_ENABLED is not 'true', skipping hook")

		os.Exit(0)
	}

	return nil
}

// hookContext returns a context canceled on SIGINT/SIGTERM and bounded by
// CB_WAIT_TIMEOUT (default 5m). Returned cancel must be called by caller.
func hookContext() (context.Context, context.CancelFunc) {
	signalCtx, cancelSig := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)

	ctx, cancelTimeout := context.WithTimeout(signalCtx, waitTimeout())

	return ctx, func() {
		cancelTimeout()
		cancelSig()
	}
}

func waitTimeout() time.Duration {
	if v := os.Getenv("CB_WAIT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}

	return defaultWaitTimeout
}

func nowTS() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func newClient() (client.Client, error) {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("load in-cluster config: %w", err)
	}

	scheme := clientgoscheme.Scheme
	utilruntime.Must(apiv1alpha1.AddToScheme(scheme))

	return client.New(cfg, client.Options{Scheme: scheme})
}

// getValues returns namespace, tier, cacheKey from env vars, with validation. Used by both job-started and job-completed hooks.
func getValues() (string, string, string, error) {
	ns := os.Getenv("CB_NAMESPACE")
	if ns == "" {
		return "", "", "", fmt.Errorf("CB_NAMESPACE env var not set")
	}

	tier := os.Getenv("CB_TIER")
	if tier == "" {
		return "", "", "", fmt.Errorf("CB_TIER env var not set")
	}

	raw := os.Getenv("CACHE_KEY")
	if raw == "" {
		raw = os.Getenv("GITHUB_REPOSITORY")
	}

	if raw == "" {
		return "", "", "", fmt.Errorf("CACHE_KEY and GITHUB_REPOSITORY env vars are both empty")
	}

	key := keySanitize.ReplaceAllString(strings.ToLower(raw), "-")
	key = strings.Trim(key, "-")

	if key == "" {
		return "", "", "", fmt.Errorf("CACHE_KEY/GITHUB_REPOSITORY %q sanitizes to an empty string", raw)
	}

	// 48-char budget: PVC names are bk-cache-<key>-<arch> (9 + key + 6),
	// must fit 63-char DNS label. Truncate with sha256 suffix so distinct
	// long keys don't collide on the same prefix.
	const maxKeyLen = 48
	if len(key) > maxKeyLen {
		sum := sha256.Sum256([]byte(raw))
		key = key[:maxKeyLen-9] + "-" + hex.EncodeToString(sum[:])[:8]
	}

	return ns, tier, key, nil
}

func patchLastActivity(ctx context.Context, c client.Client, ns, name, ts string) error {
	body, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]string{
				apiv1alpha1.LastActivityAnnotation: ts,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("marshal annotation patch: %w", err)
	}

	target := &apiv1alpha1.CachedBuild{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
	}

	if err := c.Patch(ctx, target, client.RawPatch(types.MergePatchType, body)); err != nil {
		return fmt.Errorf("annotate lastActivity: %w", err)
	}

	return nil
}

func waitReady(ctx context.Context, c client.Client, ns, name string) ([]apiv1alpha1.CachedBuildEndpoint, error) {
	var endpoints []apiv1alpha1.CachedBuildEndpoint

	err := wait.PollUntilContextCancel(ctx, pollInterval, true, func(ctx context.Context) (bool, error) {
		var cb apiv1alpha1.CachedBuild

		if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &cb); err != nil {
			return false, err
		}

		if cb.Status.Phase == apiv1alpha1.PhaseReady && len(cb.Status.Endpoints) > 0 {
			endpoints = cb.Status.Endpoints

			return true, nil
		}

		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("wait for Ready: %w", err)
	}

	return endpoints, nil
}

func writeEnv(endpoints []apiv1alpha1.CachedBuildEndpoint) error {
	envPath := os.Getenv("GITHUB_ENV")

	if envPath == "" {
		return fmt.Errorf("GITHUB_ENV not set")
	}

	f, err := os.OpenFile(envPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open GITHUB_ENV: %w", err)
	}

	defer f.Close() //nolint:errcheck

	var defaultEp string

	for _, e := range endpoints {
		arch := e.Platform
		if i := strings.LastIndex(arch, "/"); i >= 0 {
			arch = arch[i+1:]
		}

		varName := "BUILDKIT_ENDPOINT_" + strings.ToUpper(arch)
		if _, err := fmt.Fprintf(f, "%s=%s\n", varName, e.Endpoint); err != nil {
			return fmt.Errorf("write GITHUB_ENV: %w", err)
		}

		fmt.Fprintf(os.Stderr, "%s %s=%s\n", logPrefix, varName, e.Endpoint)

		if defaultEp == "" {
			defaultEp = e.Endpoint
		}
	}

	if defaultEp == "" {
		return fmt.Errorf("no endpoints reported by CR status")
	}

	if _, err := fmt.Fprintf(f, "BUILDKIT_ENDPOINT=%s\n", defaultEp); err != nil {
		return fmt.Errorf("write GITHUB_ENV: %w", err)
	}

	fmt.Fprintf(os.Stderr, "%s BUILDKIT_ENDPOINT=%s\n", logPrefix, defaultEp)

	return nil
}
