# RBAC Spec

오퍼레이터가 동작하는 데 필요한 최소 권한.

코드 위치: `internal/controller/codehubruntime_controller.go`의 `+kubebuilder:rbac` 마커 + 생성된 manifest `config/rbac/role.yaml`.

## 설계 원칙

- **필요한 동사만**. 예컨대 CR의 spec을 수정할 일이 없으므로 `codehubruntimes` 리소스에는 write도 필요하지만, `/status` 서브리소스에만 쓰는 것은 아니다 (ensureDeployment가 CR 자체를 수정하진 않음에도 controller-runtime 내부 동작을 위해 write가 필요하다).
- **namespaced 리소스지만 ClusterRole 사용**. CR는 여러 네임스페이스에 존재할 수 있고, 오퍼레이터는 전 네임스페이스를 watch하기 때문이다.
- **secrets, configmaps, nodes 등 건드리지 않는다**.

## ClusterRole 규칙

| API group | Resources | Verbs | 이유 |
|---|---|---|---|
| `runtime.project-jelly.io` | `codehubruntimes` | `get;list;watch;create;update;patch;delete` | 주 리소스 watch + 내부 operation |
| `runtime.project-jelly.io` | `codehubruntimes/status` | `get;update;patch` | status 서브리소스 업데이트 |
| `runtime.project-jelly.io` | `codehubruntimes/finalizers` | `update` | v1에는 finalizer 없지만 관례적으로 포함. v1beta1에서 finalizer 도입 시 RBAC 변경 필요 없음. |
| `apps` | `deployments` | `get;list;watch;create;update;patch;delete` | 자식 Deployment 생성·스케일·삭제 |
| `""` (core) | `services` | `get;list;watch;create;update;patch;delete` | 자식 Service 생성·갱신 |
| `""` (core) | `events` | `create;patch` | 향후 `EventRecorder` 도입 대비 (v1은 아직 사용 안 함) |

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
    verbs: ["get","list","watch","create","update","patch","delete"]
  - apiGroups: ["runtime.project-jelly.io"]
    resources: ["codehubruntimes/status"]
    verbs: ["get","update","patch"]
  - apiGroups: ["runtime.project-jelly.io"]
    resources: ["codehubruntimes/finalizers"]
    verbs: ["update"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get","list","watch","create","update","patch","delete"]
  - apiGroups: [""]
    resources: ["services"]
    verbs: ["get","list","watch","create","update","patch","delete"]
  - apiGroups: [""]
    resources: ["events"]
    verbs: ["create","patch"]
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

오퍼레이터는 `--leader-elect`로 실행될 수 있다. Leader election은 `coordination.k8s.io/leases` 리소스를 사용한다. v1 매니페스트에는 해당 권한이 **아직 포함돼 있지 않다** — HA 배포가 필요해지면 다음 규칙을 추가한다:

```yaml
- apiGroups: ["coordination.k8s.io"]
  resources: ["leases"]
  verbs: ["get","list","watch","create","update","patch","delete"]
```

## Pod Security

`config/manager/manager.yaml`의 `securityContext`:

- `runAsNonRoot: true`
- `allowPrivilegeEscalation: false`
- `capabilities.drop: [ALL]`

추가로 read-only root filesystem, seccompProfile RuntimeDefault 등은 v1beta1에 추가 예정.

## v1에 **없는** 것

- Redis 연결 시크릿 (`secrets` 권한): 현재는 CLI flag/env로만 주입. Secret 기반 구성은 필요할 때 추가.
- `coordination.k8s.io/leases`: leader election 실제 배포 시 추가.
- 멀티 테넌트 네임스페이스 격리 (RoleBinding 기반): v1은 ClusterRole 전역 관리만.
