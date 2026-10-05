{{/*
Expand the name of the chart.
*/}}
{{- define "occm.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Name of the chart's objects: the release name, unless overridden. One release
manages one cluster, so several releases can share a namespace.
*/}}
{{- define "occm.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else if .Values.nameOverride -}}
{{- .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{/*
Name of the managed cluster.
*/}}
{{- define "occm.clusterName" -}}
{{- default .Release.Name .Values.cluster.name -}}
{{- end -}}

{{- define "occm.serviceAccountName" -}}
{{- default (include "occm.fullname" .) .Values.serviceAccountName -}}
{{- end -}}

{{- define "occm.secretName" -}}
{{- default (printf "%s-cloud-config" (include "occm.clusterName" .)) .Values.secret.name -}}
{{- end -}}

{{- define "occm.kubeconfigSecretName" -}}
{{- default (printf "%s-kubeconfig" (include "occm.clusterName" .)) .Values.kubeconfig.secretName -}}
{{- end -}}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "occm.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Standard Kubernetes recommended labels.
*/}}
{{- define "occm.labels.standard" -}}
helm.sh/chart: {{ include "occm.chart" . }}
app.kubernetes.io/name: {{ include "occm.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "occm.labels.matchLabels" -}}
app.kubernetes.io/name: {{ include "occm.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "occm.common.matchLabels" -}}
app: {{ template "occm.name" . }}
release: {{ .Release.Name }}
{{- end -}}

{{- define "occm.common.metaLabels" -}}
chart: {{ template "occm.chart" . }}
heritage: {{ .Release.Service }}
{{- end -}}

{{- define "occm.controllermanager.matchLabels" -}}
component: controllermanager
{{ include "occm.common.matchLabels" . }}
{{- end -}}

{{- define "occm.controllermanager.labels" -}}
{{ include "occm.controllermanager.matchLabels" . }}
{{ include "occm.common.metaLabels" . }}
{{ if .Values.podLabels }}
{{- toYaml .Values.podLabels }}
{{- end }}
{{- end -}}

{{/*
Common annotations and pod annotations
*/}}
{{- define "occm.controllermanager.annotations" -}}
{{- if .Values.commonAnnotations }}
{{- toYaml .Values.commonAnnotations }}
{{- end }}
{{ if .Values.podAnnotations }}
{{- toYaml .Values.podAnnotations }}
{{- end }}
{{- end -}}


{{/*
Create cloud-config makro.
*/}}
{{- define "cloudConfig" -}}
[Global]
{{- range $key, $value := .Values.cloudConfig.global }}
{{ $key }} = {{ $value | quote }}
{{- end }}

[Networking]
{{- range $key, $value := .Values.cloudConfig.networking }}
{{ $key }} = {{ $value | quote }}
{{- end }}

[LoadBalancer]
{{- if eq .Values.loadBalancer.mode "internal" }}
internal-lb = "true"
{{- else }}
internal-lb = "false"
floating-network-id = {{ .Values.loadBalancer.floatingNetworkID | quote }}
{{- end }}
{{- range $key, $value := .Values.cloudConfig.loadBalancer }}
{{- if not (has $key (list "internal-lb" "floating-network-id")) }}
{{ $key }} = {{ $value | quote }}
{{- end }}
{{- end }}

[BlockStorage]
{{- range $key, $value := .Values.cloudConfig.blockStorage }}
{{ $key }} = {{ $value | quote }}
{{- end }}

[Metadata]
{{- range $key, $value := .Values.cloudConfig.metadata }}
{{ $key }} = {{ $value | quote }}
{{- end }}

[Route]
{{- range $key, $value := .Values.cloudConfig.route }}
{{ $key }} = {{ $value | quote }}
{{- end }}
{{- end }}

{{/*
Generate string of enabled controllers. Might have a trailing comma (,) which needs to be trimmed.
*/}}
{{- define "occm.enabledControllers" }}
{{- range .Values.enabledControllers -}}{{ . }},{{- end -}}
{{- end }}

{{/*
Path of the kubeconfig of the cluster the controller manages, when it does not run inside that cluster.
*/}}
{{- define "occm.kubeconfigPath" -}}
{{- printf "%s/kubeconfig" (trimSuffix "/" .Values.kubeconfig.mountPath) -}}
{{- end }}

{{/*
Check the load balancer mode. Only checked when the chart writes cloud.conf: a
Secret brought along with secret.create=false carries its own settings.
*/}}
{{- define "occm.validateLoadBalancer" -}}
{{- if and .Values.secret.create (not .Values.cloudConfigContents) -}}
{{- if not (has .Values.loadBalancer.mode (list "external" "internal")) -}}
{{- fail (printf "loadBalancer.mode must be external or internal, got %q" .Values.loadBalancer.mode) -}}
{{- end -}}
{{- if and (eq .Values.loadBalancer.mode "external") (not .Values.loadBalancer.floatingNetworkID) -}}
{{- fail "loadBalancer.floatingNetworkID is required when loadBalancer.mode is external: floating IPs are allocated from that network" -}}
{{- end -}}
{{- range $key := list "internal-lb" "floating-network-id" -}}
{{- if hasKey ($.Values.cloudConfig.loadBalancer | default dict) $key -}}
{{- fail (printf "cloudConfig.loadBalancer.%s is set from loadBalancer.mode and loadBalancer.floatingNetworkID, remove it from cloudConfig" $key) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
