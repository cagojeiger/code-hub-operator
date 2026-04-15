# Spoke API Spec — `spoke-v1`

REST 계약: hub orchestrator (또는 테스트 클라이언트)가 spoke의 워크스페이스 lifecycle을 제어할 때 쓰는 인터페이스. 단일 source of truth는 `api/openapi/spoke-v1.yaml`이고, 이 문서는 사람이 빠르게 읽을 수 있는 보조 가이드다.

## 위치

| 파일 | 역할 |
|---|---|
| `api/openapi/spoke-v1.yaml` | 권위 spec (OpenAPI 3.1) |
| `api/openapi/examples/*.json` | 요청·응답 예시 페이로드 |
| `docs/spec/api.md` | 이 문서 (사람용 요약) |

코드와 spec이 어긋나면 spec이 옳다. CI의 contract test가 이를 강제한다.

## 왜 OpenAPI인가

- **언어 중립**: 미래에 hub가 Python/TS/Go 어느 것이어도 같은 spec에서 client 생성 가능
- **단일 파일**: 한 PR로 변경 review 가능
- **mocking 무료**: `prism mock spoke-v1.yaml`로 즉시 mock 서버 가능 → hub 개발이 spoke 구현을 기다리지 않아도 된다
- **검증 도구가 풍부**: Spectral (lint), oapi-codegen (Go 생성), redoc (문서 렌더링), schemathesis (퍼지)

gRPC를 안 쓴 이유: 우리 use case는 단순 CRUD다. streaming · bidirectional이 없다. 그리고 protoc 빌드 체인이 늘어나는 비용을 정당화할 만한 이득이 없다.

## 컴포넌트

```
hub (test 또는 실제 orchestrator)
   │
   │ HTTP/JSON, Bearer auth
   │
   v
+──────────────────────+
│  spoke API server    │  ← 이 spec이 정의하는 것
│  (operator binary    │
│   안의 HTTP layer)   │
+──────────────────────+
   │
   │ Kubernetes client
   │ (in-cluster ServiceAccount)
   │
   v
CodeHubWorkspace CRs
   │
   │ watch
   │
   v
operator reconciler  ← 기존 구현, 손 안 댐
   │
   v
Deployment / Service / Pod
```

spoke API는 K8s를 추상화한 얇은 layer다. 사용자 인증, quota, 스케줄링 결정은 hub가 한다. spoke는 신뢰된 hub의 명령을 받아 CR 생성/조회/삭제만 한다.

## 엔드포인트 5개

| Method | Path | 의미 |
|---|---|---|
| `POST` | `/api/v1/workspaces` | 워크스페이스 생성 (CR create) |
| `GET` | `/api/v1/workspaces` | 리스트 (필터: `?owner=`) |
| `GET` | `/api/v1/workspaces/{id}` | 단건 조회 |
| `DELETE` | `/api/v1/workspaces/{id}` | 삭제 (멱등) |
| `POST` | `/api/v1/workspaces/{id}/wake` | wake 힌트 (annotation bump) |

다섯 개로 의도적으로 작게 유지한다. 더 필요해지면 spec에 추가하기 전에 "정말 필요한가" 한 번 더 묻는다.

## 핵심 모델

### `Workspace`

```jsonc
{
  "id": "alice-personal",          // DNS-1123 label, CR name과 1:1
  "createdAt": "2026-04-15T01:30:00Z",
  "spec": {
    "classRef": "standard",        // CodeHubWorkspaceClass 이름
    "owner": "alice",              // label/검색 키
    "image": null,                 // optional override
    "env": null                    // workspace-only (Class와 머지 안 됨)
  },
  "status": {
    "phase": "Running",            // Pending|Running|Idle|ScaledDown|Error
    "observedGeneration": 1,
    "desiredReplicas": 1,
    "readyReplicas": 1,
    "resolvedClass": "standard",
    "endpoint": {                  // phase=Running일 때만 set
      "scheme": "http",
      "host": "alice-personal.codehub.svc.cluster.local",
      "port": 80
    },
    "lastEvaluatedAt": "2026-04-15T01:30:34Z",
    "message": null
  }
}
```

### `Phase` 상태 머신

```
       create CR
         │
         v
   ┌───────────┐   reconcile + pod ready    ┌───────────┐
   │  Pending  │ ───────────────────────────>│  Running  │
   └─────┬─────┘                              └─────┬─────┘
         │                                          │
         │                                          │ idle (no last_used)
         │                                          v
         │                                    ┌───────────┐
         │                                    │   Idle    │ ← (v1에서는 Running과 합쳐도 무방)
         │                                    └─────┬─────┘
         │                                          │
         │                                          │ idle timeout 경과
         │                                          v
         │                                    ┌───────────┐
         │                                    │ScaledDown │
         │                                    └─────┬─────┘
         │                                          │
         │                                          │ wake
         │                                          v
         │                                    ┌───────────┐
         └────────────reconcile error────────>│   Error   │
                                              └───────────┘
```

phase 전이는 operator가 결정한다. spec은 phase 값을 **노출만** 한다 (수정 API 없음). 클라이언트는 phase 문자열을 enum 외부로 확장하지 말 것.

### `Error`

모든 4xx/5xx 응답이 같은 모양:

```jsonc
{
  "code": "CLASS_NOT_FOUND",       // enum, 안전한 switch 가능
  "message": "...",                // 사람용
  "details": { "...": "..." },     // optional 구조화 컨텍스트
  "requestId": "..."               // trace 상관용
}
```

알려진 코드:
- `INVALID_REQUEST` (400)
- `UNAUTHORIZED` (401)
- `NOT_FOUND` (404)
- `CONFLICT` (409)
- `CLASS_NOT_FOUND` (422)
- `QUOTA_EXCEEDED` (422)
- `INTERNAL` (5xx)

클라이언트는 모르는 `code`를 `INTERNAL`과 동등하게 처리해야 한다 (forward compat).

## 멱등성

| 동작 | 멱등? | 비고 |
|---|---|---|
| `POST /workspaces` (id 명시) | ❌ | 두 번째 호출은 `409 CONFLICT` |
| `POST /workspaces` (id 생략) | ❌ | 매번 새 워크스페이스 |
| `GET /workspaces[/...]` | ✅ | 단순 조회 |
| `DELETE /workspaces/{id}` | ✅ | 없어도 `204` |
| `POST /workspaces/{id}/wake` | ✅ | annotation 타임스탬프만 갱신 |

진짜 멱등 create가 필요하면 v2에서 `Idempotency-Key` 헤더를 추가한다 (Stripe 패턴). v1은 클라이언트가 id를 정해서 conflict를 success-on-replay로 해석하는 식으로 우회.

## 인증

- `Authorization: Bearer <token>` 모든 엔드포인트 요구
- token = 단일 shared secret, 클러스터 `Secret`에 저장, spoke binary가 부팅 시 파일에서 로드
- spoke는 **end-user 인증을 하지 않는다** — hub만 신뢰
- TLS는 v1에서는 cluster mesh (Istio) 또는 in-cluster service-to-service에 의존. mTLS는 필요하면 v1.x에서 추가 가능

## 버전 정책

- URL path version: `/api/v1/` (현재) → `/api/v2/` (breaking change 시)
- spec 자체 SemVer는 `info.version` (현재 `1.0.0`)
- v1 안에서는 **additive only**: 새 optional 필드, 새 endpoint, 새 enum 값 OK
- 다음은 모두 v2가 필요한 변경이다:
  - 필드 제거 / 이름 변경
  - 타입 narrow (예: string → enum, int32 → int16)
  - 응답 status code 변경
  - required → optional 또는 그 반대 (드물게)

클라이언트가 안전하려면:
- 알려지지 않은 응답 필드는 무시 (forward-compat)
- 알려지지 않은 enum 값은 `Error.code` 기준으로 `INTERNAL`로 간주

## 호환 도구

| 도구 | 목적 |
|---|---|
| [Spectral](https://github.com/stoplightio/spectral) | spec lint (CI) |
| [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) | Go 타입/server interface 생성 |
| [Prism](https://github.com/stoplightio/prism) | mock server (`prism mock spoke-v1.yaml`) |
| [Redoc](https://github.com/Redocly/redoc) | 정적 HTML 문서 렌더 |
| [Schemathesis](https://github.com/schemathesis/schemathesis) | property-based 퍼지 테스트 |

이 PR은 spec만 도입한다. 코드 생성, mock, fuzz는 후속 PR에서 필요해지면 추가한다.

## 변경 절차 (이 spec을 수정할 때)

1. `api/openapi/spoke-v1.yaml`을 수정한다.
2. `make lint-spec` (Spectral)로 spec 자체가 valid한지 확인한다.
3. 변경이 additive면 `info.version`의 minor를 올린다 (`1.0.0` → `1.1.0`).
4. 변경이 breaking이면 새 path version을 만든다 (`spoke-v2.yaml`). 두 파일을 한동안 공존시킨다.
5. 같은 PR에서 examples도 갱신한다.
6. 같은 PR에서 이 가이드(`docs/spec/api.md`)의 영향받은 섹션도 갱신한다.

코드(spoke 구현)는 **이 spec을 따라가야 하지** 그 반대가 아니다. 코드가 spec과 어긋나면 contract test가 fail해야 한다. 그게 spec-first의 핵심이다.

## 다음 단계

이 PR은 **spec만** 머지한다. 후속 PR:

1. `feat(api): lint spec in CI` — Spectral 또는 redocly cli, ~30s
2. `feat(spoke-api): implement spoke server against spoke-v1` — 핸들러, 인증 미들웨어, manifest, contract test
3. `feat(operator): watch wake annotation for immediate reconcile` — `/wake` 엔드포인트가 실제로 효과를 갖도록
