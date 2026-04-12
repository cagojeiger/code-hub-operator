# Controller Spec — `CodeHubWorkspaceReconciler`

코드 위치: `internal/controller/codehubworkspace_controller.go`.

## 구조체

```go
type CodeHubWorkspaceReconciler struct {
    client.Client
    Scheme *runtime.Scheme
    Store  store.LastUsedStore
    Clock  Clock // optional; nil이면 real time
}
```

- **`Store`**: `internal/store` 패키지의 `LastUsedStore` 인터페이스. 프로덕션은 `RedisStore`, 테스트는 `FakeStore`.
- **`Clock`**: `time.Now()` 주입점. 테스트에서 `fixedClock`으로 대체.

## Watch 대상

`SetupWithManager`:

```go
ctrl.NewControllerManagedBy(mgr).
    For(&CodeHubWorkspace{}).
    Owns(&appsv1.Deployment{}).
    Owns(&corev1.Service{}).
    Complete(r)
```

- 1차 이벤트: `CodeHubWorkspace` create/update/delete
- 2차 이벤트: owned `Deployment`, `Service` 변경 → 해당 CR로 enqueue
- 주기적 requeue: 성공/에러 모두 `RequeueAfter: 30 * time.Second` (상수 `requeueAfter`)

## Reconcile 전체 순서

파일 라인 기준 흐름:

1. **CR 조회**
   ```go
   cr := &CodeHubWorkspace{}
   if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
       return ctrl.Result{}, client.IgnoreNotFound(err)
   }
   ```
   CR가 이미 지워졌으면 noop 종료. ownerRef 기반 GC가 나머지를 정리한다.

2. **Class 머지 + Spec validation**
   먼저 `applyClassDefaults(ctx, client, cr)`로 `spec.classRef`를 해석해 defaults를 합친다. 이후 `validateForDeployment(cr)` (in `internal/controller/deployment.go`)를 호출한다. validation 실패 시 `Phase=Error`를 남기고 **30초 주기로 requeue**한다. (Class 수정으로 자동 복구 가능하게 유지)

3. **Service 보장**
   `ensureService(ctx, cr)`. 없으면 생성, 있으면 우리가 관리하는 필드(`ports`, `selector`, labels)만 업데이트. `clusterIP` 등 API 서버가 할당하는 필드는 건드리지 않는다.

4. **외부 스토어 조회**
   `lastUsed, found, storeErr := r.Store.Get(ctx, cr.Spec.LastUsedKey)`
   - `storeErr != nil` → 에러 경로 (아래 §에러 처리)
   - 정상이면 `found`/`lastUsed` 값으로 진행

5. **idle 판정**
   ```go
   idleTimeout := time.Duration(cr.Spec.IdleTimeoutSeconds) * time.Second
   isIdle := found && now.Sub(lastUsed) > idleTimeout
   ```
   - `found == false`면 **active로 간주**. 방금 만든 CR이 첫 reconcile에 scale-down 되는 것을 방지한다.

6. **desired replicas 계산**
   | 조건 | desired | phase |
   |---|---|---|
   | `!isIdle` | `maxReplicas` | `Running` |
   | `isIdle && minReplicas == 0` | `0` | `ScaledDown` |
   | `isIdle && minReplicas == 1` | `1` | `Idle` |

7. **Deployment 보장**
   `ensureDeployment(ctx, cr, desired)` — 없으면 생성, 있으면 `replicas`·`template`·`labels` 차이가 있을 때만 `Update`. 반환값은 `(scaleAction, readyReplicas, err)`.

8. **Status 기록**
   `writeSuccessStatus(...)`이 `phase`, `desiredReplicas`, `readyReplicas`, `lastScaleAction`, `observedGeneration`, `lastEvaluatedTime`, `idleSince`, `Conditions`를 채워 `r.Status().Update(ctx, cr)` 호출.

9. **Requeue**: `ctrl.Result{RequeueAfter: 30 * time.Second}` 반환.

## 결정 규칙 요약

| 상황 | replicas | phase | lastScaleAction | 조건 |
|---|---|---|---|---|
| 초기 생성, last_used 없음 | `maxReplicas` (1) | `Running` | `ScaleToOne` | `Ready=F(desired>0이지만 아직 ready=0)`, `ExternalStoreReachable=T` |
| last_used 최근 | `maxReplicas` | `Running` | 변경 없으면 `NoChange` | `ExternalStoreReachable=T` |
| last_used 오래됨, min=0 | `0` | `ScaledDown` | `ScaleToZero` | `Ready=F`, `ExternalStoreReachable=T` |
| last_used 오래됨, min=1 | `1` | `Idle` | `NoChange` | `ExternalStoreReachable=T` |
| idle→active 복귀 | `0→1` | `Running` | `ScaleToOne` | — |
| store 에러 | **유지** | `Error` | 변경 없음 | `ExternalStoreReachable=F` |
| invalid spec | — | `Error` | — | `Ready=F` |

## 에러 처리

### 1. Store 에러

위치: `Reconcile`의 store 블록 + `writeStoreErrorStatus`.

```go
if storeErr != nil {
    if err := r.ensureDeploymentPreserveReplicas(ctx, cr); err != nil { ... }
    ready := r.observeReady(ctx, cr)
    r.writeStoreErrorStatus(ctx, cr, storeErr, ready, clock)
    return ctrl.Result{RequeueAfter: requeueAfter}, nil
}
```

- **replicas는 절대 바뀌지 않는다**. `ensureDeploymentPreserveReplicas`는 Deployment가 없을 때만 `maxReplicas`로 생성하고, 있으면 그대로 둔다.
- `Phase=Error`, `ExternalStoreReachable=False`, `reason=StoreError`로 표시.
- 에러를 상위로 전파하지 않고 `nil`을 반환해 controller-runtime의 지수 backoff 대신 자신이 정한 30초 주기를 따른다.

### 2. Deployment/Service 생성·갱신 에러

- `Phase=Error`, `Ready=False/reason=ReconcileError/message=<err>` 기록 후 에러를 상위로 반환. controller-runtime의 기본 backoff가 작동한다 (`RequeueAfter: 30s`와 병행).

### 3. Invalid spec

- `writeErrorStatus`로 `Phase=Error` 기록 후 `ctrl.Result{RequeueAfter: 30s}` 반환.

### 4. Status update 자체가 실패

- 모든 status writer는 **best-effort**(`_ = r.Status().Update(ctx, cr)`)다. status write 실패로 reconcile 전체를 실패시키지 않는다. 다음 reconcile에서 다시 써진다.

## 헬퍼 함수

| 함수 | 파일 | 역할 |
|---|---|---|
| `buildDeployment(cr, replicas)` | `deployment.go` | CR에서 Deployment 객체 렌더링. 순수함수, 테스트 대상. |
| `buildService(cr)` | `service.go` | CR에서 Service 객체 렌더링. 순수함수. |
| `validateForDeployment(cr)` | `deployment.go` | CRD OpenAPI 위에 추가되는 안전망 (image/port/replicas 검사) |
| `podTemplateEquivalent(a, b)` | `deployment.go` | 우리가 관리하는 필드만 좁게 비교해 무의미한 업데이트를 막는다 |
| `envFromMap(m)` | `deployment.go` | 맵을 **정렬된** `[]EnvVar`로 변환 → reconcile 결정성 확보 |
| `servicePortsEqual`, `selectorsEqual` | `service.go` | Service용 좁은 동등성 비교 |
| `ensureService`, `ensureDeployment`, `ensureDeploymentPreserveReplicas`, `observeReady` | `codehubworkspace_controller.go` | Reconcile 내부 단계 |
| `writeSuccessStatus`, `writeStoreErrorStatus`, `writeErrorStatus` | `codehubworkspace_controller.go` | status 기록 3종 |

## Clock 주입

```go
type Clock interface { Now() time.Time }
type realClock struct{}
func (realClock) Now() time.Time { return time.Now() }
```

- 프로덕션: `r.Clock == nil` → `realClock{}`
- 테스트: `fixedClock{t: ...}` 주입 → 테스트가 `sleep` 없이 "30분 경과"를 시뮬레이션.

## v1에 **없는** 것 (의도적 제외)

- **Finalizer**: 삭제 정리를 `ownerReferences` GC에 위임. Redis 키 정리나 외부 리소스 해제는 v1 범위가 아니다.
- **Admission webhook**: validation은 CRD OpenAPI schema + `validateForDeployment()`로만.
- **Multi-Kind reconciler**: 1 controller = 1 Kind 원칙을 지킨다.
- **In-place resize / HPA 연동 / VPA**: v1beta1 이후 검토.
- **Events 기록**: 스케일 액션(`ScaleToOne`, `ScaleToZero`)과 일반 reconcile 에러를 Kubernetes Event로 기록한다.
