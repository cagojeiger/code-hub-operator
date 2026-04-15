# Spec Documents

`code-hub-operator`의 구현 스펙 요약 문서 모음.

## Index

| 문서 | 핵심 내용 |
|---|---|
| [crd.md](./crd.md) | `CodeHubWorkspace` 스키마, validation, status 필드 |
| [class.md](./class.md) | `CodeHubWorkspaceClass` 클러스터 레벨 기본값 + Workspace 머지 규칙 |
| [api.md](./api.md) | Spoke REST API (`spoke-v1`) 계약 + OpenAPI spec 위치 |
| [controller.md](./controller.md) | reconcile 순서, 에러 처리, requeue 정책 |
| [store.md](./store.md) | `LastUsedStore` 계약, Redis/Fake 구현 |
| [resources.md](./resources.md) | Deployment/Service 생성 규칙과 드리프트 복구 |
| [rbac.md](./rbac.md) | 최소 권한 ClusterRole |
| [configuration.md](./configuration.md) | manager 실행 플래그/환경변수 |

## 원칙

- 코드와 문서가 다르면 코드를 기준으로 문서를 즉시 갱신한다.
- v1 범위 밖 기능은 짧게만 언급한다.
- 값(포트/타임아웃/권한 동사)은 실제 파일과 동일해야 한다.
