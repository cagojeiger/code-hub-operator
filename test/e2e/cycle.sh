#!/usr/bin/env bash
# End-to-end: run the full CodeHubRuntime scale cycle on a kind cluster.
#
# Phases:
#   A. apply CR → Pod Ready + CR phase=Running
#   B. in-cluster curl → Service HTTP 200
#   C. stale last_used in Redis → CR phase=ScaledDown, replicas=0
#   D. assert final state + events + operator still healthy
#
# Never touches the user's main kubectl context — every kubectl call is
# pinned with --context=${KUBE_CONTEXT}. Pre-requisite: a kind cluster named
# ${KIND_CLUSTER} exists and the operator image is loaded. See Makefile
# target `e2e-kind` for the wrapper that sets all of that up.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

KIND_CLUSTER="${KIND_CLUSTER:-codehub-dev}"
KUBE_CONTEXT="${KUBE_CONTEXT:-kind-${KIND_CLUSTER}}"
IMG="${IMG:-code-hub-operator:e2e}"
OPERATOR_NS="code-hub-operator-system"
APP_NS="e2e-demo"
CR_NAME="demo-runtime"
LAST_USED_KEY="runtime:${APP_NS}:${CR_NAME}:last_used_at"

KC=(kubectl --context="${KUBE_CONTEXT}")

fail() {
  echo
  echo "=================================================================="
  echo "E2E FAILED: $*"
  echo "=================================================================="
  dump_state || true
  exit 1
}

dump_state() {
  echo
  echo "=== operator deploy ==="
  "${KC[@]}" -n "${OPERATOR_NS}" get deploy,pods || true
  echo
  echo "=== operator logs (tail 60) ==="
  "${KC[@]}" -n "${OPERATOR_NS}" logs deploy/code-hub-operator-controller-manager --tail=60 || true
  echo
  echo "=== CR ==="
  "${KC[@]}" -n "${APP_NS}" get codehubruntime "${CR_NAME}" -o yaml || true
  echo
  echo "=== app ns resources ==="
  "${KC[@]}" -n "${APP_NS}" get codehubruntime,deploy,svc,pods || true
  echo
  echo "=== app ns events ==="
  "${KC[@]}" -n "${APP_NS}" get events --sort-by=.lastTimestamp | tail -20 || true
}

step() {
  echo
  echo "--- [$(date '+%H:%M:%S')] $*"
}

# ─── Pre-flight ──────────────────────────────────────────────────────────────
step "Pre-flight: checking kind cluster ${KIND_CLUSTER}"
if ! kind get clusters | grep -q "^${KIND_CLUSTER}$"; then
  fail "kind cluster '${KIND_CLUSTER}' not found. Run 'make e2e-kind' or create it first."
fi
"${KC[@]}" cluster-info >/dev/null || fail "kubectl cannot reach ${KUBE_CONTEXT}"

# ─── Install: CRD + RBAC + manager + redis ───────────────────────────────────
step "Installing CRD"
"${KC[@]}" apply -f "${REPO_ROOT}/config/crd/bases"

step "Installing RBAC"
"${KC[@]}" apply -f "${REPO_ROOT}/config/rbac"

step "Installing manager (image=${IMG})"
"${KC[@]}" apply -f "${REPO_ROOT}/config/manager/manager.yaml"
"${KC[@]}" -n "${OPERATOR_NS}" set image \
  deployment/code-hub-operator-controller-manager \
  "manager=${IMG}"
"${KC[@]}" -n "${OPERATOR_NS}" patch deployment code-hub-operator-controller-manager \
  --type=strategic \
  -p '{"spec":{"template":{"spec":{"containers":[{"name":"manager","imagePullPolicy":"IfNotPresent"}]}}}}'

step "Installing Redis (unauthenticated, in-cluster)"
cat <<'YAML' | "${KC[@]}" -n "${OPERATOR_NS}" apply -f -
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: redis
spec:
  replicas: 1
  selector:
    matchLabels:
      app: redis
  template:
    metadata:
      labels:
        app: redis
    spec:
      containers:
        - name: redis
          image: redis:7-alpine
          ports:
            - containerPort: 6379
---
apiVersion: v1
kind: Service
metadata:
  name: redis
spec:
  selector:
    app: redis
  ports:
    - port: 6379
      targetPort: 6379
YAML

step "Waiting for operator rollout"
"${KC[@]}" -n "${OPERATOR_NS}" rollout status deploy/code-hub-operator-controller-manager --timeout=180s \
  || fail "operator failed to roll out"
"${KC[@]}" -n "${OPERATOR_NS}" rollout status deploy/redis --timeout=120s \
  || fail "redis failed to roll out"

# ─── App namespace + CR ──────────────────────────────────────────────────────
step "Creating app namespace ${APP_NS}"
"${KC[@]}" create ns "${APP_NS}" --dry-run=client -o yaml | "${KC[@]}" apply -f -

step "Resetting Redis state (idempotency)"
"${KC[@]}" -n "${OPERATOR_NS}" exec deploy/redis -- \
  redis-cli DEL "${LAST_USED_KEY}" >/dev/null \
  || fail "redis DEL failed — is the operator redis pod running?"

step "Applying sample CR (nginx:alpine, idle=60s)"
"${KC[@]}" -n "${APP_NS}" delete codehubruntime "${CR_NAME}" --ignore-not-found --wait=true
cat <<YAML | "${KC[@]}" -n "${APP_NS}" apply -f -
---
apiVersion: runtime.project-jelly.io/v1alpha1
kind: CodeHubRuntime
metadata:
  name: ${CR_NAME}
spec:
  image: nginx:alpine
  imagePullPolicy: IfNotPresent
  servicePort: 80
  containerPort: 80
  idleTimeoutSeconds: 60
  minReplicas: 0
  maxReplicas: 1
  lastUsedKey: "${LAST_USED_KEY}"
  resources:
    requests:
      cpu: "50m"
      memory: "32Mi"
    limits:
      cpu: "200m"
      memory: "128Mi"
YAML

# ─── Phase A: scale-up ───────────────────────────────────────────────────────
step "Phase A — waiting for Pod Ready"
"${KC[@]}" -n "${APP_NS}" wait --for=condition=Ready pod \
  -l "app.kubernetes.io/instance=${CR_NAME}" --timeout=240s \
  || fail "Pod never became Ready"

PHASE_A=$("${KC[@]}" -n "${APP_NS}" get codehubruntime "${CR_NAME}" -o jsonpath='{.status.phase}')
DESIRED_A=$("${KC[@]}" -n "${APP_NS}" get codehubruntime "${CR_NAME}" -o jsonpath='{.status.desiredReplicas}')
[[ "${PHASE_A}" == "Running" ]] || fail "expected phase=Running, got '${PHASE_A}'"
[[ "${DESIRED_A}" == "1" ]] || fail "expected desiredReplicas=1, got '${DESIRED_A}'"
echo "Phase A OK: phase=${PHASE_A}, desiredReplicas=${DESIRED_A}"

# ─── Phase B: traffic ────────────────────────────────────────────────────────
step "Phase B — in-cluster curl to Service"
HTTP_CODE=$("${KC[@]}" -n "${APP_NS}" run curl-test --rm -i --restart=Never \
  --image=curlimages/curl:latest --command -- \
  curl -sS -o /dev/null -w '%{http_code}' "http://${CR_NAME}.${APP_NS}.svc.cluster.local" \
  | tr -d '[:space:]' || echo "000")
if [[ "${HTTP_CODE}" != *"200"* ]]; then
  fail "expected HTTP 200 from Service, got '${HTTP_CODE}'"
fi
echo "Phase B OK: Service returned HTTP 200"

# ─── Phase C: stale last_used → scale-to-0 ───────────────────────────────────
step "Phase C — setting stale last_used in Redis"
STALE_EPOCH=$(( $(date +%s) - 300 ))
"${KC[@]}" -n "${OPERATOR_NS}" exec deploy/redis -- \
  redis-cli SET "${LAST_USED_KEY}" "${STALE_EPOCH}" \
  || fail "redis SET failed"
echo "SET ${LAST_USED_KEY} = ${STALE_EPOCH} (5 min ago)"

step "Phase C — waiting for phase=ScaledDown (up to 120s)"
"${KC[@]}" -n "${APP_NS}" wait \
  --for=jsonpath='{.status.phase}'=ScaledDown \
  "codehubruntime/${CR_NAME}" --timeout=120s \
  || fail "CR never reached phase=ScaledDown"

# ─── Phase D: final assertions ───────────────────────────────────────────────
step "Phase D — final assertions"

REPLICAS_D=$("${KC[@]}" -n "${APP_NS}" get deploy "${CR_NAME}" -o jsonpath='{.spec.replicas}')
[[ "${REPLICAS_D}" == "0" ]] || fail "expected Deployment replicas=0, got '${REPLICAS_D}'"

# Poll up to 60s for all runtime pods to fully drain (terminating → deleted).
POD_COUNT=-1
for _ in $(seq 1 60); do
  POD_COUNT=$("${KC[@]}" -n "${APP_NS}" get pods \
    -l "app.kubernetes.io/instance=${CR_NAME}" \
    -o json | python3 -c '
import json, sys
pods = json.load(sys.stdin)["items"]
# Count only pods that are NOT terminating and NOT already Succeeded/Failed.
alive = [p for p in pods
         if p["metadata"].get("deletionTimestamp") is None
         and p["status"].get("phase") not in ("Succeeded", "Failed")]
print(len(alive))
')
  [[ "${POD_COUNT}" == "0" ]] && break
  sleep 1
done
[[ "${POD_COUNT}" == "0" ]] || fail "expected 0 active pods after scale-down, got ${POD_COUNT}"

# Scale-down event should be present. Poll briefly because event
# write is async to the Deployment spec update.
scale_down_seen=0
for _ in $(seq 1 15); do
  if "${KC[@]}" -n "${APP_NS}" get events -o jsonpath='{range .items[*]}{.message}{"\n"}{end}' \
      | grep -q "Scaled down replica set .* from 1 to 0"; then
    scale_down_seen=1
    break
  fi
  sleep 1
done
[[ "${scale_down_seen}" == "1" ]] || fail "no 'Scaled down ... from 1 to 0' event recorded"

# Operator pod must still be Running.
OP_READY=$("${KC[@]}" -n "${OPERATOR_NS}" get pods \
  -l app.kubernetes.io/name=code-hub-operator \
  -o jsonpath='{.items[0].status.containerStatuses[0].ready}')
[[ "${OP_READY}" == "true" ]] || fail "operator pod not Ready after cycle"

echo
echo "=================================================================="
echo "E2E PASS: full scale cycle observed"
echo "  Phase A: CR applied → Pod Ready, phase=Running"
echo "  Phase B: Service returned HTTP 200"
echo "  Phase C: stale Redis key → phase=ScaledDown"
echo "  Phase D: replicas=0, 0 active pods, scale-down event, operator healthy"
echo "=================================================================="
