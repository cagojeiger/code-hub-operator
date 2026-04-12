# Store Spec — `LastUsedStore`

코드 위치: `internal/store/`.

## 왜 외부 스토어인가

CR의 `status`에 `last_used_at`을 기록하는 방식은 문제가 크다.

- **쓰기 비용**: 사용자 요청마다 Kubernetes API에 status update를 발생시키면 API 서버와 etcd가 얻어맞는다.
- **멀티 작성자**: 여러 앱/gateway 인스턴스가 동시에 쓰면 resourceVersion 충돌이 빈번하다.
- **책임 분리 위반**: 오퍼레이터는 쿠버네티스 상태를 제어하고, 앱은 자기 사용 시각을 기록해야 한다. 같은 객체에 둘이 쓰는 구조는 제어/관측을 섞는다.

따라서 `last_used_at`은 **외부 스토어**(v1은 Redis)에 둔다. 오퍼레이터는 **읽기만** 한다.

## 인터페이스

파일: `internal/store/store.go`

```go
type LastUsedStore interface {
    Get(ctx context.Context, key string) (time.Time, bool, error)
}
```

반환 계약:

| 케이스 | 반환 |
|---|---|
| 키에 값이 있음 | `(ts, true, nil)` |
| 키가 없음 | `(time.Time{}, false, nil)` |
| 전송/파싱 실패 | `(time.Time{}, false, err)` |

**key 없음과 에러는 엄격히 구분된다**. 오퍼레이터는 "없음"은 active로 간주하지만 "에러"는 Phase=Error로 처리하고 replicas를 건드리지 않는다.

구현체는 **동시성 안전해야 한다** — reconcile이 여러 워커에서 동시에 돌 수 있기 때문.

## Redis 구현

파일: `internal/store/redis.go`

```go
type RedisStore struct { client *redis.Client }
func NewRedisStore(client *redis.Client) *RedisStore
```

- 라이브러리: `github.com/redis/go-redis/v9`
- `redis.Nil` → `(zero, false, nil)` 매핑
- 값 파싱: `strconv.ParseInt(raw, 10, 64)` → `time.Unix(secs, 0)`
- 파싱 실패 시 에러를 리턴하므로 오퍼레이터는 Phase=Error로 빠진다 (데이터 오염 방지)

### Key 포맷 (관례)

오퍼레이터는 key 형식을 강제하지 않는다. CR의 `spec.lastUsedKey`를 그대로 쓴다. 다만 관례는 다음과 같다.

```
workspace:<namespace>:<name>:last_used_at
```

예: `workspace:demo:demo-workspace:last_used_at`

### Value 포맷

| 포맷 | v1 지원 |
|---|---|
| Unix epoch seconds (decimal string) | ✅ |
| RFC3339 | ❌ (지금은 파싱 안 함) |

v1은 **정수만** 받는다. RFC3339 지원은 v1beta1 후보.

### 기록 주체 (오퍼레이터 외부)

누가 Redis에 쓰는지는 배포 설계에 따라 다르다. 권장 순서:

1. **애플리케이션 내부**: 요청 핸들러가 "의미 있는 사용"(healthz/metrics 제외) 완료 후 `SET`
2. **API Gateway/Proxy**: 라우팅 지점에서 경로별 필터링 후 `SET`
3. **별도 checker**: 특수한 경우 (v1은 권장 안 함)

`SET`은 멱등이므로 레이스 조건 걱정이 없다.

## Fake 구현

파일: `internal/store/fake.go`

```go
type FakeStore struct { ... }
func NewFakeStore() *FakeStore
func (f *FakeStore) Set(key string, ts time.Time)
func (f *FakeStore) Delete(key string)
func (f *FakeStore) SetError(err error) // 이후 Get 호출이 err 반환
```

`sync.RWMutex`로 보호돼 동시 접근이 안전하다. 테스트에서 `SetError(nil)`로 에러 상태를 복구할 수 있다.

`Get`은 실제 Redis 구현과 동일한 계약을 지킨다:

```go
func (f *FakeStore) Get(ctx, key) (time.Time, bool, error) {
    if f.err != nil { return zero, false, f.err }
    ts, ok := f.entries[key]
    if !ok { return zero, false, nil }
    return ts, true, nil
}
```

## 에러 재시도 전략

- `RedisStore.Get`은 **자체 재시도를 하지 않는다**. controller-runtime의 reconcile 주기(30초)에 맡긴다.
- Redis 클라이언트 레벨의 timeout/연결 풀은 `cmd/main.go`에서 `redis.Options`로 설정할 수 있다 (v1은 기본값 사용).

## v1에 **없는** 것

- **복수 저장소 지원** (e.g. Postgres, DynamoDB): 인터페이스는 열려 있지만 구현체는 Redis 하나만.
- **Bulk Get**: 컨트롤러가 개별 key만 조회하므로 bulk API 불필요.
- **Write API**: 오퍼레이터는 쓰기 권한을 요구하지 않는다. `LastUsedStore` 인터페이스에 `Set`이 없는 이유.
- **Redis Cluster / Sentinel 전용 경로**: `redis.Client`가 이미 두 모드를 모두 지원하므로 별도 추상화는 만들지 않았다.
