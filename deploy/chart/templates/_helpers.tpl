{{/* Shared labels for every object of the release. */}}
{{- define "app.labels" -}}
app.kubernetes.io/name: ephemeral-app
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Values.image.tag | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/* Pod security settings shared by every pod: non-root, default seccomp, no API token. */}}
{{- define "app.podSecurity" -}}
automountServiceAccountToken: false
securityContext:
  runAsNonRoot: true
  runAsUser: 65532
  runAsGroup: 65532
  seccompProfile:
    type: RuntimeDefault
{{- end -}}

{{/* Container security settings shared by every container. */}}
{{- define "app.containerSecurity" -}}
securityContext:
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  capabilities:
    drop: ["ALL"]
{{- end -}}

{{/* Seeder pod spec running one action ("load" or "verify") against the API service. */}}
{{- define "app.seederPod" -}}
{{- $ := index . 0 -}}
{{- $action := index . 1 -}}
{{ include "app.podSecurity" $ }}
restartPolicy: Never
containers:
  - name: seeder
    image: "{{ $.Values.image.seeder }}:{{ $.Values.image.tag }}"
    imagePullPolicy: {{ $.Values.image.pullPolicy }}
    args: ["{{ $action }}", "-url", "http://{{ $.Release.Name }}-api:8080", "-seed", "/seed/seed.json", "-timeout", "{{ $.Values.seedDeadlineSeconds }}s"]
    {{- include "app.containerSecurity" $ | nindent 4 }}
    resources:
      {{- toYaml $.Values.resources.seeder | nindent 6 }}
    volumeMounts:
      - name: seed
        mountPath: /seed
        readOnly: true
volumes:
  - name: seed
    configMap:
      name: {{ $.Release.Name }}-seed
{{- end -}}
