#!/usr/bin/env bash
# End-to-end test for the crond-agent mutating admission webhook (V2 injection).
#
# Spins up a kind cluster, installs the chart with the injector enabled using a
# locally built image, and asserts:
#   1. an opted-in CronJob is auto-wrapped (init container + rewritten command
#      + injected marker),
#   2. failurePolicy: Ignore — with the injector scaled to 0, CronJob creation
#      still succeeds (unmutated) rather than being blocked.
#
# Requires: docker, kind, kubectl, helm. Self-cleaning.
set -euo pipefail

CLUSTER="crond-injector-e2e"
NS="my-jobs"
IMAGE="crond-agent-injector:e2e"
RELEASE="ca"
CHART="$(cd "$(dirname "$0")/.." && pwd)/charts/crond-agent"

cleanup() { kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "==> building injector image"
docker build -f "$(dirname "$0")/../Dockerfile.injector" -t "$IMAGE" --build-arg VERSION=e2e "$(dirname "$0")/.." >/tmp/e2e-docker.log 2>&1 \
  || { echo "docker build failed"; tail -20 /tmp/e2e-docker.log; exit 1; }

echo "==> creating kind cluster"
kind create cluster --name "$CLUSTER" >/dev/null 2>&1
kind load docker-image "$IMAGE" --name "$CLUSTER" >/dev/null 2>&1

echo "==> helm install (injector enabled, local image)"
helm install "$RELEASE" "$CHART" \
  --namespace "$NS" --create-namespace \
  --set injector.enabled=true \
  --set injector.image.repository=crond-agent-injector \
  --set injector.image.tag=e2e \
  --set injector.image.pullPolicy=Never \
  --set injector.replicaCount=1 \
  --set pingKeys.PING_KEY_BACKUP=11111111-1111-1111-1111-111111111111 \
  >/dev/null

echo "==> waiting for injector rollout (webhook must be ready before we test)"
kubectl -n "$NS" rollout status deploy/"$RELEASE"-crond-agent-injector --timeout=120s

echo "==> labeling namespace to opt it into the webhook"
kubectl label ns "$NS" crond.io/inject=enabled --overwrite >/dev/null

echo "==> applying an opted-in CronJob"
cat <<'YAML' | kubectl -n my-jobs apply -f - >/dev/null
apiVersion: batch/v1
kind: CronJob
metadata:
  name: wrapped
  annotations:
    crond.io/inject: "true"
    crond.io/ping-key-env: PING_KEY_BACKUP
spec:
  schedule: "*/5 * * * *"
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: OnFailure
          containers:
            - name: backup
              image: alpine:3.20
              command: ["/bin/sh", "-c"]
              args: ["echo hello"]
YAML

echo "==> asserting injection"
kubectl -n "$NS" get cronjob wrapped -o json >/tmp/wrapped.json
fail=0
grep -q 'crond-agent-installer' /tmp/wrapped.json || { echo "FAIL: no installer init container"; fail=1; }
grep -q '/shared/crond-agent'   /tmp/wrapped.json || { echo "FAIL: command not rewritten"; fail=1; }
grep -q 'crond.io/injected'      /tmp/wrapped.json || { echo "FAIL: no injected marker"; fail=1; }
grep -q 'secretKeyRef'           /tmp/wrapped.json || { echo "FAIL: no secretKeyRef env"; fail=1; }
[ "$fail" = 0 ] && echo "PASS: opted-in CronJob was auto-wrapped"

echo "==> failurePolicy: scaling injector to 0, creating a CronJob"
kubectl -n "$NS" scale deploy/"$RELEASE"-crond-agent-injector --replicas=0 >/dev/null
kubectl -n "$NS" wait --for=delete pod -l app.kubernetes.io/name=crond-agent-injector --timeout=60s >/dev/null 2>&1 || true
cat <<'YAML' | kubectl -n my-jobs apply -f - >/dev/null
apiVersion: batch/v1
kind: CronJob
metadata:
  name: when-down
  annotations:
    crond.io/inject: "true"
    crond.io/ping-key-env: PING_KEY_BACKUP
spec:
  schedule: "*/5 * * * *"
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: OnFailure
          containers:
            - name: backup
              image: alpine:3.20
              command: ["/bin/sh", "-c"]
              args: ["echo hello"]
YAML
kubectl -n "$NS" get cronjob when-down -o json >/tmp/whendown.json
if grep -q 'crond-agent-installer' /tmp/whendown.json; then
  echo "FAIL: CronJob was mutated with the webhook down"; fail=1
else
  echo "PASS: CronJob created unmutated while injector down (failurePolicy: Ignore)"
fi

[ "$fail" = 0 ] && echo "E2E: ALL PASS" || { echo "E2E: FAILURES"; exit 1; }
