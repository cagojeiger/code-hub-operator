# Reconcile Tests

파일: `internal/controller/codehubruntime_controller_test.go`

## 공통 하네스

### `testEnv`

모든 테스트는 `newTestEnv(t, objs...)`로 시작한다.

```go
type testEnv struct {
    t      *testing.T
    client client.Client
    store  *store.FakeStore
    clock  *fixedClock
    rec    *CodeHubRuntimeReconciler
}
```

구성 요소:

- **`client`**: `sigs.k8s.io/controller-runtime/pkg/client/fake`의 빌더로 생성. `runtimev1alpha1` 타입과 `clientgoscheme` 등록, status subresource 명시:
  ```go
  fake.NewClientBuilder().
      WithScheme(scheme).
      WithObjects(objs...).
      WithStatusSubresource(&runtimev1alpha1.CodeHubRuntime{}).
      Build()
  ```
- **`store`**: `store.NewFakeStore()` — 모든 테스트에서 새 인스턴스
- **`clock`**: `fixedClock{t: 2026-04-11 12:00 UTC}` — 결정적 기준 시각
- **`rec`**: `CodeHubRuntimeReconciler{Client, Scheme, Store, Clock}` — 위 3개를 주입

### 헬퍼 메서드

| 메서드 | 용도 |
|---|---|
| `env.reconcile(name, ns)` | Reconcile 1회 호출, 에러 없으면 Result 반환 |
| `env.reconcileExpectErr(name, ns)` | 에러 허용 버전 (NotFound 등 검증용) |
| `env.getCR(name, ns)` | 최신 CR 재조회 |
| `env.getDeployment(name, ns)` | Deployment 재조회 (없으면 실패) |
| `env.getDeploymentMaybe(name, ns)` | Deployment 재조회 (없어도 OK) |
| `env.getService(name, ns)` | Service 재조회 |

### `sampleRuntime()`

모든 테스트의 기본 CR:

```go
&runtimev1alpha1.CodeHubRuntime{
    ObjectMeta: metav1.ObjectMeta{
        Name: "demo", Namespace: "default", Generation: 1,
    },
    Spec: runtimev1alpha1.CodeHubRuntimeSpec{
        Image:              "ghcr.io/acme/demo:0.1.0",
        ImagePullPolicy:    corev1.PullIfNotPresent,
        ServicePort:        80,
        ContainerPort:      8080,
        IdleTimeoutSeconds: 1800, // 30m
        MinReplicas:        0,
        MaxReplicas:        1,
        LastUsedKey:        "runtime:default:demo:last_used_at",
    },
}
```

## 테스트 매트릭스

| # | 함수 | 시나리오 | 검증 포인트 |
|---|---|---|---|
| 1 | `TestReconcile_CreatesDeploymentAndService` | 초기 생성, last_used 없음 | Deployment+Service 생성, ownerRef 2개, Phase=Running, DesiredReplicas=1, ObservedGeneration, `ExternalStoreReachable=True` |
| 2 | `TestReconcile_ActiveWithRecentUsage` | last_used = now-10m | replicas=1, Phase=Running, IdleSince=nil |
| 3 | `TestReconcile_IdleScalesDownToZero` | last_used = now-1h, min=0 | replicas=0, Phase=ScaledDown, LastScaleAction=ScaleToZero, IdleSince = lastUsed + 30m |
| 4 | `TestReconcile_IdleWithMinReplicasOneReportsIdle` | last_used = now-2h, min=max=1 | replicas=1 유지, Phase=**Idle** (not Running, not ScaledDown) |
| 5 | `TestReconcile_IdleThenResumed` | idle → last_used=now | 0→1 복귀, Phase=Running, LastScaleAction=ScaleToOne, IdleSince=nil |
| 6 | `TestReconcile_NoLastUsedTreatedAsActive` | 스토어 비어있음 | replicas=1, Phase=Running (fresh 런타임 보호) |
| 7 | `TestReconcile_StoreErrorPreservesReplicas` | 생성 후 store 에러 | replicas 유지, Phase=Error, `ExternalStoreReachable=False/reason=StoreError` |
| 8 | `TestReconcile_StoreErrorOnFirstReconcileStillCreatesDeployment` | 처음부터 store 에러 | Deployment는 MaxReplicas로 생성, Phase=Error |
| 9 | `TestReconcile_NotFoundIsNoop` | CR 존재 안 함 | 에러 없이 `ctrl.Result{}` 반환 |
| 10 | `TestReconcile_InvalidSpecStopsWithoutRequeueLoop` | Image="" | Phase=Error, `ctrl.Result{}` (RequeueAfter 없음) |
| 11 | `TestReconcile_IdempotentUpdate` | 2회 연속 reconcile | Deployment/Service ResourceVersion 불변 |
| 12 | `TestReconcile_ImageUpdatePropagates` | CR 이미지 변경 | Deployment template 이미지 반영 |
| 13 | `TestReconcile_ClockAdvanceTriggersIdle` | 시계 +31m | 자동 idle 전환 |

---

## 시나리오 상세

### 1. `TestReconcile_CreatesDeploymentAndService`

첫 reconcile이 모든 자식 리소스를 만들고 올바른 status를 기록하는지 확인한다.

**검증**:
```go
require.Equal(t, int32(1), *dep.Spec.Replicas)
require.Equal(t, cr.Spec.Image, dep.Spec.Template.Spec.Containers[0].Image)
require.Len(t, dep.OwnerReferences, 1)
require.Equal(t, "CodeHubRuntime", dep.OwnerReferences[0].Kind)

require.Equal(t, int32(80), svc.Spec.Ports[0].Port)
require.Equal(t, int32(8080), svc.Spec.Ports[0].TargetPort.IntVal)
require.Len(t, svc.OwnerReferences, 1)

require.Equal(t, runtimev1alpha1.PhaseRunning, got.Status.Phase)
require.Equal(t, int32(1), got.Status.DesiredReplicas)

storeCond := findCondition(got.Status.Conditions, runtimev1alpha1.ConditionExternalStoreReachable)
require.Equal(t, metav1.ConditionTrue, storeCond.Status)
```

**왜 중요**: ownerReferences가 빠지면 CR 삭제 시 자식 리소스가 고아로 남는다. v1은 finalizer를 쓰지 않으므로 ownerRef GC가 유일한 cleanup 경로다.

### 2. `TestReconcile_ActiveWithRecentUsage`

```go
env.store.Set(cr.Spec.LastUsedKey, env.clock.t.Add(-10*time.Minute))
env.reconcile(cr.Name, cr.Namespace)

require.Equal(t, int32(1), *env.getDeployment(...).Spec.Replicas)
require.Equal(t, runtimev1alpha1.PhaseRunning, got.Status.Phase)
require.Nil(t, got.Status.IdleSince)
```

30분 timeout 안쪽이므로 active 판정. `IdleSince`가 nil임을 명시 확인해서 "active 상태에서 IdleSince가 남아 있는 버그"를 잡는다.

### 3. `TestReconcile_IdleScalesDownToZero`

핵심 scale-down 경로.

```go
lastUsed := env.clock.t.Add(-1 * time.Hour)
env.store.Set(cr.Spec.LastUsedKey, lastUsed)
env.reconcile(cr.Name, cr.Namespace)

require.Equal(t, int32(0), *dep.Spec.Replicas)
require.Equal(t, runtimev1alpha1.PhaseScaledDown, got.Status.Phase)
require.Equal(t, runtimev1alpha1.ScaleActionScaleToZero, got.Status.LastScaleAction)
require.NotNil(t, got.Status.IdleSince)
require.True(t, got.Status.IdleSince.Time.Equal(lastUsed.Add(30*time.Minute)))
```

`IdleSince = lastUsed + idleTimeout` 계산도 검증. 이 값이 잘못되면 운영자가 "언제부터 idle이었나"를 잘못 이해한다.

### 4. `TestReconcile_IdleWithMinReplicasOneReportsIdle`

특수 케이스: `minReplicas=1, maxReplicas=1`이면 idle이어도 replicas는 1 유지한다. 하지만 Phase는 `Running`이 아닌 `Idle`로 표시해야 한다.

```go
cr.Spec.MinReplicas = 1
cr.Spec.MaxReplicas = 1
env.store.Set(cr.Spec.LastUsedKey, env.clock.t.Add(-2*time.Hour))
env.reconcile(cr.Name, cr.Namespace)

require.Equal(t, int32(1), *dep.Spec.Replicas)
require.Equal(t, runtimev1alpha1.PhaseIdle, got.Status.Phase)
```

이 케이스가 없으면 "replicas는 그대로인데 운영자가 사용 여부를 알 방법이 없다"는 문제가 생긴다. Phase 세 가지(`Running`/`Idle`/`ScaledDown`)의 구분선을 테스트로 못 박는다.

### 5. `TestReconcile_IdleThenResumed`

2단계:

```go
// Step 1: idle → 0
env.store.Set(cr.Spec.LastUsedKey, env.clock.t.Add(-1*time.Hour))
env.reconcile(...)
require.Equal(t, int32(0), *dep.Spec.Replicas)

// Step 2: 사용 재개 → 1
env.store.Set(cr.Spec.LastUsedKey, env.clock.t)
env.reconcile(...)
require.Equal(t, int32(1), *dep.Spec.Replicas)
require.Equal(t, runtimev1alpha1.ScaleActionScaleToOne, got.Status.LastScaleAction)
require.Nil(t, got.Status.IdleSince)
```

**왜 중요**: scale-up 경로는 scale-down과 대칭이지만 "IdleSince가 nil로 초기화되는지"를 별도로 확인해야 한다. 이 필드를 지우지 않으면 UI가 "idle인데 replicas=1"이라는 모순 상태를 보여준다.

### 6. `TestReconcile_NoLastUsedTreatedAsActive`

스토어에 키 자체가 없을 때. **가장 중요한 안전 테스트** 중 하나다.

```go
// store에 아무것도 안 넣음
env.reconcile(cr.Name, cr.Namespace)
require.Equal(t, int32(1), *dep.Spec.Replicas,
    "missing last-used must be treated as active to avoid scaling down fresh runtimes")
```

**왜**: `found=false`를 idle로 잘못 해석하면 새로 만든 CR이 즉시 scale-down 된다. 이 버그는 UX를 완전히 망가뜨리고 사용자가 "왜 안 올라오냐"로 디버깅에 시간을 쓰게 만든다. 어설션 메시지까지 붙여 실패 원인을 명확히 했다.

### 7. `TestReconcile_StoreErrorPreservesReplicas`

가장 중요한 에러 처리 테스트.

```go
// 1. 먼저 정상 생성
env.reconcile(cr.Name, cr.Namespace)
require.Equal(t, int32(1), *env.getDeployment(...).Spec.Replicas)

// 2. store 고장
env.store.SetError(errors.New("boom"))
env.reconcile(cr.Name, cr.Namespace)

// 3. 검증
dep := env.getDeployment(...)
require.Equal(t, int32(1), *dep.Spec.Replicas,
    "replicas MUST be preserved when the external store is unreachable")

got := env.getCR(...)
require.Equal(t, runtimev1alpha1.PhaseError, got.Status.Phase)

storeCond := findCondition(got.Status.Conditions, runtimev1alpha1.ConditionExternalStoreReachable)
require.Equal(t, metav1.ConditionFalse, storeCond.Status)
require.Equal(t, "StoreError", storeCond.Reason)
```

**왜**: "Redis 일시 장애 = 전체 런타임 scale-down"은 용납할 수 없다. 불확실한 데이터에 기반해 destructive action을 하면 안 된다는 원칙을 코드로 강제한다. 실패 시 어설션 메시지가 의도를 설명하도록 했다.

### 8. `TestReconcile_StoreErrorOnFirstReconcileStillCreatesDeployment`

edge case: 오퍼레이터가 처음 기동할 때부터 Redis가 죽어 있으면?

```go
env.store.SetError(errors.New("boom"))
env.reconcile(cr.Name, cr.Namespace)

dep, ok := env.getDeploymentMaybe(...)
require.True(t, ok, "deployment should be created even when store fails")
require.Equal(t, int32(1), *dep.Spec.Replicas)
require.Equal(t, runtimev1alpha1.PhaseError, got.Status.Phase)
```

**왜**: Deployment가 안 만들어지면 나중에 Redis가 복구돼도 "Redis 복구 + reconcile"만으로 해결 안 되고 사람 손이 필요해진다. 시스템을 수동 개입 없이 복구 가능하게 유지하는 것이 목표. 그래서 `ensureDeploymentPreserveReplicas`는 Deployment 생성은 수행한다.

### 9. `TestReconcile_NotFoundIsNoop`

```go
res, err := env.reconcileExpectErr("missing", "default")
require.NoError(t, err)
require.Equal(t, ctrl.Result{}, res)
```

`client.IgnoreNotFound` 래핑이 제대로 작동하는지. 삭제된 CR에 대한 늦은 이벤트를 에러로 처리하면 controller-runtime이 백오프를 걸고 로그가 노이즈로 가득 찬다.

### 10. `TestReconcile_InvalidSpecStopsWithoutRequeueLoop`

```go
cr.Spec.Image = "" // invalid
env := newTestEnv(t, cr)

res, err := env.reconcileExpectErr(cr.Name, cr.Namespace)
require.NoError(t, err)
require.Equal(t, ctrl.Result{}, res,
    "invalid spec is a user error and should not trigger a tight requeue loop")
```

**왜**: 사용자 실수를 초당 수십 번 재시도해봐야 고쳐지지 않는다. `ctrl.Result{}` (RequeueAfter=0)을 반환하면 이벤트 기반 재시도만 발생한다 = 사용자가 CR을 수정할 때만 다시 시도. 합리적인 백오프 정책이다.

### 11. `TestReconcile_IdempotentUpdate`

```go
env.reconcile(cr.Name, cr.Namespace)
rv1 := env.getDeployment(...).ResourceVersion
svcRv1 := env.getService(...).ResourceVersion

env.reconcile(cr.Name, cr.Namespace) // 변화 없음
require.Equal(t, rv1, env.getDeployment(...).ResourceVersion)
require.Equal(t, svcRv1, env.getService(...).ResourceVersion)
```

**왜 중요**: 멱등성이 깨지면 매 30초마다 Deployment/Service가 업데이트되고, 1000개 CR이 있으면 초당 66번의 불필요한 k8s API write가 발생한다. `podTemplateEquivalent`와 `servicePortsEqual`, `selectorsEqual`의 정확성이 여기서 보장된다.

### 12. `TestReconcile_ImageUpdatePropagates`

```go
env.reconcile(cr.Name, cr.Namespace)

fresh := env.getCR(cr.Name, cr.Namespace)
fresh.Spec.Image = "ghcr.io/acme/demo:0.2.0"
require.NoError(t, env.client.Update(context.Background(), fresh))

env.reconcile(cr.Name, cr.Namespace)
dep := env.getDeployment(cr.Name, cr.Namespace)
require.Equal(t, "ghcr.io/acme/demo:0.2.0", dep.Spec.Template.Spec.Containers[0].Image)
```

idempotent test(11번)와 대칭. 변경이 있으면 **반드시** 반영되어야 한다. 두 테스트가 함께 "변화 있을 때만 update, 변화 없으면 update 안 함"을 증명한다.

### 13. `TestReconcile_ClockAdvanceTriggersIdle`

결정적 타이머 없는 시간 테스트의 대표 예시.

```go
env.store.Set(cr.Spec.LastUsedKey, env.clock.t) // now
env.reconcile(cr.Name, cr.Namespace)
require.Equal(t, int32(1), *env.getDeployment(...).Spec.Replicas)

// 시계를 31분 앞으로
env.clock.advance(31 * time.Minute)
env.reconcile(cr.Name, cr.Namespace)
require.Equal(t, int32(0), *env.getDeployment(...).Spec.Replicas)
```

**왜**: 실제로 30분 기다려서는 테스트할 수 없다. `fixedClock.advance`가 `Clock` 주입 패턴의 가치를 증명하는 핵심 테스트다. 이 테스트가 있기 때문에 다른 테스트들도 `sleep`을 쓰지 않고 시간 의존 로직을 검증할 수 있다.

---

## 커버 안 하는 것 (의도적)

| 시나리오 | 왜 테스트 안 함 |
|---|---|
| 실제 API 서버의 defaulting | fake client 한계. Kind E2E로 이동 |
| webhook admission | v1에 webhook 없음 |
| 동시성 race (여러 reconciler worker) | controller-runtime 자체가 직렬화 보장, FakeStore race test로 충분 |
| CRD validation (OpenAPI schema) | `kubectl apply` 시점에만 작동. `validateForDeployment`로 Go 레벨 안전망 |
| Deployment status 전파 (readyReplicas) | fake client가 Pod 런처를 돌리지 않으므로 `ready=0`이 기본. reconciler 코드는 이 값을 그대로 기록할 뿐 |
| Finalizer 처리 | v1에 없음 |

## 메트릭

현재 `internal/controller/` 패키지 전체 실행 시간은 약 **0.06초** (`go test`). 13개 reconcile 테스트 모두 밀리초 단위.

`go test ./... -race` 기준으로도 전체 프로젝트가 1초 이내에 끝난다. CI에서 매 PR마다 돌려도 병목이 아니다.
