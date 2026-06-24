# crond-agent Helm chart

Monitor your Kubernetes CronJobs with [crond.io](https://crond.io).

> **Prefer prose with steps and screenshots?** See the walkthrough at
> [crond.io/docs/kubernetes](https://crond.io/docs/kubernetes). This README
> is the engineer reference (values table, macros, troubleshooting).

This chart does **not** create CronJobs. It manages a `Secret` of ping
keys and provides Helm template macros you call from your own CronJob
spec to inject an init container + command rewrite. Your CronJob keeps
its own YAML.

Two installation patterns, same chart:

1. **Init-container injection** (default, recommended) — your job image
   stays untouched. An init container copies the agent binary into a
   shared `emptyDir`; the main container runs it.
2. **Pre-baked image** — your image already contains `/crond-agent`.

## Requirements

- Helm 3.10+
- Kubernetes 1.25+
- A crond.io account on Pro or Enterprise (K8s source is Pro+ gated)
- One or more crond.io monitors created with `source=k8s` — each gives you
  a `ping_key` UUID

## Install

```bash
helm install crond-agent oci://ghcr.io/platops-security/crond-agent/charts/crond-agent \
  --version 0.2.0 \
  --namespace my-app --create-namespace \
  --set pingKeys.PING_KEY_BACKUP=11111111-1111-1111-1111-111111111111 \
  --set pingKeys.PING_KEY_REPORTS=22222222-2222-2222-2222-222222222222
```

Each `pingKeys` entry becomes a key in the chart-owned Secret
`<release>-pingkeys` and is mounted as an env var inside the wrapped job.

Multi-namespace setups install the chart per-namespace.

## Wrap a CronJob — init-container pattern

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: nightly-backup
  namespace: my-app                  # must match the chart's release namespace
spec:
  schedule: "0 2 * * *"              # must match the monitor's schedule in crond.io
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: OnFailure
          {{- include "crond-agent.wrap" (dict
              "context" $
              "envKey" "PING_KEY_BACKUP"
              "image" "myco/backup:1.0"
              "command" (list "/opt/backup.sh" "--full"))
            | nindent 10 }}
```

The wrapper expands to: pod-level `securityContext`, an `emptyDir`
volume, an init container running `crond-agent install`, and a `job`
container running `crond-agent exec --key=$(PING_KEY_BACKUP) -- /opt/backup.sh --full`.

This snippet must be rendered through `helm template` against THIS
chart — `crond-agent.wrap` is a chart macro. The simplest path is to
ship your CronJob YAMLs as templates inside your own umbrella chart that
depends on `crond-agent`.

## Wrap a CronJob — pre-baked image pattern

If you control your job image, bake the agent in:

```dockerfile
FROM ghcr.io/platops-security/crond-agent:0.1.1 AS agent

FROM alpine:3.20
COPY --from=agent /crond-agent /usr/local/bin/crond-agent
COPY backup.sh /opt/backup.sh
ENTRYPOINT ["/opt/backup.sh"]
```

Then your CronJob skips the wrapper macro and invokes `crond-agent`
directly — only `envFromSecret` is reused. See
`examples/cronjob-prebaked.yaml`.

## Values reference

| Key | Default | Description |
|---|---|---|
| `image.repository` | `ghcr.io/platops-security/crond-agent` | Agent container image |
| `image.tag` | `"0.1.1"` | First release with the `install` subcommand |
| `image.pullPolicy` | `IfNotPresent` | |
| `agent.apiUrl` | `https://api.crond.io` | Override for self-hosted / staging |
| `agent.extraArgs` | `[]` | Extra flags appended to every `crond-agent exec` call |
| `agent.captureOutput` | `true` | When `false`, drops captured stdout/stderr from the ping payload. Sets `CROND_CAPTURE_OUTPUT` on the wrapped container. |
| `agent.redactPatterns` | `[]` | Go regexps applied to captured streams before send. Matches → `[REDACTED]`. Joined with newlines into `CROND_REDACT_PATTERNS`. |
| `pingKeys` | `{}` | Map of `ENV_VAR_NAME` → ping key UUID. Each becomes a Secret key. |
| `podSecurityContext` | non-root, fsGroup 65532, seccomp RuntimeDefault | Pod-level hardening |
| `containerSecurityContext` | no privesc, RO root, drop all caps | Container-level hardening |
| `initResources.{requests,limits}` | 10m/16Mi → 100m/64Mi | Init container only; main container resources are yours |
| `sharedVolumeName` | `crond-agent-shared` | Override on volume-name collision |
| `sharedMountPath` | `/shared` | Where init container drops the binary |

## How it works

```
┌── Pod ──────────────────────────────────────────────────────────┐
│  Volumes: { crond-agent-shared (emptyDir) }                     │
│                                                                  │
│  initContainer "crond-agent-installer"                          │
│    image: ghcr.io/.../agent:0.1.1   (FROM scratch, ~3-10 MB)    │
│    command: /crond-agent install --target=/shared/crond-agent   │
│           ↓ copies own binary into the shared emptyDir          │
│                                                                  │
│  container "job"                                                │
│    image: <your-image>                                          │
│    command: /shared/crond-agent exec --key=$(PING_KEY_X)        │
│             --api-url=… -- <your original command>              │
│           ↓ on exit (0 or non-zero) → HTTPS POST to api.crond.io│
└──────────────────────────────────────────────────────────────────┘
```

The agent image is `FROM scratch` — no shell, no `cp`. The init
container uses the agent's own `install` subcommand (added in agent
v0.1.1) to self-copy into the shared volume.

## Troubleshooting

**`kubectl logs <pod>` shows nothing from the wrapped job.**
Confirm the chart's image tag is `0.1.1` or later. v0.1.0 lacks the
`--passthrough-stdout` default that tees the child's output to the
agent's stdout.

**Pod fails with `secret not found` or `key not found`.**
Either the chart wasn't installed in the same namespace as the CronJob,
or the env var name in `crond-agent.wrap`'s `envKey` doesn't match a key
in `.Values.pingKeys`. Check:
```bash
kubectl get secret <release>-pingkeys -n <ns> -o yaml
```

**`secret not found` with a name that has an extra prefix
(`t-crond-agent-pingkeys` instead of `crond-agent-pingkeys`).**
The chart's secret name is derived from the Helm release name. If you
render an example CronJob via `helm template <some-other-name> ...` and
apply it against a chart installed under release name `crond-agent`, the
secretKeyRef will point at the wrong secret. **Use the same release name
for chart install and CronJob render.** Or override with `fullnameOverride`.

**Agent retries `ping retries exhausted ... no such host` from inside
the cluster.**
The pod can't resolve `api.crond.io`. Usually a cluster DNS / egress
issue, not a chart problem. Verify:
```bash
kubectl run dns-test --rm -it --image=busybox -- nslookup api.crond.io
```
If lookup fails inside the cluster but works on your laptop, check
CoreDNS forwarders and any NetworkPolicy that might block egress to
`api.crond.io:443`.

**Schedule drift between CronJob and crond.io monitor.**
The CronJob's `spec.schedule` and the crond.io monitor's schedule are
**independent** — there is no operator that syncs them in V1. If they
differ, the monitor goes "late" even when the job runs on time. Set
both to the same cron expression and grace window.

**`helm install` fails with `cannot patch` or `field is immutable`.**
You're trying to upgrade across a chart major version. `helm uninstall`
then `helm install` (the Secret will be recreated; pingKeys are
plumbing, not data, so this is safe).

**`helm test` fails referencing `PING_KEY_BACKUP`.**
The smoke-test fixture under `templates/tests/` requires
`pingKeys.PING_KEY_BACKUP` to be set. Either define it or skip
`tests.enabled`.

## What's NOT in this chart (yet)

- No mutating admission webhook (auto-injection)
- No auto-discovery (your monitor must be created in the dashboard;
  copy the ping_key UUID into the chart by hand)
- No CRD or operator
- No support for K8s `Job` (one-shot) — CronJob only

These are tracked for V2+.

## License

MIT. See repo root for full text.
