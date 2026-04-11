# Spec Documents

`code-hub-operator` v1alpha1의 카테고리별 상세 명세. 전체 개요는 `docs/architecture.md`, 실행 계획서는 `/root/.claude/plans/buzzing-yawning-truffle.md`, 실제 코드는 본 저장소를 참고한다.

## Index

| 문서 | 내용 |
|---|---|
| [crd.md](./crd.md) | `CodeHubRuntime` CRD 정의 — Group/Version/Kind, Spec/Status 필드, validation, printer columns |
| [controller.md](./controller.md) | Reconcile 동작 순서, 결정 규칙, 에러 경로, 조건(Conditions), requeue 정책 |
| [store.md](./store.md) | `LastUsedStore` 인터페이스, Redis 구현, key/value 포맷, 에러 시맨틱 |
| [resources.md](./resources.md) | 오퍼레이터가 생성·관리하는 Deployment / Service의 구조, 라벨, ownerReference |
| [rbac.md](./rbac.md) | 오퍼레이터 ServiceAccount에 필요한 ClusterRole 규칙 |
| [configuration.md](./configuration.md) | 매니저 entrypoint 플래그·환경변수·leader election·프로브 |

## 스펙 문서 작성 원칙

- 모든 필드/동작은 **코드의 실제 위치**(`file:line`)와 함께 기술한다.
- 의도적으로 제외한 것(예: finalizer)은 "v1에 없음"으로 명시한다. 독자가 누락과 의도적 제외를 구별할 수 있어야 한다.
- 숫자(timeout 기본값, 포트, 주기)는 본문에 직접 쓰고, 코드 변경 시 문서도 동기화한다.
