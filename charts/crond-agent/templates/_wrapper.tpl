{{/*
═══════════════════════════════════════════════════════════════════════════
  crond-agent wrapper macros — invoke from your CronJob spec to inject
  the agent's init-container, shared volume, and command rewrite.
═══════════════════════════════════════════════════════════════════════════

  USAGE — one-shot convenience macro (recommended for most users):

    apiVersion: batch/v1
    kind: CronJob
    metadata:
      name: nightly-backup
    spec:
      schedule: "0 2 * * *"
      jobTemplate:
        spec:
          template:
            spec:
              restartPolicy: OnFailure
              {{- include "crond-agent.wrap" (dict
                  "context" $
                  "envKey" "PING_KEY_BACKUP"
                  "image" "myco/backup:1.0"
                  "command" (list "/opt/backup.sh" "--full")
                ) | nindent 14 }}

  …expands to volumes + initContainers + a container called `job` running
  `crond-agent exec --key=$(PING_KEY_BACKUP) --api-url=<apiUrl> -- /opt/backup.sh --full`.

  ─────────────────────────────────────────────────────────────────────────

  USAGE — composable macros (for users who need to merge with their own
  spec, add more containers, mount extra volumes, etc.):

    spec:
      volumes:
        {{- include "crond-agent.volume" $ | nindent 8 }}
        - name: my-config
          configMap: { name: backup-config }
      initContainers:
        {{- include "crond-agent.initContainer" $ | nindent 8 }}
      containers:
        - name: job
          image: myco/backup:1.0
          command:
            {{- include "crond-agent.wrappedCommand"
                (dict "context" $ "envKey" "PING_KEY_BACKUP"
                      "command" (list "/opt/backup.sh"))
              | nindent 12 }}
          env:
            {{- include "crond-agent.envFromSecret"
                (dict "context" $ "envKey" "PING_KEY_BACKUP")
              | nindent 12 }}
          volumeMounts:
            {{- include "crond-agent.volumeMount" $ | nindent 12 }}
            - name: my-config
              mountPath: /etc/backup

  ─────────────────────────────────────────────────────────────────────────

  Every macro that needs chart values takes `$` (the root context) under the
  `context` key. Helm named templates lose access to `.Values` when called
  with a dict, so passing context explicitly is unavoidable.
*/}}


{{/*
─────────────────────────────────────────────────────────────────────────
crond-agent.wrap — full pod spec (volumes + initContainers + containers).
Args (dict):
  context : $                    -- chart root context (required)
  envKey  : "PING_KEY_BACKUP"    -- env var name; must exist in .Values.pingKeys
  image   : "myco/backup:1.0"    -- the user's job image
  command : list                 -- command + args the original job runs
─────────────────────────────────────────────────────────────────────────
*/}}
{{- define "crond-agent.wrap" -}}
{{- $ctx := .context -}}
securityContext:
  {{- toYaml $ctx.Values.podSecurityContext | nindent 2 }}
volumes:
  {{- include "crond-agent.volume" $ctx | nindent 2 }}
initContainers:
  {{- include "crond-agent.initContainer" $ctx | nindent 2 }}
containers:
  - name: job
    image: {{ .image | quote }}
    command:
      {{- include "crond-agent.wrappedCommand"
          (dict "context" $ctx "envKey" .envKey "command" .command)
        | nindent 6 }}
    env:
      {{- include "crond-agent.envFromSecret"
          (dict "context" $ctx "envKey" .envKey)
        | nindent 6 }}
      {{- include "crond-agent.privacyEnv" $ctx | nindent 6 }}
    volumeMounts:
      {{- include "crond-agent.volumeMount" $ctx | nindent 6 }}
    securityContext:
      {{- toYaml $ctx.Values.containerSecurityContext | nindent 6 }}
{{- end -}}


{{/*
─────────────────────────────────────────────────────────────────────────
crond-agent.volume — emptyDir volume the init container and job share.
Args: $ (root context)
─────────────────────────────────────────────────────────────────────────
*/}}
{{- define "crond-agent.volume" -}}
- name: {{ .Values.sharedVolumeName }}
  emptyDir: {}
{{- end -}}


{{/*
─────────────────────────────────────────────────────────────────────────
crond-agent.volumeMount — mount the shared volume into a container.
Args: $ (root context)
─────────────────────────────────────────────────────────────────────────
*/}}
{{- define "crond-agent.volumeMount" -}}
- name: {{ .Values.sharedVolumeName }}
  mountPath: {{ .Values.sharedMountPath }}
{{- end -}}


{{/*
─────────────────────────────────────────────────────────────────────────
crond-agent.initContainer — copies /crond-agent into the shared volume.
The agent image is FROM scratch (no shell, no cp); the agent self-copies
via its own `install` subcommand (added in v0.1.1).
Args: $ (root context)
─────────────────────────────────────────────────────────────────────────
*/}}
{{- define "crond-agent.initContainer" -}}
- name: crond-agent-installer
  image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"
  imagePullPolicy: {{ .Values.image.pullPolicy }}
  command: ["/crond-agent"]
  args:
    - "install"
    - "--target={{ .Values.sharedMountPath }}/crond-agent"
  volumeMounts:
    {{- include "crond-agent.volumeMount" . | nindent 4 }}
  securityContext:
    {{- toYaml .Values.containerSecurityContext | nindent 4 }}
  resources:
    {{- toYaml .Values.initResources | nindent 4 }}
{{- end -}}


{{/*
─────────────────────────────────────────────────────────────────────────
crond-agent.wrappedCommand — rewritten command list that runs the
original command through `crond-agent exec`.
Args (dict):
  context : $                    -- chart root context (required)
  envKey  : "PING_KEY_BACKUP"    -- env var name the agent reads --key from
  command : list                 -- the original command + args
─────────────────────────────────────────────────────────────────────────
*/}}
{{- define "crond-agent.wrappedCommand" -}}
{{- $ctx := .context -}}
- {{ printf "%s/crond-agent" $ctx.Values.sharedMountPath | quote }}
- "exec"
- {{ printf "--key=$(%s)" .envKey | quote }}
- {{ printf "--api-url=%s" $ctx.Values.agent.apiUrl | quote }}
{{- range $ctx.Values.agent.extraArgs }}
- {{ . | quote }}
{{- end }}
- "--"
{{- range .command }}
- {{ . | quote }}
{{- end }}
{{- end -}}


{{/*
─────────────────────────────────────────────────────────────────────────
crond-agent.privacyEnv — emits CROND_CAPTURE_OUTPUT / CROND_REDACT_PATTERNS
env vars when the corresponding values are non-default. Omits CAPTURE_OUTPUT
when true (default) and REDACT_PATTERNS when empty, so the rendered
manifest stays clean for the 95% of users who don't change these.
Args: $ (root context)
─────────────────────────────────────────────────────────────────────────
*/}}
{{- define "crond-agent.privacyEnv" -}}
{{- if not .Values.agent.captureOutput }}
- name: CROND_CAPTURE_OUTPUT
  value: "false"
{{- end }}
{{- with .Values.agent.redactPatterns }}
- name: CROND_REDACT_PATTERNS
  # Newline separator — commas appear inside regex quantifiers ({1,40}).
  value: {{ join "\n" . | quote }}
{{- end }}
{{- end -}}


{{/*
─────────────────────────────────────────────────────────────────────────
crond-agent.envFromSecret — env var bound to a ping_key in the Secret.
Args (dict):
  context : $                    -- chart root context (required)
  envKey  : "PING_KEY_BACKUP"    -- env var name AND Secret key
─────────────────────────────────────────────────────────────────────────
*/}}
{{- define "crond-agent.envFromSecret" -}}
- name: {{ .envKey }}
  valueFrom:
    secretKeyRef:
      name: {{ include "crond-agent.secretName" .context }}
      key: {{ .envKey }}
{{- end -}}
