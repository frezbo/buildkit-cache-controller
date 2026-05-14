# buildkit-cache-controller

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat) ![AppVersion: 0.1.0](https://img.shields.io/badge/AppVersion-0.1.0-informational?style=flat)

Kubernetes controller that provisions per-cache-key buildkit pods backed by
named PVCs, for cache-aware CI builds. Consumed by GitHub Actions
self-hosted runners via the CachedBuild CRD.

**Homepage:** <https://github.com/siderolabs/buildkit-cache-controller>

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| Sidero Labs |  | <https://www.siderolabs.com> |

## Source Code

* <https://github.com/siderolabs/buildkit-cache-controller>

## Requirements

Kubernetes: `>=1.27.0`

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` |  |
| allowedNamespaces | list | `[]` | Required, non-empty list of namespaces the controller may materialize buildkit Deployments / Services / PVCs in; the chart installs a Role + RoleBinding in each. Every watch is scoped to this list (CRs included) so the controller has zero cluster-wide RBAC and CRs created elsewhere are ignored. Empty value aborts install and the controller exits when the allowed-namespaces flag is missing. |
| fullnameOverride | string | `""` |  |
| healthProbe.port | int | `8081` |  |
| image.pullPolicy | string | `"Always"` |  |
| image.repository | string | `"ghcr.io/siderolabs/buildkit-cache-controller"` |  |
| image.tag | string | `""` |  |
| imagePullSecrets | list | `[]` |  |
| leaderElection.enabled | bool | `true` |  |
| leaderElection.id | string | `"buildkit-cache-controller.siderolabs.com"` |  |
| metrics.port | int | `8080` |  |
| metrics.service.enabled | bool | `true` |  |
| metrics.vmServiceScrape.enabled | bool | `true` |  |
| metrics.vmServiceScrape.interval | string | `"30s"` |  |
| metrics.vmServiceScrape.keepMetrics | string | `"cb_.*|controller_runtime_reconcile_(total|errors_total|time_seconds.*)"` |  |
| nameOverride | string | `""` |  |
| nodeSelector | object | `{}` |  |
| podAnnotations | object | `{}` |  |
| podLabels | object | `{}` |  |
| podSecurityContext.fsGroup | int | `65532` |  |
| podSecurityContext.runAsGroup | int | `65532` |  |
| podSecurityContext.runAsNonRoot | bool | `true` |  |
| podSecurityContext.runAsUser | int | `65532` |  |
| podSecurityContext.seccompProfile.type | string | `"RuntimeDefault"` |  |
| replicaCount | int | `1` |  |
| resources.limits.cpu | string | `"500m"` |  |
| resources.limits.memory | string | `"256Mi"` |  |
| resources.requests.cpu | string | `"50m"` |  |
| resources.requests.memory | string | `"64Mi"` |  |
| runnerServiceAccount | object | `{"enabled":false,"name":"cb-runner"}` | Optional ServiceAccount + Role + RoleBinding for the actions/runner pod that runs cb-hook. Grants the runner SA the minimal CR perms (cachedbuilds get/create/patch) needed by job-started.sh / job-completed.sh. Created once per entry in allowedNamespaces so every namespace the controller watches has a matching runner SA. |
| securityContext.allowPrivilegeEscalation | bool | `false` |  |
| securityContext.capabilities.drop[0] | string | `"ALL"` |  |
| securityContext.readOnlyRootFilesystem | bool | `true` |  |
| securityContext.runAsGroup | int | `65532` |  |
| securityContext.runAsNonRoot | bool | `true` |  |
| securityContext.runAsUser | int | `65532` |  |
| serviceAccount.annotations | object | `{}` |  |
| serviceAccount.automount | bool | `true` |  |
| serviceAccount.create | bool | `true` |  |
| serviceAccount.name | string | `""` |  |
| tolerations | list | `[]` |  |

