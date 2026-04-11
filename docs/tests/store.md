# Store Tests

파일: `internal/store/fake_test.go`

## 대상

`internal/store/fake.go`의 `FakeStore`는 테스트에서 Redis를 대체하는 in-memory 구현이다. 컨트롤러 테스트가 이 객체에 의존하므로, FakeStore 자체가 `LastUsedStore` 인터페이스 계약을 올바르게 지키는지 별도 테스트로 검증한다.

`RedisStore`(실제 Redis 구현) 단위 테스트는 v1에 없다. 실제 Redis를 띄우는 통합 테스트 없이 `go-redis/v9` 내부를 모킹하는 것은 가치가 낮기 때문. 필요해지면 `testcontainers-go` 기반 테스트를 v1beta1에 추가한다.

## 테스트 목록

| # | 함수 | 무엇을 보장 |
|---|---|---|
| 1 | `TestFakeStore_EmptyReturnsNotFound` | 빈 스토어에서 `Get`은 `(zero, false, nil)` 반환 — 에러가 아닌 "없음" 시맨틱 유지 |
| 2 | `TestFakeStore_SetGetRoundTrip` | `Set(k, ts)` 후 `Get(k)`가 같은 `ts` 반환, `ok=true` |
| 3 | `TestFakeStore_Delete` | `Delete` 후 `Get`은 `ok=false` |
| 4 | `TestFakeStore_ErrorPropagates` | `SetError(boom)` 후 `Get`이 `ErrorIs(boom)` 만족 |
| 5 | `TestFakeStore_ErrorClears` | `SetError(nil)`로 에러 상태 해제 가능 |
| 6 | `TestFakeStore_ConcurrentAccess` | goroutine 1개가 1000번 `Set`하는 동안 메인 goroutine이 1000번 `Get` — `-race`로 검증 |

## 세부

### 1. `TestFakeStore_EmptyReturnsNotFound`

```go
fs := NewFakeStore()
ts, ok, err := fs.Get(context.Background(), "missing")
require.NoError(t, err)
require.False(t, ok)
require.True(t, ts.IsZero())
```

**왜 중요**: `LastUsedStore` 인터페이스 계약에서 "없음"과 "에러"는 엄격히 구분된다. 컨트롤러는 "없음"을 active로 간주하고 Deployment를 생성하지만, "에러"는 Phase=Error로 처리하고 replicas를 건드리지 않는다. FakeStore가 이 구분을 지키지 않으면 컨트롤러 테스트가 전부 무의미해진다.

### 2. `TestFakeStore_SetGetRoundTrip`

고정된 epoch seconds(`time.Unix(1712839200, 0)`) 사용. `time.Now()`를 쓰지 않는 이유: 비교 시 `Equal`이 타임존이나 monotonic clock bit로 실패할 수 있기 때문.

### 3. `TestFakeStore_Delete`

`Delete` 후 `ok=false` 검증. `Delete`는 컨트롤러 코드에서는 사용하지 않지만, 테스트 중 상태 초기화용으로 쓸 수 있어 FakeStore 공개 API에 포함했다.

### 4. `TestFakeStore_ErrorPropagates`

```go
wantErr := errors.New("boom")
fs.SetError(wantErr)
_, _, err := fs.Get(context.Background(), "k")
require.ErrorIs(t, err, wantErr)
```

`errors.Is` 기반 비교를 써서 향후 wrap해도 깨지지 않도록 한다.

### 5. `TestFakeStore_ErrorClears`

```go
fs.SetError(errors.New("boom"))
fs.SetError(nil) // clear
_, ok, err := fs.Get(context.Background(), "k")
require.NoError(t, err)
require.False(t, ok)
```

**왜 필요**: Reconcile 테스트의 `TestReconcile_StoreErrorPreservesReplicas`는 "처음엔 정상 → 에러 주입 → 결과 확인" 패턴이다. 한 테스트 안에서 에러 상태를 넣었다 뺄 수 있어야 한다. 이 동작이 FakeStore의 공개 계약임을 단위 테스트로 못 박는다.

### 6. `TestFakeStore_ConcurrentAccess`

```go
done := make(chan struct{})
go func() {
    for i := 0; i < 1000; i++ {
        fs.Set("k", time.Now())
    }
    close(done)
}()
for i := 0; i < 1000; i++ {
    _, _, _ = fs.Get(context.Background(), "k")
}
<-done
```

`go test -race`로 실행하면 `sync.RWMutex` 없이 구현했을 때 data race로 실패한다. 어설션 없이 "충돌이 없음"만 확인하는 스모크 테스트.

**왜 필요**: controller-runtime은 여러 reconciler worker로 reconcile을 병렬 실행한다. `LastUsedStore` 구현이 스레드 안전하지 않으면 프로덕션에서 간헐적 panic이나 잘못된 값을 반환할 수 있다. 인터페이스 설명("구현은 반드시 동시성 안전")을 코드로 강제한다.

## 커버 안 하는 것

- **실제 Redis와의 상호작용**: `redis.Nil` 변환, `ParseInt` 실패, 커넥션 에러 등 `RedisStore.Get` 내부 로직은 v1에서 단위 테스트하지 않는다. 이유는 go-redis 내부를 모킹하면 모킹이 실패 원인이 되고, 실제 Redis를 띄우면 CI가 무거워지기 때문. 대신 `LastUsedStore` 인터페이스 레벨에서 컨트롤러가 모든 반환 케이스를 처리하는지 reconcile 테스트로 검증한다.
- **큰 데이터셋 성능**: FakeStore는 테스트용이므로 벤치마크 없음.
