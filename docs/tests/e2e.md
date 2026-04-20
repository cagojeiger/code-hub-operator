# E2E 테스트 (kind)

**Tier 3** — 실제 kind 클러스터에서 operator 를 띄우고, 풀 스케일 사이클을 관찰한다.

대상 파일: `test/e2e/cycle.sh`, `Makefile` 의 `e2e-kind` 타겟

## 이 tier 가 보장하는 것

Unit / Envtest 가 만질 수 없는 영역:

| 검증 항목 | 왜 여기서만 가능한가 |
|---|---|
| 실제 이미지 pull + Pod 기동 | kubelet 이 있어야 함 |
| Service → Pod 트래픽 라우팅 | CNI / kube-proxy 가 있어야 함 |
| RBAC / ServiceAccount 실제 permission | API server + authorizer 전체 경로 |
| 리더 선출 lease 획득 | coordination.k8s.io API + RBAC |
| Redis 실제 연결 + key SET/GET 경로 | Pod-to-Pod 네트워크 |
| reconcile requeue 주기가 실시간으로 동작 | wall clock |

이번 PR 개발 중 이 tier 가 실제로 찾아낸 것:
- `config/rbac/role.yaml` 에 `coordination.k8s.io/leases` 권한 누락 → 리더 선출 실패 → reconcile 시작 불가. Unit/Envtest 로는 잡히지 않았다.

## 사이클 스크립트

`test/e2e/cycle.sh` 는 네 단계를 순서대로 실행한다:

### Phase A — Scale-up from create

1. `CodeHubWorkspaceClass` 적용 (`e2e-standard`)
2. `CodeHubWorkspace` CR 적용 (`classRef: e2e-standard`, `minReplicas: 0`, `maxReplicas: 1`)
3. 기대: 리컨사일러가 Redis 에 키 없음을 보고 `isIdle=false` → `desired=maxReplicas=1` 로 판단
4. Deployment 생성, replicas=1, Pod 스케줄링 → image pull → Ready
5. `kubectl wait --for=condition=Ready pod ...` 로 최대 240초 대기
6. CR status: `phase=Running`, `desiredReplicas=1`, `readyReplicas=1`, `resolvedClass=e2e-standard`

### Phase B — Service traffic

1. in-cluster curl Pod 를 spawn (`curlimages/curl`)
2. `http://demo-workspace.e2e-demo.svc.cluster.local` 에 GET
3. 기대: `HTTP 200` (nginx default welcome)
4. Service → Pod 경로가 실제로 동작함을 증명

### Phase C — Scale-down via idle

1. Redis 에 `SET <lastUsedKey> <now-120>` (2분 전 epoch seconds)
2. `kubectl wait --for=jsonpath='{.status.phase}'=ScaledDown` 최대 90초 대기
3. 기대: 다음 reconcile 에서 리컨사일러가 `now - lastUsed > idleTimeout` 판정 → `desired=minReplicas=0`
4. Deployment replicas=0, Pod 종료
5. CR status: `phase=ScaledDown`, `idleSince` 채워짐

### Phase D — Final assertions

스크립트 마지막에 다음을 모두 검증:
- Deployment replicas == 0
- Pod 개수 == 0
- CR phase == `ScaledDown`
- `kubectl get events` 에 `ScalingReplicaSet ... from 1 to 0` 존재
- Operator pod 가 여전히 Running (리컨사일러가 살아있음)

하나라도 실패하면 non-zero exit + 전체 상태 덤프 출력.

## 실행

### 한 번에
```bash
make e2e-kind
```

내부적으로:
```bash
docker build -t code-hub-operator:e2e .
kind create cluster --name codehub-dev --wait 60s
kind load docker-image code-hub-operator:e2e --name codehub-dev
IMG=code-hub-operator:e2e test/e2e/cycle.sh
```

### 단계 분리 (디버깅용)
```bash
# 클러스터만 먼저
kind create cluster --name codehub-dev

# 이미지 반복 로드 (코드 수정 시)
docker build -t code-hub-operator:e2e . && \
  kind load docker-image code-hub-operator:e2e --name codehub-dev && \
  kubectl --context=kind-codehub-dev -n code-hub-operator-system rollout restart deploy/code-hub-operator-controller-manager

# 사이클만 다시
IMG=code-hub-operator:e2e test/e2e/cycle.sh

# 정리
kind delete cluster --name codehub-dev
```

### 메인 kubectl context 보호

스크립트는 **모든** `kubectl` 명령에 `--context=kind-codehub-dev` 를 명시한다. `kubectl config use-context` 는 절대 호출하지 않는다. 실행 전/후 사용자의 현재 context 는 변하지 않는다.

이번 PR 개발 중 합의된 규칙이다 — 로컬 개발자는 이미 home 클러스터에 붙어 있을 수 있고, 테스트 때문에 context 가 바뀌면 실수로 운영 클러스터에 명령을 날리기 쉽다.

## 사용 이미지

| 용도 | 이미지 | 왜 이걸 쓰나 |
|---|---|---|
| 리컨사일러가 만드는 샘플 runtime | `nginx:alpine` | 작고(25MB), 즉시 HTTP 200 응답 |
| operator manager 자체 | `code-hub-operator:e2e` (로컬 빌드) | kind load 로 주입 |
| Redis | `redis:7-alpine` | 최소 설정, 인증 없음 |
| 트래픽 검증 | `curlimages/curl:latest` | in-cluster 에서 1회성 curl |

외부 레지스트리 credential 은 필요없다. 전부 Docker Hub 퍼블릭 이미지.

## 제약

- **kind 1노드**: multi-node 스케줄링 이슈는 잡지 못한다.
- **CNI 는 기본 kindnet**: Cilium/Istio 특수 동작은 여기서 관찰 안 됨.
- **StorageClass 는 host-path**: PV 실 스토리지 성능은 검증 대상 아님.
- **시간**: 약 60초의 idle 대기 + 이미지 pull 시간이 있어 전체 사이클 스크립트는 3–6분 걸린다. 빠른 피드백용은 아니다.

## CI 통합

`.github/workflows/ci.yml` 의 `e2e-kind` job 이 이 스크립트를 호출한다.

조건:
- main 브랜치 push
- `workflow_dispatch` (수동)
- PR 에 `e2e` 라벨이 붙은 경우

Unit / Envtest job 이 성공한 경우에만 실행 (job dependency).

## 실패 진단

스크립트가 실패하면 로그 마지막에 다음을 덤프한다:
```
=== CR status ===          (kubectl get codehubworkspace -o yaml)
=== demo ns resources ===  (get codehubworkspace,deploy,svc,pods)
=== events (demo) ===      (get events --sort-by=.lastTimestamp)
=== operator logs ===      (logs deploy/code-hub-operator-controller-manager)
```

CI 에서는 이 블록이 job summary 에 그대로 남는다. 대부분의 실패는 이 덤프만 보면 원인이 나온다.

## 추가 가이드

새 E2E 시나리오를 추가할 때:

1. **Unit/Envtest 로 대체 불가능한가?** 먼저 자문한다. 가능하면 위 tier 로 옮긴다. kind e2e 는 최후의 수단.
2. **idempotent 하게 쓴다**: 같은 클러스터에서 재실행해도 깨지지 않도록. `kubectl delete` 로 이전 상태를 먼저 치운다.
3. **timeout 은 넉넉하게**: CI GitHub-hosted runner 는 로컬보다 느리다. 이미지 pull 은 180s, reconcile 대기는 90s 를 기본으로.
4. **assertion 은 마지막에 모아서**: 중간 단계에서 exit 하면 덤프를 못 남긴다. 단계마다 `|| true` 로 돌고 마지막에 flag 로 판정.
5. **외부 의존 금지**: 테스트 중 인터넷에서 뭘 당겨오면 rate limit / 망 장애로 플래키해진다. 필요한 이미지는 전부 `kind load` 로 주입.
