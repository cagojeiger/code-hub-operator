# CRD Spec — `CodeHubWorkspace`

코드 위치: `api/v1alpha1/codehubworkspace_types.go`, `api/v1alpha1/groupversion_info.go`.

## 식별자

| 항목 | 값 |
|---|---|
| API group | `codehub.project-jelly.io` |
| API version | `v1alpha1` |
| Kind | `CodeHubWorkspace` |
| List kind | `CodeHubWorkspaceList` |
| Plural | `codehubworkspaces` |
| Short name | `chr` |
| Scope | `Namespaced` |
| Subresources | `status` |

CRD manifest: `config/crd/bases/codehub.project-jelly.io_codehubworkspaces.yaml`

## Printer Columns

`kubectl get chr`는 다음 컬럼을 출력한다.

| Column | JSONPath | Type |
|---|---|---|
| `Phase` | `.status.phase` | string |
| `Ready` | `.status.readyReplicas` | integer |
| `Desired` | `.status.desiredReplicas` | integer |
| `Age` | `.metadata.creationTimestamp` | date |

선언 위치: `api/v1alpha1/codehubworkspace_types.go` 의 `+kubebuilder:printcolumn` 마커.

## Spec

필드 위치: `CodeHubWorkspaceSpec` in `api/v1alpha1/codehubworkspace_types.go`.

| 필드 | 타입 | 필수 | 기본값 | Validation | 의미 |
|---|---|---|---|---|---|
| `image` | `string` | yes | — | `minLength=1` | 컨테이너 이미지 |
| `imagePullPolicy` | `string` | no | `IfNotPresent` | `Always \| IfNotPresent \| Never` | Pod pull policy |
| `servicePort` | `int32` | yes | — | `1–65535` | Service가 노출할 포트 |
| `containerPort` | `int32` | yes | — | `1–65535` | 컨테이너 listen 포트 |
| `idleTimeoutSeconds` | `int64` | yes | — | `>= 60` | 이 시간 이상 무활동 시 idle 판정 |
| `minReplicas` | `int32` | yes | — | `0–1` | idle 시 replica 수 |
| `maxReplicas` | `int32` | yes | — | `1–1` | active 시 replica 수 (v1은 1 고정) |
| `lastUsedKey` | `string` | yes | — | `minLength=1` | 외부 스토어에서 읽을 key |
| `env` | `map[string]string` | no | `nil` | — | Pod env vars (키 순서는 자동 정렬) |
| `resources` | `corev1.ResourceRequirements` | no | `nil` | — | Pod requests/limits |

**v1 제약**:

- `maxReplicas`는 v1에서 **항상 1**. validation marker로 강제된다.
- `minReplicas`는 0 또는 1만 허용. 보통 0을 쓴다.
- `minReplicas > maxReplicas`는 CRD 레벨 validation을 통과하더라도 `validateForDeployment()` (`internal/controller/deployment.go`)가 다시 거부한다.

## Status

필드 위치: `CodeHubWorkspaceStatus` in `api/v1alpha1/codehubworkspace_types.go`.

| 필드 | 타입 | 의미 |
|---|---|---|
| `phase` | `string` | `Running \| Idle \| ScaledDown \| Error` |
| `readyReplicas` | `int32` | Deployment `status.readyReplicas` 관측값 |
| `desiredReplicas` | `int32` | 오퍼레이터가 마지막으로 적용한 replicas |
| `lastScaleAction` | `string` | `ScaleToOne \| ScaleToZero \| NoChange` |
| `observedGeneration` | `int64` | 이 reconcile이 처리한 `metadata.generation` |
| `lastEvaluatedTime` | `metav1.Time` | 마지막 reconcile 시각 |
| `idleSince` | `*metav1.Time` | idle로 전환된 시각 (`lastUsed + idleTimeout`). active면 `nil` |
| `conditions` | `[]metav1.Condition` | 표준 condition 리스트 |

### 상태를 저장하지 않는 것

- **`last_used_at` 원본 값**: 쓰면 초당 여러 번 status write가 발생할 수 있으므로 절대 저장하지 않는다. 외부 스토어(Redis)에만 있다.
- **요청 로그, 히스토리, 레이턴시 시계열**: 오퍼레이터 책임이 아니다.

### Phase 상수

`api/v1alpha1/codehubworkspace_types.go` 내 상수:

```go
PhaseRunning    = "Running"
PhaseIdle       = "Idle"
PhaseScaledDown = "ScaledDown"
PhaseError      = "Error"
```

### LastScaleAction 상수

```go
ScaleActionScaleToOne  = "ScaleToOne"
ScaleActionScaleToZero = "ScaleToZero"
ScaleActionNoChange    = "NoChange"
```

### Condition 타입 (v1 사용 중)

| Type | True 의미 | False 의미 |
|---|---|---|
| `Ready` | `readyReplicas >= desiredReplicas && desired > 0` | desired=0, reconcile 에러, 또는 아직 준비 중 |
| `ExternalStoreReachable` | 직전 reconcile에서 store.Get 성공 | store 에러 발생 |

## 샘플 CR

`config/samples/runtime_v1alpha1_codehubworkspace.yaml`:

```yaml
apiVersion: codehub.project-jelly.io/v1alpha1
kind: CodeHubWorkspace
metadata:
  name: demo-runtime
  namespace: demo
spec:
  image: ghcr.io/acme/runtime-demo:0.1.0
  imagePullPolicy: IfNotPresent
  servicePort: 80
  containerPort: 8080
  idleTimeoutSeconds: 1800
  minReplicas: 0
  maxReplicas: 1
  lastUsedKey: "runtime:demo:demo-runtime:last_used_at"
  env:
    APP_MODE: runtime
  resources:
    requests: { cpu: "250m", memory: "256Mi" }
    limits:   { cpu: "500m", memory: "512Mi" }
```

## DeepCopy

`api/v1alpha1/zz_generated.deepcopy.go`는 controller-gen이 생성할 것과 동등한 손으로 쓴 DeepCopy 구현이다. `CodeHubWorkspace`, `CodeHubWorkspaceList`, `CodeHubWorkspaceSpec`, `CodeHubWorkspaceStatus` 각각에 대해 `DeepCopy`, `DeepCopyInto`, `DeepCopyObject`(top-level 타입만)를 제공한다.

## 버저닝 계획

- v1alpha1은 자유롭게 필드를 추가/삭제할 수 있다.
- v1beta1 승격 시점에 CRD가 안정화되며, 이후는 Kubernetes API 변경 가이드라인에 따라 **backwards-compatible** 변경만 허용한다.
