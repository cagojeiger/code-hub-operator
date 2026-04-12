# Reconcile Tests

파일: `internal/controller/codehubworkspace_controller_test.go`

## 목적

리컨실러의 상태 전이와 스케일링 의사결정이 회귀 없이 유지되는지 검증한다.

## 테스트 구조

```text
[newTestEnv]
  |- fake client (status subresource 포함)
  |- FakeStore
  |- fixedClock
  \- CodeHubWorkspaceReconciler
```

## 핵심 시나리오 매트릭스

| 그룹 | 테스트 | 보장 |
|---|---|---|
| 생성/기본 | `CreatesDeploymentAndService` | 자식 리소스 생성 + ownerRef + Running |
| 활성/유휴 | `ActiveWithRecentUsage` | 최근 사용 시 replicas=1 |
| 활성/유휴 | `IdleScalesDownToZero` | idle 시 replicas=0 + ScaledDown |
| 활성/유휴 | `IdleWithMinReplicasOneReportsIdle` | min=1이면 replicas=1 + Idle |
| 복귀 | `IdleThenResumed` | 0→1 복귀 + `ScaleToOne` |
| 결측 키 | `NoLastUsedTreatedAsActive` | 키 없음은 active 처리 |
| 외부 장애 | `StoreErrorPreservesReplicas` | store 에러 시 replicas 보존 |
| 외부 장애 | `StoreErrorOnFirstReconcileStillCreatesDeployment` | 첫 reconcile store 에러여도 배포 생성 |
| 입력 오류 | `InvalidSpecStopsWithoutRequeueLoop` | invalid spec 재큐 루프 방지 |
| 멱등성 | `IdempotentUpdate` | 무변경 reconcile에서 RV 불변 |
| spec 반영 | `ImageUpdatePropagates` | 이미지 변경 전파 |
| spec 반영 | `ResourcesUpdatePropagates` | resources 변경 전파 |
| 드리프트 복구 | `ServiceMetadataDriftIsReconciled` | service labels/ownerRef 복구 |
| 시간 제어 | `ClockAdvanceTriggersIdle` | sleep 없이 idle 전환 검증 |

## 원칙

- `time.Sleep` 대신 `fixedClock.advance()` 사용
- 실패 메시지는 불변 조건을 직접 설명
- fake client 한계(실제 kube defaulting/컨트롤러 동작)는 envtest/e2e에서 검증
