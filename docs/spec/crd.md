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
| Short name | `chws` |
| Scope | `Namespaced` |
| Subresources | `status` |

CRD manifest: `config/crd/bases/codehub.project-jelly.io_codehubworkspaces.yaml`

## Printer Columns

`kubectl get chws`는 다음 컬럼을 출력한다.

| Column | JSONPath | Type |
|---|---|---|
| `Class` | `.spec.classRef` | string |
| `Phase` | `.status.phase` | string |
| `Ready` | `.status.readyReplicas` | integer |
| `Desired` | `.status.desiredReplicas` | integer |
| `Age` | `.metadata.creationTimestamp` | date |

선언 위치: `api/v1alpha1/codehubworkspace_types.go` 의 `+kubebuilder:printcolumn` 마커.

## Spec

필드 위치: `CodeHubWorkspaceSpec` in `api/v1alpha1/codehubworkspace_types.go`.

| 필드 | 타입 | 필수 | Validation | 의미 |
|---|---|---|---|---|
| `classRef` | `string` | no | — | 참조할 `CodeHubWorkspaceClass` 이름 |
| `image` | `string` | no | — | 컨테이너 이미지 (Class에서 상속 가능) |
| `imagePullPolicy` | `string` | no | `Always \| IfNotPresent \| Never` | pull policy (미설정 시 최종 fallback `IfNotPresent`) |
| `servicePort` | `int32` | no | `1–65535` | Service 포트 (Class에서 상속 가능) |
| `containerPort` | `int32` | no | `1–65535` | 컨테이너 포트 (Class에서 상속 가능) |
| `idleTimeoutSeconds` | `int64` | no | `>= 60` | idle 판정 시간 (Class에서 상속 가능) |
| `minReplicas` | `int32` | yes | `0–1` | idle 시 replica 수 |
| `maxReplicas` | `int32` | yes | `1–1` | active 시 replica 수 (v1은 1 고정) |
| `lastUsedKey` | `string` | yes | `minLength=1` | 외부 스토어 key |
| `env` | `map[string]string` | no | — | Pod env vars |
| `resources` | `corev1.ResourceRequirements` | no | — | 리소스 requests/limits (Class에서 상속 가능) |

필수 필드는 `minReplicas`, `maxReplicas`, `lastUsedKey` 3개다.

## Status

필드 위치: `CodeHubWorkspaceStatus` in `api/v1alpha1/codehubworkspace_types.go`.

| 필드 | 타입 | 의미 |
|---|---|---|
| `phase` | `string` | `Running \| Idle \| ScaledDown \| Error` |
| `readyReplicas` | `int32` | Deployment `status.readyReplicas` 관측값 |
| `desiredReplicas` | `int32` | 오퍼레이터가 마지막으로 적용한 replicas |
| `lastScaleAction` | `string` | `ScaleToOne \| ScaleToZero \| NoChange` |
| `observedGeneration` | `int64` | 마지막으로 처리한 `metadata.generation` |
| `lastEvaluatedTime` | `metav1.Time` | 마지막 reconcile 시각 |
| `idleSince` | `*metav1.Time` | idle 전환 시각 (`lastUsed + idleTimeout`) |
| `resolvedClass` | `string` | 마지막으로 성공적으로 머지된 Class 이름 |
| `conditions` | `[]metav1.Condition` | 표준 condition 목록 |

### Condition 타입 (v1)

| Type | True 의미 | False 의미 |
|---|---|---|
| `Ready` | `readyReplicas >= desiredReplicas && desired > 0` | desired=0, 아직 준비 중, 또는 reconcile 에러 |
| `ExternalStoreReachable` | 직전 reconcile에서 `Store.Get` 성공 | store 에러 발생 |
| `ClassResolved` | `classRef`를 정상 해석하고 defaults 머지 성공 | class 미존재/RBAC/API 에러 |

## 샘플 CR

샘플 파일:
- `config/samples/codehub_v1alpha1_codehubworkspaceclass.yaml`
- `config/samples/codehub_v1alpha1_codehubworkspace.yaml`

`CodeHubWorkspace` 예시:

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

## v1 제약

- `maxReplicas`는 v1에서 **항상 1**.
- `minReplicas`는 0 또는 1.
- `minReplicas > maxReplicas` 같은 논리 제약은 controller의 `validateForDeployment()`가 추가 검증한다.
- `last_used_at` 원본 값은 status에 저장하지 않고 외부 스토어(Redis)에만 둔다.
