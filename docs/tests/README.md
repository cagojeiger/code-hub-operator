# Test Documentation

`code-hub-operator` 전체 테스트 구조와 돌리는 방법.

## 구성

| 문서 | 대상 파일 | 테스트 수 |
|---|---|---|
| [store.md](./store.md) | `internal/store/fake_test.go` | 6 |
| [builders.md](./builders.md) | `internal/controller/deployment_test.go`, `service_test.go` | 11 |
| [reconcile.md](./reconcile.md) | `internal/controller/codehubruntime_controller_test.go` | 13 |

**총 30개 테스트 함수**, 전부 `go test ./...`만으로 실행된다.

## 설계 원칙

### 1. 외부 바이너리 의존 없음

envtest가 `kube-apiserver`/`etcd` 바이너리를 다운로드해 띄우는 방식은 CI 환경에서 플래키하고 느리다. 이 레포는 **`sigs.k8s.io/controller-runtime/pkg/client/fake`**를 쓴다.

- `kube-apiserver` 프로세스 불필요
- Kind/Minikube 불필요
- 테스트 전체가 1초 이내에 끝난다 (실측: `internal/controller/` 약 0.06s, `internal/store/` 약 0.02s)

**trade-off**: fake client는 실제 API 서버의 webhook, admission, field default 처리 같은 동작을 시뮬레이션하지 않는다. 그래서 E2E는 별도 수동 단계로 둔다 (Kind + 샘플 CR).

### 2. 결정적 테스트 (no sleeps, no timers)

시간 관련 테스트는 전부 **`fixedClock`**을 주입한다 (`internal/controller/codehubruntime_controller_test.go`의 `fixedClock` 타입). `time.Sleep`이나 `time.AfterFunc`에 의존하는 테스트는 0개.

```go
type fixedClock struct{ t time.Time }
func (c *fixedClock) Now() time.Time          { return c.t }
func (c *fixedClock) advance(d time.Duration) { c.t = c.t.Add(d) }
```

`TestReconcile_ClockAdvanceTriggersIdle`는 이 패턴으로 "31분 경과"를 0.001초만에 시뮬레이션한다.

### 3. 빠른 피드백용 단위 테스트 + 넓은 reconcile 매트릭스

| 계층 | 속도 | 커버 범위 |
|---|---|---|
| 순수 함수 단위 테스트 | 마이크로초 | `buildDeployment`, `buildService`, `validateForDeployment`, 비교 함수들 |
| FakeStore 단위 테스트 | 마이크로초 | 인터페이스 계약 (not-found, error, 동시성) |
| Reconcile 통합 테스트 (fake client) | 밀리초 | 전체 결정 흐름 13개 시나리오 |

### 4. 테스트는 "무엇을 보장하는가"를 명시한다

실패한 테스트만 봐도 어떤 불변 조건이 깨졌는지 알 수 있게 어설션 메시지를 단다. 예:

```go
require.Equal(t, int32(1), *dep.Spec.Replicas,
    "missing last-used must be treated as active to avoid scaling down fresh runtimes")
```

## 실행

### 전체
```bash
go test ./...
```

### 패키지별
```bash
go test ./internal/store/...
go test ./internal/controller/...
```

### 특정 테스트
```bash
go test ./internal/controller/... -run TestReconcile_IdleScalesDownToZero -v
```

### 경쟁 조건 검사
```bash
go test ./... -race -count=1
```

`-count=1`은 캐시 비활성화. `Makefile`의 `test` 타겟이 `-race -count=1`로 실행한다.

### 커버리지
```bash
go test ./... -cover
go test ./... -coverprofile=cover.out && go tool cover -html=cover.out
```

(v1에서 공식 커버리지 목표치는 정하지 않았다. 수치보다 "어떤 시나리오가 검증되는가"에 집중한다.)

## 테스트가 **아직 하지 않는 것**

| 영역 | 현황 | 대안 |
|---|---|---|
| 실제 API 서버 동작 (defaulting, admission) | fake client 한계 | 수동 Kind E2E |
| Redis 실제 연결 실패 | mocking만 | 수동 통합 테스트 또는 `testcontainers-go` (v1beta1 후보) |
| Webhook validation | v1에 webhook 없음 | 해당 없음 |
| Finalizer 로직 | v1에 finalizer 없음 | 해당 없음 |
| 멀티-controller 레이스 | 단일 프로세스 테스트 | HA 배포 도입 시 추가 |
| 성능/부하 테스트 | 없음 | 필요해지면 benchmark로 |

## 테스트 추가 가이드

새 시나리오를 추가할 때:

1. **순수 함수로 분리 가능한가?** → `deployment.go` / `service.go`에 함수를 넣고 `*_test.go`에 테스트
2. **Reconcile 결정 규칙인가?** → `newTestEnv` + `env.reconcile(name, ns)` 패턴 사용
3. **시간 관련인가?** → `env.clock.advance(...)` 또는 `env.clock.set(...)` 사용, `time.Sleep` 금지
4. **새 의존성(store, clock)** → 기존 인터페이스 확장, `cmd/main.go`에서만 real 구현 주입

절대 하지 말 것:
- 테스트 안에서 `time.Sleep` 사용
- `realClock`이 기본값이라는 이유로 테스트에서 시간 assertion 생략
- fake client가 "실제 API와 다르니까" 검증 생략 — 같은 계약을 따르는 범위 안에서는 충분히 유효하다
