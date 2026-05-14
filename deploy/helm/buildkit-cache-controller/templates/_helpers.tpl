{{/*
Expand the name of the chart.
*/}}
{{- define "buildkit-cache-controller.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "buildkit-cache-controller.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "buildkit-cache-controller.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "buildkit-cache-controller.labels" -}}
helm.sh/chart: {{ include "buildkit-cache-controller.chart" . }}
{{ include "buildkit-cache-controller.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "buildkit-cache-controller.selectorLabels" -}}
app.kubernetes.io/name: {{ include "buildkit-cache-controller.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "buildkit-cache-controller.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "buildkit-cache-controller.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Validate that allowedNamespaces is set and non-empty. Called from every
template that depends on the value so a missing setting fails install
with a friendly message instead of a downstream rendering error.
*/}}
{{- define "buildkit-cache-controller.validateAllowedNamespaces" -}}
{{- if not .Values.allowedNamespaces -}}
{{- fail "buildkit-cache-controller: .Values.allowedNamespaces is required and must be a non-empty list (e.g. allowedNamespaces: [ci])." -}}
{{- end -}}
{{- end -}}

{{/*
Leader-election Lease name. Single source of truth: the deployment passes
this as --leader-elect-id, and the leader-election Role scopes its
get/update/patch rule to this resourceName.
*/}}
{{- define "buildkit-cache-controller.leaderElectionID" -}}
{{- default "buildkit-cache-controller.siderolabs.com" .Values.leaderElection.id -}}
{{- end -}}
