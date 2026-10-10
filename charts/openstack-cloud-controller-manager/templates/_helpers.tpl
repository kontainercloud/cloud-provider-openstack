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
The lines `key = "value"` of a map, in key order. A key with an empty value is
left out: the controller treats it like a key that is not set.
*/}}
{{- define "occm.keyValues" -}}
{{- range $key, $value := . }}
{{- if not (kindIs "invalid" $value) }}
{{- if ne (toString $value) "" }}
{{ $key }} = {{ $value | quote }}
{{- end }}
{{- end }}
{{- end }}
{{- end -}}

{{/*
Create cloud-config makro. The load balancer keys come from the loadBalancer
values, cloudConfig.loadBalancer adds to them.
*/}}
{{- define "cloudConfig" -}}
{{- $lb := .Values.loadBalancer -}}
{{- $managed := dict "internal-lb" (eq $lb.mode "internal") "rpc-server-addr" $lb.rpcServerAddr "api-key" $lb.apiKey "tenant-id" $lb.tenantID "network-id" $lb.networkID "subnet-id" $lb.subnetID -}}
{{- if ne $lb.mode "internal" -}}
{{- $_ := set $managed "floating-network-id" $lb.floatingNetworkID -}}
{{- end -}}
[Global]{{ include "occm.keyValues" .Values.cloudConfig.global }}

[Networking]{{ include "occm.keyValues" .Values.cloudConfig.networking }}

[LoadBalancer]{{ include "occm.keyValues" $managed }}{{ include "occm.keyValues" .Values.cloudConfig.loadBalancer }}

[BlockStorage]{{ include "occm.keyValues" .Values.cloudConfig.blockStorage }}

[Metadata]{{ include "occm.keyValues" .Values.cloudConfig.metadata }}

[Route]{{ include "occm.keyValues" .Values.cloudConfig.route }}
{{- end }}

{{/*
Generate string of enabled controllers. Might have a trailing comma (,) which needs to be trimmed.
*/}}
{{- define "occm.enabledControllers" }}
{{- range .Values.enabledControllers -}}{{ . }},{{- end -}}
{{- end }}

{{/*
Path of the kubeconfig of the workload cluster in the pod.
*/}}
{{- define "occm.kubeconfigPath" -}}
{{- printf "%s/kubeconfig" (trimSuffix "/" .Values.kubeconfig.mountPath) -}}
{{- end }}
