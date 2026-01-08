{{/*
Expand the name of the chart.
*/}}
{{- define "delos.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "delos.fullname" -}}
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
{{- define "delos.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "delos.labels" -}}
helm.sh/chart: {{ include "delos.chart" . }}
{{ include "delos.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "delos.selectorLabels" -}}
app.kubernetes.io/name: {{ include "delos.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service labels
*/}}
{{- define "delos.serviceLabels" -}}
{{ include "delos.labels" . }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{/*
Service selector labels
*/}}
{{- define "delos.serviceSelectorLabels" -}}
{{ include "delos.selectorLabels" . }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "delos.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "delos.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create image name
*/}}
{{- define "delos.image" -}}
{{- $registry := .Values.global.imageRegistry | default "" }}
{{- $repository := .Values.common.image.repository }}
{{- $tag := .Values.common.image.tag | default .Chart.AppVersion }}
{{- if $registry }}
{{- printf "%s/%s:%s" $registry $repository $tag }}
{{- else }}
{{- printf "%s:%s" $repository $tag }}
{{- end }}
{{- end }}

{{/*
Database URL
*/}}
{{- define "delos.databaseUrl" -}}
{{- printf "host=%s port=%d user=%s dbname=%s sslmode=disable" .Values.database.host (.Values.database.port | int) .Values.database.user .Values.database.name }}
{{- end }}

{{/*
Redis URL
*/}}
{{- define "delos.redisUrl" -}}
{{- printf "redis://%s:%d" .Values.redis.host (.Values.redis.port | int) }}
{{- end }}
