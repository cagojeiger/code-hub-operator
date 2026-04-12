# CodeHubWorkspaceClass Spec

클러스터 레벨 기본값을 담는 cluster-scoped CRD. 플랫폼 관리자가 한 번 정의해두면 사용자 `CodeHubWorkspace`가 `spec.classRef`로 참조해서 image/ports/idle/resources 를 상속받는다.

## 왜 두 개로 나눴나

단일 CRD 구조에서는 모든 필드를 사용자가 자기 Workspace YAML에 직접 써야 했다. 문제:

1. **플랫폼 정책 강제 불가**: 사용자가 기본 이미지나 리소스 한도를 마음대로 바꿔도 막을 수단이 없다.
2. **기본값 복붙**: 새 Workspace마다 20줄짜리 boilerplate를 다시 쓰는 비용.
3. **운영자 재배포 없는 업데이트 불가**: 기본값을 바꾸려면 코드 배포가 필요했다.

`CodeHubWorkspaceClass`를 도입해 이 3가지를 모두 해결한다. Tekton의 `Pipeline`+`PipelineRun`, Knative의 `Configuration`+`Revision`, 또는 Kubernetes 코어의 `StorageClass`+`PVC` / `IngressClass`+`Ingress` 패턴과 같은 "template + instance" 분리다.

## GVK

| 속성 | 값 |
|---|---|
| Group | `codehub.project-jelly.io` |
| Version | `v1alpha1` |
| Kind | `CodeHubWorkspaceClass` |
| ShortName | `chwsc` |
| Scope | **Cluster** |

## Spec 필드 (모두 optional)

| 필드 | 타입 | 설명 |
|---|---|---|
| `image` | string | 기본 컨테이너 이미지 |
| `imagePullPolicy` | enum | `Always` / `IfNotPresent` / `Never` |
| `servicePort` | int32 | Service가 노출할 기본 포트 (1-65535) |
| `containerPort` | int32 | 컨테이너가 리스닝할 기본 포트 (1-65535) |
| `idleTimeoutSeconds` | int64 | idle 판정 시간 (최소 60) |
| `resources` | ResourceRequirements | 기본 requests/limits |

모두 optional이라 "이미지만 주는 Class", "리소스만 주는 Class" 같은 부분 defaults도 가능하다.

## 머지 규칙

리컨사일 맨 앞에서 `applyClassDefaults(ctx, client, workspace)`가 실행된다:

```
effective.Image              = ws.Image              ?: class.Image
effective.ImagePullPolicy    = ws.ImagePullPolicy    ?: class.ImagePullPolicy ?: IfNotPresent
effective.ServicePort        = ws.ServicePort        ?: class.ServicePort
effective.ContainerPort      = ws.ContainerPort      ?: class.ContainerPort
effective.IdleTimeoutSeconds = ws.IdleTimeoutSeconds ?: class.IdleTimeoutSeconds
effective.Resources          = ws.Resources          ?: class.Resources (deep-copied)
```

핵심 원칙:

- **Workspace 값이 Class 값을 이긴다**. 사용자가 자기 Workspace에 명시적으로 적은 값이면 Class defaults를 무시한다.
- **Class 값이 하드코딩 기본값을 이긴다**. `ImagePullPolicy`만 최종 fallback `IfNotPresent`를 갖는다 (기존 CRD default였던 걸 옮김).
- **Env는 절대 머지하지 않는다**. `Env`는 인스턴스-특이 값이라 Class가 주면 혼란을 만든다. Workspace에만 존재한다.
- **mutation은 deep copy 위에서만 한다**. 리컨사일러는 `fetched.DeepCopy()`를 받아 merge하고, 원본 spec은 etcd에 절대 다시 쓰지 않는다. `Status().Update`는 status subresource라서 spec 변경을 어차피 무시하지만, 명시적으로 격리한다.

## 상태 반영

성공 경로:
- `status.resolvedClass` 에 실제 머지된 Class 이름을 기록
- `ClassResolved=True` 컨디션 (reason: `Merged`)

실패 경로 (Class missing / RBAC 거부 / transient API 오류):
- `status.phase = Error`
- `ClassResolved=False` 컨디션 (reason: `ClassNotFound` / `ClassAccessError` / `ClassFetchError`)
- `ReconcileError` 타입의 Warning 이벤트 발행
- 자식 리소스(Deployment/Service) 생성하지 않고 다음 requeue까지 대기

## 샘플

`config/samples/codehub_v1alpha1_codehubworkspaceclass.yaml`:

```yaml
apiVersion: codehub.project-jelly.io/v1alpha1
kind: CodeHubWorkspaceClass
metadata:
  name: standard
spec:
  image: ghcr.io/acme/runtime-demo:0.1.0
  imagePullPolicy: IfNotPresent
  servicePort: 80
  containerPort: 8080
  idleTimeoutSeconds: 1800
  resources:
    requests: { cpu: "250m", memory: "256Mi" }
    limits:   { cpu: "500m", memory: "512Mi" }
```

`config/samples/codehub_v1alpha1_codehubworkspace.yaml`:

```yaml
apiVersion: codehub.project-jelly.io/v1alpha1
kind: CodeHubWorkspace
metadata:
  name: demo-workspace
  namespace: demo
spec:
  classRef: standard
  minReplicas: 0
  maxReplicas: 1
  lastUsedKey: "workspace:demo:demo-workspace:last_used_at"
  env:
    APP_MODE: workspace
```

플랫폼 관리자가 Class를 한 번 apply 하고 나면, 사용자 Workspace는 12줄 안쪽으로 줄어든다.

## RBAC

리컨사일러는 Class를 읽기만 하므로 `codehubworkspaceclasses` 리소스에 `get;list;watch`만 갖는다. 쓰기 권한은 없다 (Class는 플랫폼 관리자가 kubectl/gitops로 직접 관리).

## 제한 사항

- **Class 업데이트가 기존 Workspace에 즉시 반영되지 않을 수 있음**. 리컨사일 주기(30s)마다 다시 읽으므로 보통 즉시 반영되지만, Class의 watch가 Workspace reconcile을 트리거하지는 않는다. 급한 상황에선 Workspace annotation bump로 강제 재조정.
- **멀티 Class 체인은 지원 안 함**. 한 Workspace가 한 Class만 참조한다. "base class + overlay class" 같은 상속 구조는 v1 범위 밖.
- **Class 삭제 전후 정책 없음**. Class를 지워도 이미 리컨사일된 Workspace는 머지 결과(= Deployment spec)를 그대로 유지한다. 다음 reconcile에서 Class가 없어졌음을 감지하면 `ClassResolved=False`로 Error phase 전환.
- **필드 캡 없음**. Class는 default만 제공하고 "이 한계를 넘지 말라" 같은 LimitRange 스타일 enforcement는 v1 범위 밖. 필요해지면 validating webhook이나 별도 policy CRD로 추가.

## 연관 문서

- [crd.md](./crd.md) — `CodeHubWorkspace` 스키마
- [controller.md](./controller.md) — reconcile 플로우, 머지는 Step 1.5에 해당
- [rbac.md](./rbac.md) — ClusterRole 규칙
