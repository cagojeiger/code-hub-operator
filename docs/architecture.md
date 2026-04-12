# Architecture — `code-hub-operator`

v1alpha1 기준 핵심 구조만 간단히 정리한다.

## 1) 컴포넌트 관계

```text
        (write last_used_at)
[App/Gateway] ----------------------> [Redis]
      |                                  ^
      | HTTP                             | GET last_used_at
      v                                  |
   [Service] ---> [Pod(s)] <--- [Deployment] <--- reconcile --- [Operator]
                        ^                                ^
                        |                                |
                        +------------ ownerRef ----------+
                                   [CodeHubWorkspace CR]
```

## 2) 운영 규칙

- 오퍼레이터는 `last_used_at`을 **읽기만** 한다.
- 키가 없으면 active로 보고 `maxReplicas(=1)`를 유지한다.
- idle이면 `minReplicas(0 또는 1)`로 내린다.
- store 에러 시 replica는 보존하고 상태만 `Error`로 기록한다.

## 3) Reconcile 흐름

```text
[Get CR]
  -> not found: 종료
  -> validate 실패: status=Error, 재큐 없음
  -> ensure Service
  -> Store.Get(lastUsedKey)
       -> error: Deployment 보존 경로 + status(Error)
       -> ok: idle 판정 -> desired 계산 -> ensure Deployment
  -> status 업데이트
  -> RequeueAfter 30s
```

## 4) 소유 리소스

- `CodeHubWorkspace` 1개당 `Deployment` 1개 + `Service` 1개
- 이름/네임스페이스는 CR과 동일
- `ownerReferences`로 GC 연동

## 5) 패키지 의존

```text
cmd/main.go
  -> internal/controller
  -> internal/store
  -> api/v1alpha1

internal/controller
  -> internal/store (interface)
  -> api/v1alpha1

internal/store
  -> redis/go-redis (Redis 구현체만)
```
