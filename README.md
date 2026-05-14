# buildkit-cache-controller

Kubernetes controller that provisions per-cache-key buildkit pods backed by named PVCs, for cache-aware CI builds.
Consumed by GitHub Actions runners via the `CachedBuild` CRD.

## Status

Pre-alpha.
CRD shape and behavior subject to change.

---

> [!IMPORTANT]
> **Self-hosted runners only.**
> This controller has no use on GitHub-hosted runners.
> It requires:
>
> - A self-hosted runner image with the `cb-hook` binary baked in (see [Runner image integration](#runner-image-integration)).
> - In-cluster API access from the runner pod (ServiceAccount with `cachedbuilds` get/create/patch in the runner's namespace).
> - `setup-buildx-action` using the remote driver pointed at endpoints exported by the hook.

## Concept

A `CachedBuild` custom resource represents one logical buildkit cache identity.
The controller materializes it as one PVC + one Pod per platform listed in the referenced `CachedBuildTier`, scheduled with the tier's affinity rules.
Pods are torn down after `idleTTL` of inactivity; PVCs persist so subsequent builds reawaken on the same node with a warm cache.

```text
                       ┌─────────────────────────┐
                       │ runner pod (job-started)│
                       │   cb-hook job-started   │──── kubectl-equivalent
                       └─────────────────────────┘     creates/patches CR
                                    │
                                    ▼
                         apiVersion: ci.siderolabs.com/v1alpha1
                         kind: CachedBuild              ◄─── controller
                         spec: {cacheKey, tier}              reconciles
                                    │
                                    ▼
                         ┌──────────┴──────────┐
                         │                     │
                    ┌────▼────┐           ┌────▼────┐
                    │   Pod   │           │   PVC   │
                    │ buildkitd│          │ (RWO,   │
                    └─────────┘           │  WFFC)  │
                         ▲                └─────────┘
                         │ tcp endpoint
                  ┌──────┴───────┐
                  │ setup-buildx │
                  │ driver:remote│
                  └──────────────┘
```

## CRDs

### `CachedBuildTier` (namespaced, shortName `cbt`)

Declarative profile shared by every `CachedBuild` that names it.
Lives in the same namespace as the `CachedBuild`s that reference it.

```yaml
apiVersion: ci.siderolabs.com/v1alpha1
kind: CachedBuildTier
metadata:
  name: generic
  namespace: ci
spec:
  storageClassName: ci-buildkit-cache   # WaitForFirstConsumer recommended
  storage: 50Gi
  platforms: [linux/amd64]
  idleTTL: 30m                          # tear down pods after this idle window
  image: moby/buildkit:v0.30.0          # required: pin a tag explicitly
  buildkitdConfigMap: buildkitd-config-generic   # optional: mounts /etc/buildkit
  cacheRetention: 24h                   # optional: whole-cache GC opt-in
  resources:                            # optional: defaults to buildkit defaults
    requests: {memory: 1Gi}
    limits:   {memory: 4Gi}
  affinity:                             # optional: raw corev1.Affinity
    podAntiAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
        - weight: 100
          podAffinityTerm:
            topologyKey: kubernetes.io/hostname
            labelSelector:
              matchLabels:
                ci.siderolabs.com/cache-group: generic
```

### `CachedBuild` (namespaced, shortName `cb`)

Identity record.
Created by the runner hook on every `job-started` event; multiple parallel jobs converge on the same CR (idempotent).

```yaml
apiVersion: ci.siderolabs.com/v1alpha1
kind: CachedBuild
metadata:
  name: my-repo-myorg                   # PVC/Deployment/Service naming root
  namespace: ci
  annotations:
    ci.siderolabs.com/last-activity: "2026-05-14T12:00:00Z"
spec:
  tier: generic                         # references CachedBuildTier
```

The controller writes status (phase, endpoints, pvcs, deployments, cacheSize, conditions).
Endpoints are TCP buildkit URIs in short form — `tcp://bk-<name>-<arch>.<ns>.svc:1234` — so the cluster's DNS search path resolves to whatever cluster domain is configured (default `cluster.local`).

## Runner image integration

The `cb-hook` binary plus shim scripts ship together in `ghcr.io/siderolabs/cb-hook`.
Bake them into your self-hosted runner image:

```dockerfile
# Pin a tagged release in production. Declare as a stage so subsequent
# COPY --from=cb-hook resolves; COPY --from=<image-ref> only works with
# BuildKit and is brittle across builders.
ARG CB_HOOK_IMAGE=ghcr.io/siderolabs/cb-hook:latest
FROM ${CB_HOOK_IMAGE} AS cb-hook

FROM ubuntu:24.04
# ... runner installation ...

# Binary at /opt/runner-hooks/cb-hook; shims expect this exact path.
COPY --link --from=cb-hook /cb-hook       /opt/runner-hooks/cb-hook
COPY --link --from=cb-hook /runner-hooks/ /opt/runner-hooks/

ENV ACTIONS_RUNNER_HOOK_JOB_STARTED=/opt/runner-hooks/job-started.sh \
    ACTIONS_RUNNER_HOOK_JOB_COMPLETED=/opt/runner-hooks/job-completed.sh
```

The runner rejects hook paths that don't end in `.sh`/`.ps1`/`.js`; the shipped `*.sh` shims exist solely to `exec` the Go binary.

### Runner environment

The runner pod must set these env vars (typically via the runner scale-set spec):

| Variable          | Required | Purpose                                                       |
| ----------------- | -------- | ------------------------------------------------------------- |
| `CB_ENABLED`      | yes      | `"true"` enables hook; anything else makes it a silent no-op. |
| `CB_NAMESPACE`    | yes      | Namespace where the `CachedBuild` CR is created.              |
| `CB_TIER`         | yes      | `CachedBuildTier` name in that namespace.                     |
| `CACHE_KEY`       | no       | Override; defaults to sanitized `GITHUB_REPOSITORY`.          |
| `CB_WAIT_TIMEOUT` | no       | How long `job-started` waits for `Ready` (default `5m`).      |

The hook also reads `GITHUB_ENV` (always set by the runner) to export the buildkit endpoints back to subsequent steps:

- `BUILDKIT_ENDPOINT_AMD64=tcp://...`
- `BUILDKIT_ENDPOINT_ARM64=tcp://...`
- `BUILDKIT_ENDPOINT=<first endpoint>` (convenience default)

### RBAC for the runner ServiceAccount

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: cb-runner, namespace: ci}
rules:
  - apiGroups: [ci.siderolabs.com]
    resources: [cachedbuilds]
    verbs: [get, create, patch]
```

`patch` covers the `lastActivity` annotation refresh.
No `list`/`delete` needed.

## Workflow integration

Use `docker/setup-buildx-action` with the remote driver:

```yaml
- uses: docker/setup-buildx-action@v3
  with:
    driver: remote
    endpoint: ${{ env.BUILDKIT_ENDPOINT }}      # default endpoint
    # or per-arch: ${{ env.BUILDKIT_ENDPOINT_AMD64 }}
```

For multi-arch in one buildx instance:

```yaml
- uses: docker/setup-buildx-action@v3
  with:
    driver: remote
    endpoint: ${{ env.BUILDKIT_ENDPOINT_AMD64 }}
    append: |
      - endpoint: ${{ env.BUILDKIT_ENDPOINT_ARM64 }}
        platforms: linux/arm64
```

## Install (controller side)

Helm chart ships under [`deploy/helm/buildkit-cache-controller/`](deploy/helm/buildkit-cache-controller).

```sh
helm install bk-cache deploy/helm/buildkit-cache-controller \
  --namespace bk-cache --create-namespace \
  --set 'allowedNamespaces={ci,buildkit-cache-test}'
```

`allowedNamespaces` is **required**: install aborts if empty, and the controller refuses to start without `--allowed-namespaces`.
Every watch (CRs included) and every mutation is scoped to these namespaces via per-ns `Role`s — the controller has **zero** cluster-wide RBAC.
CRs created in any other namespace are invisible to the informer cache and won't be reconciled.

See the [chart README](deploy/helm/buildkit-cache-controller/README.md) for full values + RBAC shape.

### What the chart doesn't install

- `CachedBuildTier` instances — operator owns; one per runner-scale-set / workload profile.
- `buildkitd` ConfigMaps referenced from tiers.
- `StorageClass` (typically a `WaitForFirstConsumer` local-path class).
- `ServiceMonitor` / `PodMonitor` — only `VMServiceScrape` is shipped; toggle off and bring your own if you run prom-operator.

The reference profiles under [`deploy/manifests/tiers.yaml`](deploy/manifests/tiers.yaml) + [`deploy/manifests/buildkitd-configs.yaml`](deploy/manifests/buildkitd-configs.yaml) are good starting points — copy + edit to taste.

## Metrics

Exposed at `:8080/metrics` (controller-runtime registry).
Prefix `cb_`.
Headline:

- `cb_provisioned_total{tier,warm}` — cold/warm rate; hit-rate KPI
- `cb_provisioning_duration_seconds{tier,warm}` — cold provisioning latency histogram
- `cb_idle_teardown_total{tier}` — Deployments scaled to zero for inactivity
- `cb_active_deployments{tier,arch,ready}` — live Deployment count
- `cb_active_pvcs{tier}`, `cb_storage_request_bytes{tier}` — PVC inventory

## Development

```bash
make generate   # CRDs + deepcopy + role.yaml from kubebuilder markers
make rekres     # regenerate Dockerfile/Makefile/etc. from .kres.yaml
go build ./... && go vet ./... && go test ./...
```
