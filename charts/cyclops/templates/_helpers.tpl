{{- define "cyclops.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "cyclops.fullname" -}}
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

{{- define "cyclops.labels" -}}
app.kubernetes.io/name: {{ include "cyclops.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "cyclops.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cyclops.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
control-plane: controller-manager
{{- end }}

{{/* ServiceAccount names. The report name must match the controller's --report-service-account. */}}
{{- define "cyclops.controllerServiceAccount" -}}{{ include "cyclops.fullname" . }}-controller-manager{{- end }}
{{- define "cyclops.reportServiceAccount" -}}{{ include "cyclops.fullname" . }}-report{{- end }}
{{- define "cyclops.mailConfigMap" -}}{{ include "cyclops.fullname" . }}-mail-config{{- end }}

{{/* The one credentials Secret in use: the chosen provider's. Empty means none. */}}
{{- define "cyclops.credentialsSecret" -}}
{{- if eq .Values.email.provider "SES" -}}
{{- .Values.email.ses.credentialsSecretName -}}
{{- else -}}
{{- .Values.email.smtp.credentialsSecretName -}}
{{- end -}}
{{- end }}

{{- define "cyclops.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end }}

{{- define "cyclops.emailTemplateConfigMap" -}}
{{- if and .Values.emailTemplate.useAsDefault (not .Values.emailTemplate.create) }}{{ fail "emailTemplate.useAsDefault requires emailTemplate.create: true" }}{{ end -}}{{ default (printf "%s-email-template" (include "cyclops.fullname" .)) .Values.emailTemplate.name }}{{- end }}
