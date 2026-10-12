{{- define "chat.labels" -}}
app.kubernetes.io/part-of: chat
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end }}

{{- define "chat.selector" -}}
app.kubernetes.io/name: {{ . }}
app.kubernetes.io/part-of: chat
{{- end }}

{{- define "chat.image" -}}
{{- if .tag -}}
{{ .repository }}:{{ .tag }}
{{- else -}}
{{ .repository }}
{{- end -}}
{{- end }}

{{- define "chat.podSecurityContext" -}}
runAsNonRoot: true
seccompProfile:
  type: RuntimeDefault
{{- end }}

{{- define "chat.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop:
    - ALL
{{- end }}
