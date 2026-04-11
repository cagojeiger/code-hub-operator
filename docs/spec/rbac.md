# RBAC Spec

오퍼레이터가 동작하는 데 필요한 최소 권한.

코드 위치: `internal/controller/codehubruntime_controller.go`의 `+kubebuilder:rbac` 마커 + 생성된 manifest `config/rbac/role.yaml`.

## 설계 원칙

- **필요한 동사만**. CR 본문(`codehubruntimes`)은 읽기만 하고, 쓰기는 `/status`에만 한다.
- **namespaced 리소스지만 ClusterRole 사용**. CR는 여러 네임스페이스에 존재할 수 있고, 오퍼레이터는 전 네임스페이스를 watch하기 때문이다.
- **secrets, configmaps, nodes 등 건드리지 않는다**.

## ClusterRole 규칙

| API group | Resources | Verbs | 이유 |
|---|---|---|---|
| `runtime.project-jelly.io` | `codehubruntimes` | `get;list;watch` | 주 리소스 watch/조회 |
| `runtime.project-jelly.io` | `codehubruntimes/status` | `get;update` | status 서브리소스 업데이트 |
| `apps` | `deployments` | `get;list;watch;create;update` | 자식 Deployment 생성·스케일 |
| `""` (core) | `events` | `create;patch` | 스케일/에러 이벤트 기록 |
| `""` (core) | `services` | `get;list;watch;create;update` | 자식 Service 생성·갱신 |
| `coordination.k8s.io` | `leases` | `get;list;watch;create;update;patch;delete` | leader election lock |

## 매니페스트

### ClusterRole
`config/rbac/role.yaml`:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: code-hub-operator-manager-role
rules:
  - apiGroups: ["runtime.project-jelly.io"]
    resources: ["codehubruntimes"]
    verbs: ["get","list","watch"]
  - apiGroups: ["runtime.project-jelly.io"]
    resources: ["codehubruntimes/status"]
    verbs: ["get","update"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get","list","watch","create","update"]
  - apiGroups: [""]
    resources: ["events"]
    verbs: ["create","patch"]
  - apiGroups: [""]
    resources: ["services"]
    verbs: ["get","list","watch","create","update"]
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["get","list","watch","create","update","patch","delete"]
```

### ServiceAccount
`config/rbac/service_account.yaml`:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: code-hub-operator-controller-manager
  namespace: code-hub-operator-system
```

### ClusterRoleBinding
`config/rbac/role_binding.yaml`:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: code-hub-operator-manager-rolebinding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: code-hub-operator-manager-role
subjects:
  - kind: ServiceAccount
    name: code-hub-operator-controller-manager
    namespace: code-hub-operator-system
```

## Leader Election

오퍼레이터는 `--leader-elect`로 실행되며, leader election lock으로 `coordination.k8s.io/leases`를 사용한다. 따라서 현재 v1 매니페스트에 leases 권한이 포함되어 있다.

## Pod Security

`config/manager/manager.yaml`의 `securityContext`:

- `runAsNonRoot: true`
- `allowPrivilegeEscalation: false`
- `capabilities.drop: [ALL]`

추가로 read-only root filesystem, seccompProfile RuntimeDefault 등은 v1beta1에 추가 예정.

## v1에 **없는** 것

- Redis 연결 시크릿 (`secrets` 권한): 현재는 CLI flag/env로만 주입. Secret 기반 구성은 필요할 때 추가.
- `codehubruntimes/finalizers` 권한: v1은 finalizer를 쓰지 않으므로 미포함.
- 멀티 테넌트 네임스페이스 격리 (RoleBinding 기반): v1은 ClusterRole 전역 관리만.
