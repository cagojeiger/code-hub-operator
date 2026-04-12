# Test Documentation

`code-hub-operator` 의 테스트는 **3-tier** 로 구성된다. 각 tier는 다른 속도·커버리지 trade-off를 갖고, CI의 다른 스테이지에서 실행된다.

```
┌────────────────────────────────────────────────────────────────┐
│ Tier 1: Unit (fake client)                                     │
│  - 속도: ~0.1s   커버: 결정 로직 / 빌더 / 스토어 계약          │
│  - 실행: go test ./...                                         │
│  - CI: 모든 PR, 모든 푸시                                      │
├────────────────────────────────────────────────────────────────┤
│ Tier 2: Envtest (real kube-apiserver + etcd)                   │
│  - 속도: ~5s    커버: CRD validation / watch / owner GC        │
│  - 실행: go test -tags=envtest ./...                           │
│  - CI: 모든 PR (envtest 바이너리 prefetch 후)                  │
├────────────────────────────────────────────────────────────────┤
│ Tier 3: E2E on kind (real node + kubelet + network)            │
│  - 속도: ~2–5 min  커버: 이미지 pull / 트래픽 / 풀 사이클      │
│  - 실행: make e2e-kind  (또는 test/e2e/cycle.sh)               │
│  - CI: main 푸시 / workflow_dispatch / `e2e` 라벨 PR           │
└────────────────────────────────────────────────────────────────┘
```

## 문서 맵

| 문서 | 다루는 Tier | 대상 파일 |
|---|---|---|
| [store.md](./store.md) | Unit | `internal/store/fake_test.go` |
| [builders.md](./builders.md) | Unit | `internal/controller/deployment_test.go`, `service_test.go` |
| [reconcile.md](./reconcile.md) | Unit | `internal/controller/codehubworkspace_controller_test.go` |
| [envtest.md](./envtest.md) | Envtest | `internal/controller/envtest_test.go` |
| [e2e.md](./e2e.md) | E2E | `test/e2e/cycle.sh` |

## 왜 3-tier인가

단일 tier로는 서로 다른 질문에 답하지 못한다.

| 질문 | 답하는 tier |
|---|---|
| "reconcile 결정 로직이 분기별로 맞는가" | Unit |
| "CRD 스키마 validation (min/max/required) 이 실제로 먹히는가" | Envtest |
| "`Owns(&Deployment{})` watch가 실제로 동작하는가" | Envtest |
| "CR 삭제 시 Deployment/Service가 owner GC로 지워지는가" | Envtest |
| "실제 이미지 pull 후 Pod가 Ready가 되는가" | E2E |
| "Service 경유 HTTP 트래픽이 Pod까지 도달하는가" | E2E |
| "Redis 스테일 키 → 실 클러스터에서 scale-to-0 까지 가는가" | E2E |

Unit은 빠르지만 fake client 한계로 CRD validation·watch·GC를 우회한다. Envtest는 real API server를 띄우므로 그 구멍을 메우되, kubelet이 없어 Pod는 Pending에 머문다. E2E는 kind 노드에서 실제로 Pod를 띄우지만 1분 이상 걸려 모든 PR에 돌리기 비싸다.

## 설계 원칙

### 1. Tier 1은 외부 의존 없음

`sigs.k8s.io/controller-runtime/pkg/client/fake` 사용. `kube-apiserver` 프로세스, kind, docker, 네트워크 전부 불필요. 로컬에서 `go test ./...` 한 줄로 30+ 테스트가 1초 안에 끝난다.

### 2. Tier 2는 build tag 로 격리

`//go:build envtest` 태그로 envtest 테스트 파일을 묶는다. `KUBEBUILDER_ASSETS` 환경변수가 설정돼야만 실행되므로, 바이너리가 없는 환경에서 `go test ./...`가 깨지지 않는다.

```bash
# Unit만 (기본)
go test ./...

# Envtest 포함
setup-envtest use 1.30.0 -p env > /tmp/envtest.env
source /tmp/envtest.env
go test -tags=envtest ./...
```

### 3. Tier 3은 스크립트로, 아이템포턴트하게

`test/e2e/cycle.sh` 는 kind 클러스터 생성 → 이미지 로드 → CRD/RBAC/operator/redis apply → 샘플 CR 적용 → 풀 사이클 검증 → exit 0/1 로 동작한다. 실패 원인이 보이도록 각 단계에 `echo '[step]'` 와 상태 덤프가 붙어 있다.

메인 `kubectl` context는 절대 건드리지 않는다. 모든 명령에 `--context=kind-codehub-dev` 를 명시한다.

### 4. 결정적 테스트 (no sleeps, no timers)

Unit tier는 `fixedClock` 을 주입해서 시간을 제어한다. `time.Sleep` 은 0개.

```go
type fixedClock struct{ t time.Time }
func (c *fixedClock) Now() time.Time          { return c.t }
func (c *fixedClock) advance(d time.Duration) { c.t = c.t.Add(d) }
```

`TestReconcile_ClockAdvanceTriggersIdle` 은 이 패턴으로 "idleTimeout + 1초 경과" 를 0.001초에 시뮬레이션한다.

Envtest tier는 실제 시간을 쓰지만, polling 은 `EventuallyWithT` + 최대 5초 deadline 으로 묶어서 느린 CI에서도 안정적이다.

### 5. 테스트는 "무엇을 보장하는가" 를 명시한다

실패한 테스트만 봐도 어떤 불변 조건이 깨졌는지 알 수 있게 어설션 메시지를 단다.

```go
require.Equal(t, int32(1), *dep.Spec.Replicas,
    "missing last-used must be treated as active to avoid scaling down fresh runtimes")
```

## 실행

### Tier 1 (Unit)

```bash
# 전체
go test ./...

# race 검사
go test ./... -race -count=1

# 특정 테스트
go test ./internal/controller/... -run TestReconcile_IdleScalesDownToZero -v

# 커버리지
go test ./... -coverprofile=cover.out && go tool cover -html=cover.out
```

`Makefile` 의 `test` 타겟이 `-race -count=1` 로 실행한다.

### Tier 2 (Envtest)

```bash
make envtest-setup    # setup-envtest 바이너리 설치
make test-envtest     # KUBEBUILDER_ASSETS export 후 -tags=envtest 실행
```

수동:
```bash
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
export KUBEBUILDER_ASSETS=$(setup-envtest use 1.30.0 -p path)
go test -tags=envtest ./internal/controller/... -race -count=1
```

### Tier 3 (E2E)

```bash
make e2e-kind
```

내부적으로 하는 일:
```
docker build -t code-hub-operator:e2e .
kind create cluster --name codehub-dev
kind load docker-image code-hub-operator:e2e --name codehub-dev
test/e2e/cycle.sh
```

후처리:
```bash
kind delete cluster --name codehub-dev
```

## 테스트가 **아직 하지 않는 것**

| 영역 | 현황 | 대안 |
|---|---|---|
| Webhook validation | v1에 webhook 없음 | 해당 없음 |
| Finalizer 로직 | v1에 finalizer 없음 | 해당 없음 |
| 멀티-controller 레이스 | 단일 프로세스 테스트 | HA 배포 도입 시 envtest로 추가 |
| 성능/부하 테스트 | 없음 | 필요해지면 benchmark로 |
| Redis 실제 장애 (timeout/partial response) | unit에서 fake로만 | testcontainers-go (후보) |
| 이미지 pull 실패 복구 | e2e에서 관찰 안 함 | 후속 시나리오 |

## 테스트 추가 가이드

새 시나리오를 추가할 때 **어느 tier가 맞는지 먼저 결정**한다:

1. **순수 함수로 분리 가능** → Unit, `deployment.go` / `service.go` 에 함수를 넣고 `*_test.go` 추가
2. **Reconcile 결정 규칙** → Unit, `newTestEnv` + `env.reconcile(name, ns)` 패턴
3. **CRD 스키마 / watch / GC / SSA** → Envtest, `envtest_test.go` 에 추가
4. **이미지 pull / 네트워크 / 풀 수명 주기** → E2E, `test/e2e/cycle.sh` 확장
5. **시간 관련** → Unit 우선, `env.clock.advance(...)` 사용. 절대 `time.Sleep` 금지.

절대 하지 말 것:
- Unit 테스트 안에서 `time.Sleep`
- Envtest에서 Pod Ready를 기다림 (kubelet 없음, 영원히 Pending)
- E2E에서 외부 레지스트리 의존 (kind load docker-image 로 주입)

## CI 매핑

| CI Job | Tier | 주기 | 예상 시간 |
|---|---|---|---|
| `lint` | - | every PR | <30s |
| `unit` | Tier 1 | every PR | <30s |
| `envtest` | Tier 2 | every PR | ~1min |
| `e2e-kind` | Tier 3 | main push / workflow_dispatch / PR with `e2e` label | 3–6min |

자세한 workflow YAML 은 `.github/workflows/ci.yml` 참고.
