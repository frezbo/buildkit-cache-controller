// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package controller implements the CachedBuild reconciler.
package controller

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	apiv1alpha1 "github.com/siderolabs/buildkit-cache-controller/api/v1alpha1"
	cbmetrics "github.com/siderolabs/buildkit-cache-controller/internal/metrics"
	"github.com/siderolabs/buildkit-cache-controller/internal/probe"
)

// diskUsageProbeTimeout caps each per-platform DiskUsage gRPC call so a
// slow buildkit pod can't stall the reconcile loop.
const diskUsageProbeTimeout = 5 * time.Second

// firstReadyCondition marks that a CachedBuild has reached Ready at least
// once since creation. The reconciler uses this gate to emit
// ProvisioningDuration exactly once per CR lifetime.
const firstReadyCondition = "FirstReady"

// cacheFinalizer ensures PVC cleanup runs whenever a CachedBuild is
// deleted — whether by the controller's cache GC or manual kubectl delete.
// Pods + Services cascade via ownerRef; PVCs are explicit because they
// intentionally outlive normal CR lifecycle.
const cacheFinalizer = "ci.siderolabs.com/cachedbuild-pvc-cleanup"

// requeueAfter governs how often the reconciler polls for idle TTL
// expiry on already-Ready CRs.
const requeueAfter = 1 * time.Minute

// Reconciler manages CachedBuild resources.
type Reconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// SetupWithManager wires the Reconciler into the manager's controller chain.
// Pods carry an ownerRef to their CachedBuild so cascade-delete works on CR
// removal. PVCs intentionally have no ownerRef — the cache outlives the CR.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&apiv1alpha1.CachedBuild{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Named("cachedbuild").
		Complete(r)
}

// Reconcile materializes one CachedBuild's PVCs and Pods, updates its
// status, and tears down idle pods past spec.idleTtl.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var cb apiv1alpha1.CachedBuild
	if err := r.Get(ctx, req.NamespacedName, &cb); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	if res, done, err := r.handleDeletionAndFinalizer(ctx, &cb); done || err != nil {
		return res, err
	}

	oldPhase := cb.Status.Phase

	tier, done, err := r.fetchTier(ctx, &cb, oldPhase)
	if done || err != nil {
		return ctrl.Result{}, err
	}

	res, done, err := r.handleIdlePhases(ctx, &cb, tier, oldPhase, logger)
	if done || err != nil {
		return res, err
	}

	platformState, err := r.ensurePlatforms(ctx, &cb, tier)
	if err != nil {
		return ctrl.Result{}, err
	}

	return r.persistReconcileStatus(ctx, &cb, tier, oldPhase, platformState)
}

// handleDeletionAndFinalizer covers the two terminal paths that gate
// normal reconciliation: deletion-with-finalizer cleanup, and adding the
// finalizer on a fresh CR.
func (r *Reconciler) handleDeletionAndFinalizer(ctx context.Context, cb *apiv1alpha1.CachedBuild) (ctrl.Result, bool, error) {
	if !cb.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(cb, cacheFinalizer) {
			if err := r.deletePVCs(ctx, cb); err != nil {
				return ctrl.Result{}, true, err
			}

			controllerutil.RemoveFinalizer(cb, cacheFinalizer)

			if err := r.Update(ctx, cb); err != nil {
				if apierrors.IsConflict(err) {
					return ctrl.Result{Requeue: true}, true, nil
				}

				return ctrl.Result{}, true, err
			}
		}
		// Pods + Services cascade via ownerRef.
		return ctrl.Result{}, true, nil
	}

	if !controllerutil.ContainsFinalizer(cb, cacheFinalizer) {
		controllerutil.AddFinalizer(cb, cacheFinalizer)

		if err := r.Update(ctx, cb); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, true, nil
			}

			return ctrl.Result{}, true, err
		}

		return ctrl.Result{Requeue: true}, true, nil
	}

	return ctrl.Result{}, false, nil
}

// fetchTier resolves the referenced CachedBuildTier; on miss it marks the
// CR Failed and short-circuits reconciliation. done=true means the caller
// should return immediately with (res, err).
func (r *Reconciler) fetchTier(ctx context.Context, cb *apiv1alpha1.CachedBuild, oldPhase apiv1alpha1.CachedBuildPhase) (*apiv1alpha1.CachedBuildTier, bool, error) {
	var tier apiv1alpha1.CachedBuildTier

	err := r.Get(ctx, types.NamespacedName{Namespace: cb.Namespace, Name: cb.Spec.Tier}, &tier)
	if err == nil {
		return &tier, false, nil
	}

	// Transient API errors propagate without touching status or metrics.
	// Only a real NotFound counts as operator misconfiguration — that's
	// the signal dashboards care about.
	if !apierrors.IsNotFound(err) {
		return nil, true, fmt.Errorf("lookup tier %q: %w", cb.Spec.Tier, err)
	}

	// Tier-miss is an operator misconfiguration; controller-runtime's
	// rate-limited backoff is the right retry shape (RequeueAfter is
	// ignored when err != nil). Only bump the metric + flip status on
	// the transition INTO Failed — re-NotFound on the next retry would
	// otherwise overcount a single misconfig into an ever-growing series.
	if oldPhase == apiv1alpha1.PhaseFailed {
		return nil, true, fmt.Errorf("lookup tier %q: %w", cb.Spec.Tier, err)
	}

	cbmetrics.TierLookupFailure.WithLabelValues(cb.Spec.Tier).Inc()
	cb.Status.Phase = apiv1alpha1.PhaseFailed

	if updateErr := r.Status().Update(ctx, cb); updateErr != nil {
		return nil, true, updateErr
	}

	emitPhaseTransition(cb.Spec.Tier, oldPhase, apiv1alpha1.PhaseFailed)

	return nil, true, fmt.Errorf("lookup tier %q: %w", cb.Spec.Tier, err)
}

// handleIdlePhases bundles the three early-return paths driven by
// lastActivity: whole-cache GC, the Idle short-circuit (already idle,
// activity not yet fresh), and idle teardown of pods past idleTTL.
func (r *Reconciler) handleIdlePhases(
	ctx context.Context,
	cb *apiv1alpha1.CachedBuild,
	tier *apiv1alpha1.CachedBuildTier,
	oldPhase apiv1alpha1.CachedBuildPhase,
	logger logr.Logger,
) (ctrl.Result, bool, error) {
	// Cache GC: when the tier opts in via spec.cacheRetention and the CR
	// has been idle past that duration, mark for deletion. The finalizer
	// above handles PVC cleanup; pods + services cascade via ownerRef.
	if r.shouldGarbageCollect(cb, tier) {
		if err := r.Delete(ctx, cb); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, true, err
		}

		logger.Info("garbage-collected stale cache", "cacheKey", cb.Name)

		return ctrl.Result{}, true, nil
	}

	// Stay idle when phase is already Idle and lastActivity hasn't refreshed
	// past idleTtl. Without this guard the next reconcile would ensure pods
	// → Ready → next-next reconcile tears down again → loop.
	if cb.Status.Phase == apiv1alpha1.PhaseIdle && !isFreshActivity(cb, tier) {
		return ctrl.Result{RequeueAfter: requeueAfter}, true, nil
	}

	// Idle teardown: if lastActivity is older than idleTtl, scale buildkit
	// Deployments to 0 (PVCs persist; the Deployment object stays so
	// reawaken is a simple scale-up to 1). Re-queue for the next cycle.
	if r.shouldTearDown(cb, tier) {
		if err := r.scaleDownDeployments(ctx, cb); err != nil {
			return ctrl.Result{}, true, err
		}

		cb.Status.Phase = apiv1alpha1.PhaseIdle
		cb.Status.Endpoints = nil
		cb.Status.Deployments = nil

		if err := r.Status().Update(ctx, cb); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, true, nil
			}

			return ctrl.Result{}, true, err
		}

		cbmetrics.IdleTeardown.WithLabelValues(cb.Spec.Tier).Inc()
		emitPhaseTransition(cb.Spec.Tier, oldPhase, apiv1alpha1.PhaseIdle)

		logger.Info("torn down idle buildkit pods", "cacheKey", cb.Name)

		return ctrl.Result{}, true, nil
	}

	return ctrl.Result{}, false, nil
}

// platformReconcileState bundles per-platform results gathered in the
// ensure loop so persistReconcileStatus can update the CR status in one
// place.
type platformReconcileState struct {
	pvcs          []string
	deployments   []string
	endpoints     []apiv1alpha1.CachedBuildEndpoint
	cacheUsage    []apiv1alpha1.CacheUsageEntry
	allReady      bool
	anyPVCCreated bool
}

func (r *Reconciler) ensurePlatforms(ctx context.Context, cb *apiv1alpha1.CachedBuild, tier *apiv1alpha1.CachedBuildTier) (*platformReconcileState, error) {
	platforms := tier.Spec.Platforms

	state := &platformReconcileState{
		pvcs:        make([]string, 0, len(platforms)),
		deployments: make([]string, 0, len(platforms)),
		endpoints:   make([]apiv1alpha1.CachedBuildEndpoint, 0, len(platforms)),
		cacheUsage:  make([]apiv1alpha1.CacheUsageEntry, 0, len(platforms)),
		allReady:    true,
	}

	for _, platform := range platforms {
		if err := r.ensurePlatform(ctx, cb, tier, platform, state); err != nil {
			return nil, err
		}
	}

	return state, nil
}

func (r *Reconciler) ensurePlatform(
	ctx context.Context,
	cb *apiv1alpha1.CachedBuild,
	tier *apiv1alpha1.CachedBuildTier,
	platform string,
	state *platformReconcileState,
) error {
	pvc, created, err := r.ensurePVC(ctx, cb, platform, tier.Spec.StorageClassName, tier.Spec.Storage)
	if err != nil {
		return fmt.Errorf("ensure pvc for %s: %w", platform, err)
	}

	if created {
		state.anyPVCCreated = true
	}

	state.pvcs = append(state.pvcs, pvc.Name)

	dep, err := r.ensureDeployment(ctx, cb, platform, tier.Spec.Resources, tier.Spec.Affinity, tier.Spec.Image, tier.Spec.BuildkitdConfigMap, tier.Spec.Privileged, pvc.Name)
	if err != nil {
		return fmt.Errorf("ensure deployment for %s: %w", platform, err)
	}

	state.deployments = append(state.deployments, dep.Name)

	if err := r.ensureService(ctx, cb, platform); err != nil {
		return fmt.Errorf("ensure service for %s: %w", platform, err)
	}

	if !isDeploymentReady(dep) {
		state.allReady = false

		return nil
	}

	// Use the short <svc>.<ns>.svc form; the cluster's DNS search path
	// expands it to the configured cluster domain (default cluster.local).
	// This avoids hardcoding the suffix for clusters that customize it.
	endpoint := fmt.Sprintf("tcp://%s.%s.svc:1234", podName(cb, platform), cb.Namespace)
	state.endpoints = append(state.endpoints, apiv1alpha1.CachedBuildEndpoint{
		Platform: platform,
		Endpoint: endpoint,
	})

	if usage := probeDiskUsage(ctx, platform, endpoint); usage != nil {
		state.cacheUsage = append(state.cacheUsage, *usage)
	}

	return nil
}

func (r *Reconciler) persistReconcileStatus(
	ctx context.Context,
	cb *apiv1alpha1.CachedBuild,
	tier *apiv1alpha1.CachedBuildTier,
	oldPhase apiv1alpha1.CachedBuildPhase,
	state *platformReconcileState,
) (ctrl.Result, error) {
	cb.Status.PVCs = state.pvcs
	cb.Status.Deployments = state.deployments
	cb.Status.Endpoints = state.endpoints

	// Only overwrite usage when we got fresh probe data. Pods torn down
	// for idle leave the cache on PVC intact, so showing the last known
	// size is more useful than zeroing it out.
	if len(state.cacheUsage) > 0 {
		cb.Status.CacheUsage = state.cacheUsage
		cb.Status.CacheSize = humanize.IBytes(uint64(sumUsage(state.cacheUsage)))
	}

	newPhase := apiv1alpha1.PhaseProvisioning
	if state.allReady && len(state.endpoints) == len(tier.Spec.Platforms) {
		newPhase = apiv1alpha1.PhaseReady
	}

	cb.Status.Phase = newPhase
	if ts, ok := lastActivity(cb); ok {
		cb.Status.LastActivity = &metav1.Time{Time: ts}
	}

	r.recordReadyTransitionMetrics(cb, oldPhase, newPhase, !state.anyPVCCreated)

	if err := r.Status().Update(ctx, cb); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}

		return ctrl.Result{}, err
	}

	emitPhaseTransition(cb.Spec.Tier, oldPhase, newPhase)

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// recordReadyTransitionMetrics fires on every transition into Ready.
// The Provisioned counter bumps each time so the warm/cold ratio reflects
// the cache hit-rate across cold creates AND idle-reawakens. The
// ProvisioningDuration histogram is gated by the FirstReady status
// condition so it observes exactly once per CR lifetime (cold-create
// latency; warm reawakens would skew the distribution and are tracked
// separately by phase_transition_total).
func (r *Reconciler) recordReadyTransitionMetrics(cb *apiv1alpha1.CachedBuild, oldPhase, newPhase apiv1alpha1.CachedBuildPhase, warm bool) {
	if newPhase != apiv1alpha1.PhaseReady || oldPhase == apiv1alpha1.PhaseReady {
		return
	}

	cbmetrics.Provisioned.WithLabelValues(cb.Spec.Tier, cbmetrics.WarmLabel(warm)).Inc()

	if meta.IsStatusConditionTrue(cb.Status.Conditions, firstReadyCondition) {
		return
	}

	cbmetrics.ProvisioningDuration.
		WithLabelValues(cb.Spec.Tier, cbmetrics.WarmLabel(warm)).
		Observe(time.Since(cb.CreationTimestamp.Time).Seconds())
	meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{
		Type:    firstReadyCondition,
		Status:  metav1.ConditionTrue,
		Reason:  "Provisioned",
		Message: "buildkit pods reached Ready",
	})
}

// sumUsage totals bytes across all platforms in a CacheUsage slice.
func sumUsage(entries []apiv1alpha1.CacheUsageEntry) int64 {
	var total int64
	for _, e := range entries {
		total += e.Bytes
	}

	return total
}

// probeDiskUsage queries buildkit DiskUsage with a short timeout. Returns
// nil on any error — probe failure is informational, not reconcile-fatal.
func probeDiskUsage(ctx context.Context, platform, endpoint string) *apiv1alpha1.CacheUsageEntry {
	pctx, cancel := context.WithTimeout(ctx, diskUsageProbeTimeout)
	defer cancel()

	bytes, err := probe.DiskUsage(pctx, endpoint)
	if err != nil {
		log.FromContext(ctx).V(1).Info("disk usage probe failed", "endpoint", endpoint, "err", err)

		return nil
	}

	return &apiv1alpha1.CacheUsageEntry{
		Platform:   platform,
		Bytes:      bytes,
		ObservedAt: metav1.Now(),
	}
}

// emitPhaseTransition emits the phase_transition counter only when the
// phase actually changes (avoids spurious bumps from no-op reconciles).
func emitPhaseTransition(tier string, from, to apiv1alpha1.CachedBuildPhase) {
	if from == to {
		return
	}

	cbmetrics.PhaseTransition.WithLabelValues(tier, string(from), string(to)).Inc()
}

func pvcName(cb *apiv1alpha1.CachedBuild, platform string) string {
	return fmt.Sprintf("bk-cache-%s-%s", cb.Name, archFromPlatform(platform))
}

func podName(cb *apiv1alpha1.CachedBuild, platform string) string {
	return fmt.Sprintf("bk-%s-%s", cb.Name, archFromPlatform(platform))
}

func archFromPlatform(platform string) string {
	if i := strings.LastIndex(platform, "/"); i >= 0 {
		return platform[i+1:]
	}

	return platform
}

// ensurePVC returns the PVC and whether it was just created (cold) or
// already existed (warm). Cold means the cache will be populated from
// scratch on the first build; warm means buildkit reuses content from
// a previous build cycle.
func (r *Reconciler) ensurePVC(
	ctx context.Context,
	cb *apiv1alpha1.CachedBuild,
	platform string,
	storageClass string,
	storage resource.Quantity,
) (*corev1.PersistentVolumeClaim, bool, error) {
	name := pvcName(cb, platform)
	desiredLabels := cacheLabels(cb, archFromPlatform(platform))

	var pvc corev1.PersistentVolumeClaim

	err := r.Get(ctx, types.NamespacedName{Namespace: cb.Namespace, Name: name}, &pvc)
	if err == nil {
		// Spec is immutable + only the controller creates PVCs, so the
		// only thing that can drift is mutable metadata (notably the
		// tier label after a CachedBuild.spec.tier change).
		if !maps.Equal(pvc.Labels, desiredLabels) {
			pvc.Labels = desiredLabels
			if updateErr := r.Update(ctx, &pvc); updateErr != nil {
				return nil, false, fmt.Errorf("sync pvc labels: %w", updateErr)
			}
		}

		return &pvc, false, nil
	}

	if !apierrors.IsNotFound(err) {
		return nil, false, err
	}

	pvc = corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cb.Namespace,
			Labels:    desiredLabels,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: &storageClass,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: storage,
				},
			},
		},
	}
	if err := r.Create(ctx, &pvc); err != nil {
		return nil, false, err
	}

	return &pvc, true, nil
}

// cacheLabels is the canonical label set the controller stamps on every
// owned resource (PVC, Service, Deployment, pod template). Centralized so
// drift between resources is impossible and the sync-on-update paths only
// need to compare against this one source of truth.
func cacheLabels(cb *apiv1alpha1.CachedBuild, arch string) map[string]string {
	return map[string]string{
		"app":                       "buildkit-cache",
		apiv1alpha1.CacheGroupLabel: cb.Spec.Tier,
		"ci.siderolabs.com/cache":   cb.Name,
		"ci.siderolabs.com/arch":    arch,
	}
}

// ensureService creates a per-(CachedBuild,platform) Service fronting the
// buildkit pod. Buildx connects to the Service DNS over plain TCP — no
// pod-exec RBAC needed on the consumer side. The Service is owned by the
// CachedBuild so cascade-delete cleans it up.
func (r *Reconciler) ensureService(ctx context.Context, cb *apiv1alpha1.CachedBuild, platform string) error {
	name := podName(cb, platform)
	arch := archFromPlatform(platform)
	desiredLabels := cacheLabels(cb, arch)

	var svc corev1.Service

	err := r.Get(ctx, types.NamespacedName{Namespace: cb.Namespace, Name: name}, &svc)
	if err == nil {
		if !maps.Equal(svc.Labels, desiredLabels) {
			svc.Labels = desiredLabels
			if updateErr := r.Update(ctx, &svc); updateErr != nil {
				return fmt.Errorf("sync service labels: %w", updateErr)
			}
		}

		return nil
	}

	if !apierrors.IsNotFound(err) {
		return err
	}

	svc = corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cb.Namespace,
			Labels:    desiredLabels,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				"app":                     "buildkit-cache",
				"ci.siderolabs.com/cache": cb.Name,
				"ci.siderolabs.com/arch":  arch,
			},
			Ports: []corev1.ServicePort{{
				Name:     "buildkitd",
				Port:     1234,
				Protocol: corev1.ProtocolTCP,
			}},
		},
	}

	if err := ctrl.SetControllerReference(cb, &svc, r.Scheme); err != nil {
		return err
	}

	return r.Create(ctx, &svc)
}

// ensureDeployment ensures a single-replica buildkit Deployment exists
// per (cacheKey, platform). Strategy=Recreate keeps the RWO PVC safe
// across restarts — the old pod must terminate fully before the new one
// is scheduled. Idle teardown scales this Deployment to 0; the object
// itself survives so reawaken is a simple scale-up.
// ensureDeployment creates or updates the buildkit Deployment for one
// (cacheKey, platform). Uses controllerutil.CreateOrUpdate so tier-spec
// changes (image, resources, affinity, config map, securityContext)
// propagate to existing Deployments on the next reconcile, not just on
// fresh creates.
//
// Replicas is always set to 1 here — handleIdlePhases scales to 0
// separately, and we never reach ensureDeployment during an idle window
// (the Idle short-circuit returns before ensurePlatforms runs).
func (r *Reconciler) ensureDeployment(
	ctx context.Context,
	cb *apiv1alpha1.CachedBuild,
	platform string,
	resources corev1.ResourceRequirements,
	affinity *corev1.Affinity,
	image string,
	buildkitdConfigMap string,
	privileged *bool,
	pvc string,
) (*appsv1.Deployment, error) {
	name := podName(cb, platform)
	arch := archFromPlatform(platform)

	// selectorLabels are the immutable identity of this Deployment —
	// Deployment.Spec.Selector cannot be mutated post-create. CacheGroup
	// (tier) is intentionally NOT in here: a tier change must not break
	// the selector. Tier ships as a metadata-only label via cacheLabels.
	selectorLabels := map[string]string{
		"app":                     "buildkit-cache",
		"ci.siderolabs.com/cache": cb.Name,
		"ci.siderolabs.com/arch":  arch,
	}
	labels := cacheLabels(cb, arch)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cb.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		if err := ctrl.SetControllerReference(cb, dep, r.Scheme); err != nil {
			return err
		}

		dep.Labels = labels
		dep.Spec.Replicas = new(int32(1))
		dep.Spec.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: selectorLabels}
		dep.Spec.Template = corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec:       buildkitPodSpec(arch, resources, affinity, image, buildkitdConfigMap, privileged, pvc),
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("create or update deployment: %w", err)
	}

	return dep, nil
}

// buildkitPodSpec materializes the per-platform buildkit pod template
// from tier spec inputs. Kept side-effect-free so ensureDeployment can
// rebuild + diff cheaply on every reconcile.
func buildkitPodSpec(arch string, resources corev1.ResourceRequirements, affinity *corev1.Affinity, image, buildkitdConfigMap string, privileged *bool, pvc string) corev1.PodSpec {
	spec := corev1.PodSpec{
		Affinity: combineNodeAndAntiAffinity(affinity, arch),
		// buildkitd does not talk to the Kubernetes API; mounting the
		// default SA token would only widen the blast radius if the
		// (privileged) container is compromised.
		AutomountServiceAccountToken: new(false),
		Containers: []corev1.Container{{
			Name:      "buildkitd",
			Image:     image,
			Args:      []string{"--addr=tcp://0.0.0.0:1234", "--root=/var/lib/buildkit"},
			Ports:     []corev1.ContainerPort{{ContainerPort: 1234, Name: "buildkitd"}},
			Resources: resources,
			SecurityContext: &corev1.SecurityContext{
				Privileged: privileged,
			},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					Exec: &corev1.ExecAction{
						Command: []string{"buildctl", "--addr=tcp://0.0.0.0:1234", "debug", "workers"},
					},
				},
				PeriodSeconds:    10,
				FailureThreshold: 3,
			},
			VolumeMounts: []corev1.VolumeMount{{
				Name:      "cache",
				MountPath: "/var/lib/buildkit",
			}},
		}},
		Volumes: []corev1.Volume{{
			Name: "cache",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: pvc,
				},
			},
		}},
	}

	if buildkitdConfigMap != "" {
		spec.Containers[0].VolumeMounts = append(spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{
				Name:      "buildkitd-config",
				MountPath: "/etc/buildkit",
				ReadOnly:  true,
			},
		)
		spec.Volumes = append(spec.Volumes, corev1.Volume{
			Name: "buildkitd-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: buildkitdConfigMap},
				},
			},
		})
	}

	return spec
}

// combineNodeAndAntiAffinity copies the tier's Affinity verbatim and
// AND-s a `kubernetes.io/arch=<arch>` node-affinity requirement into it.
// The buildkit pod for a given platform MUST land on a node of that arch
// — that's the one piece the operator can't override. Everything else
// (NodeAffinity preferreds, PodAffinity, PodAntiAffinity, custom role
// selectors) is left untouched.
//
// Note: NodeSelectorTerms are OR-ed; MatchExpressions inside a term are
// AND-ed. To enforce arch when the tier already specifies terms, the
// requirement is appended to every term rather than added as a new one.
func combineNodeAndAntiAffinity(tierAffinity *corev1.Affinity, arch string) *corev1.Affinity {
	archReq := corev1.NodeSelectorRequirement{
		Key: "kubernetes.io/arch", Operator: corev1.NodeSelectorOpIn, Values: []string{arch},
	}

	out := &corev1.Affinity{}
	if tierAffinity != nil {
		out = tierAffinity.DeepCopy()
	}

	if out.NodeAffinity == nil {
		out.NodeAffinity = &corev1.NodeAffinity{}
	}

	req := out.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	if req == nil || len(req.NodeSelectorTerms) == 0 {
		out.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{{
				MatchExpressions: []corev1.NodeSelectorRequirement{archReq},
			}},
		}

		return out
	}

	for i := range req.NodeSelectorTerms {
		req.NodeSelectorTerms[i].MatchExpressions = append(req.NodeSelectorTerms[i].MatchExpressions, archReq)
	}

	return out
}

// cacheSelector matches every Pod/PVC/Service this controller owns for the
// given CachedBuild — across platforms, robust to the tier being deleted
// out from under us.
func cacheSelector(cb *apiv1alpha1.CachedBuild) client.MatchingLabels {
	return client.MatchingLabels{
		"app":                     "buildkit-cache",
		"ci.siderolabs.com/cache": cb.Name,
	}
}

// scaleDownDeployments sets Replicas=0 on every Deployment owned by this
// CR. The Deployment objects survive so reawaken is a scale-up; PVCs are
// untouched.
func (r *Reconciler) scaleDownDeployments(ctx context.Context, cb *apiv1alpha1.CachedBuild) error {
	var deps appsv1.DeploymentList
	if err := r.List(ctx, &deps, client.InNamespace(cb.Namespace), cacheSelector(cb)); err != nil {
		return err
	}

	for i := range deps.Items {
		dep := &deps.Items[i]
		if dep.Spec.Replicas != nil && *dep.Spec.Replicas == 0 {
			continue
		}

		dep.Spec.Replicas = new(int32(0))
		if err := r.Update(ctx, dep); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func (r *Reconciler) deletePVCs(ctx context.Context, cb *apiv1alpha1.CachedBuild) error {
	var pvcs corev1.PersistentVolumeClaimList
	if err := r.List(ctx, &pvcs, client.InNamespace(cb.Namespace), cacheSelector(cb)); err != nil {
		return err
	}

	for i := range pvcs.Items {
		if err := r.Delete(ctx, &pvcs.Items[i]); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func (r *Reconciler) shouldGarbageCollect(cb *apiv1alpha1.CachedBuild, tier *apiv1alpha1.CachedBuildTier) bool {
	if tier.Spec.CacheRetention == nil {
		return false
	}

	ts, ok := lastActivity(cb)
	if !ok {
		return false
	}

	return time.Since(ts) > tier.Spec.CacheRetention.Duration
}

func (r *Reconciler) shouldTearDown(cb *apiv1alpha1.CachedBuild, tier *apiv1alpha1.CachedBuildTier) bool {
	if tier.Spec.IdleTTL.Duration == 0 {
		return false
	}

	if cb.Status.Phase != apiv1alpha1.PhaseReady {
		return false
	}

	ts, ok := lastActivity(cb)
	if !ok {
		return false
	}

	return time.Since(ts) > tier.Spec.IdleTTL.Duration
}

// isFreshActivity reports whether lastActivity has been refreshed within
// the tier's idleTtl. Used to decide whether an Idle CR should reawaken.
func isFreshActivity(cb *apiv1alpha1.CachedBuild, tier *apiv1alpha1.CachedBuildTier) bool {
	if tier.Spec.IdleTTL.Duration == 0 {
		return true
	}

	ts, ok := lastActivity(cb)
	if !ok {
		return false
	}

	return time.Since(ts) <= tier.Spec.IdleTTL.Duration
}

func lastActivity(cb *apiv1alpha1.CachedBuild) (time.Time, bool) {
	val, ok := cb.Annotations[apiv1alpha1.LastActivityAnnotation]
	if !ok {
		return time.Time{}, false
	}

	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return time.Time{}, false
	}

	return t, true
}

func isDeploymentReady(dep *appsv1.Deployment) bool {
	if dep == nil {
		return false
	}

	return dep.Status.ReadyReplicas > 0
}
