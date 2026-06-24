{{/*
Standard chart-name and label helpers. Kept minimal — this chart owns
just a Secret, so the usual Deployment/Service helpers from `helm create`
are pruned.
*/}}

{{/*
Expand the name of the chart.
*/}}
{{- define "crond-agent.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully-qualified release name. Used for the Secret's metadata.name so a
user can `helm install backups …` and the secret becomes `backups-pingkeys`.
*/}}
{{- define "crond-agent.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Chart-version label used in Kubernetes recommended labels.
*/}}
{{- define "crond-agent.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Recommended labels — applied to every resource the chart owns.
*/}}
{{- define "crond-agent.labels" -}}
helm.sh/chart: {{ include "crond-agent.chart" . }}
{{ include "crond-agent.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Selector labels — stable across upgrades; safe to use in label selectors.
*/}}
{{- define "crond-agent.selectorLabels" -}}
app.kubernetes.io/name: {{ include "crond-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Name of the Secret holding ping_keys. Exposed as a helper so the
wrapper macros (and example CronJobs) reference one source of truth.
*/}}
{{- define "crond-agent.secretName" -}}
{{ include "crond-agent.fullname" . }}-pingkeys
{{- end -}}
