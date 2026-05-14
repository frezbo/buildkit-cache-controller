// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package cmd

import (
	"flag"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	apiv1alpha1 "github.com/siderolabs/buildkit-cache-controller/api/v1alpha1"
	"github.com/siderolabs/buildkit-cache-controller/internal/controller"
)

var runFlags struct {
	zapOpts           zap.Options
	metricsAddr       string
	probeAddr         string
	leaderID          string
	leaderNS          string
	allowedNamespaces []string
	leaderElect       bool
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the CachedBuild reconciler manager.",
	Args:  cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		return runManager()
	},
}

func init() {
	runCmd.Flags().StringVar(&runFlags.metricsAddr, "metrics-bind-address", ":8080", "Address the Prometheus metrics endpoint binds to.")
	runCmd.Flags().StringVar(&runFlags.probeAddr, "health-probe-bind-address", ":8081", "Address the readiness/liveness probe endpoint binds to.")
	runCmd.Flags().BoolVar(&runFlags.leaderElect, "leader-elect", false, "Enable leader election for high availability.")
	runCmd.Flags().StringVar(&runFlags.leaderID, "leader-elect-id", "buildkit-cache-controller.siderolabs.com", "Leader election lease ID.")
	runCmd.Flags().StringVar(&runFlags.leaderNS, "leader-elect-namespace", "ci", "Namespace for the leader election Lease.")
	runCmd.Flags().StringSliceVar(&runFlags.allowedNamespaces, "allowed-namespaces", nil,
		"Comma-separated namespaces the controller is allowed to materialize buildkit Deployments/Services/PVCs in. "+
			"Required: every watch (CRs + Deployments/Services/PVCs) and every mutation is scoped to these namespaces. "+
			"CRs created elsewhere are invisible to the informer cache and won't be reconciled.")

	// Bridge zap controller-runtime flags (stdlib flag) into cobra/pflag.
	zapFlags := flag.NewFlagSet("zap", flag.ContinueOnError)
	runFlags.zapOpts = zap.Options{Development: false}
	runFlags.zapOpts.BindFlags(zapFlags)
	runCmd.Flags().AddGoFlagSet(zapFlags)
}

func runManager() error {
	if len(runFlags.allowedNamespaces) == 0 {
		return fmt.Errorf("--allowed-namespaces is required (no default; controller must be told which namespaces it may write into)")
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&runFlags.zapOpts)))

	logger := ctrl.Log.WithName("setup")

	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("load Kubernetes config: %w", err)
	}

	utilruntime.Must(clientgoscheme.AddToScheme(clientgoscheme.Scheme))
	utilruntime.Must(apiv1alpha1.AddToScheme(clientgoscheme.Scheme))

	nsCacheCfg := make(map[string]cache.Config, len(runFlags.allowedNamespaces))
	for _, ns := range runFlags.allowedNamespaces {
		nsCacheCfg[ns] = cache.Config{}
	}

	// Scope every watch — CRs and managed resources alike — to the allowed
	// namespaces. No cluster-wide reads at all; the controller only ever
	// sees objects in namespaces the operator has explicitly opted in.
	// CRs created elsewhere are invisible to the informer cache (the
	// per-ns Role wouldn't grant the writes anyway).
	scopedByObject := map[client.Object]cache.ByObject{
		&appsv1.Deployment{}:            {Namespaces: nsCacheCfg},
		&corev1.Service{}:               {Namespaces: nsCacheCfg},
		&corev1.PersistentVolumeClaim{}: {Namespaces: nsCacheCfg},
		&apiv1alpha1.CachedBuild{}:      {Namespaces: nsCacheCfg},
		&apiv1alpha1.CachedBuildTier{}:  {Namespaces: nsCacheCfg},
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                  clientgoscheme.Scheme,
		Metrics:                 metricsserver.Options{BindAddress: runFlags.metricsAddr},
		HealthProbeBindAddress:  runFlags.probeAddr,
		LeaderElection:          runFlags.leaderElect,
		LeaderElectionID:        runFlags.leaderID,
		LeaderElectionNamespace: runFlags.leaderNS,
		Cache:                   cache.Options{ByObject: scopedByObject},
	})
	if err != nil {
		return fmt.Errorf("create manager: %w", err)
	}

	if err := (&controller.Reconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("create CachedBuild controller: %w", err)
	}

	// Periodic gauge refresh — decoupled from Reconcile so the cluster-wide
	// list of buildkit Deployments/PVCs runs once per tick instead of once
	// per CR reconcile.
	if err := mgr.Add(&controller.GaugeRefresher{
		Client:   mgr.GetClient(),
		Interval: 30 * time.Second,
	}); err != nil {
		return fmt.Errorf("register gauge refresher: %w", err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("set up healthz check: %w", err)
	}

	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("set up readyz check: %w", err)
	}

	logger.Info("starting manager",
		"metrics", runFlags.metricsAddr,
		"probe", runFlags.probeAddr,
		"leader-election", runFlags.leaderElect,
		"allowed-namespaces", runFlags.allowedNamespaces,
	)

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		return fmt.Errorf("manager exited with error: %w", err)
	}

	return nil
}
