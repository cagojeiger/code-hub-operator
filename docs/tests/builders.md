# Builder Tests

파일:
- `internal/controller/deployment_test.go`
- `internal/controller/service_test.go`

## 대상

`buildDeployment(cr, replicas)`, `buildService(cr)`, `validateForDeployment(cr)`, `podTemplateEquivalent(a, b)`, `servicePortsEqual(a, b)`, `selectorsEqual(a, b)`. 모두 **순수 함수**이므로 빠르게 모든 경계 케이스를 돌릴 수 있다.

이 계층을 따로 테스트하는 이유: reconcile 테스트가 builder 결과의 모든 필드를 재검증하면 테스트 길이가 폭발한다. 빌더는 빌더대로, reconcile은 "빌더를 호출하고 결과를 적용한다"까지만 보장한다.

## Deployment 테스트 목록

| # | 함수 | 무엇을 보장 |
|---|---|---|
| 1 | `TestBuildDeployment_BasicShape` | 이름, 네임스페이스, replicas, selector, pod template labels, 컨테이너 이미지·포트·pull policy |
| 2 | `TestBuildDeployment_HonorsExplicitPullPolicy` | CR에 `Always` 지정 시 반영 (기본값 `IfNotPresent` 덮어쓰기) |
| 3 | `TestBuildDeployment_EnvIsDeterministic` | map을 두 번 빌드해도 `[]EnvVar`가 동일 (키 정렬) |
| 4 | `TestBuildDeployment_ReplicasRespected` | `buildDeployment(cr, 0)`·`buildDeployment(cr, 1)` replicas 전달 |
| 5 | `TestBuildDeployment_ResourcesApplied` | Spec.Resources가 컨테이너 requests/limits에 들어감 |
| 6 | `TestValidateForDeployment` | 테이블 기반 5케이스: ok, 이미지 없음, container port 0, service port 0, max < min |
| 7 | `TestPodTemplateEquivalent` | 같은 CR에서 replicas만 다를 때 equivalent, 이미지/env/resources 변경 시 not equivalent |

## 세부

### `TestBuildDeployment_BasicShape`

검증 항목:

```go
require.Equal(t, "demo", dep.Name)
require.Equal(t, "ns", dep.Namespace)
require.NotNil(t, dep.Spec.Replicas)
require.Equal(t, int32(1), *dep.Spec.Replicas)

wantSelector := map[string]string{
    "app.kubernetes.io/name":     "codehubworkspace",
    "app.kubernetes.io/instance": "demo",
}
require.Equal(t, wantSelector, dep.Spec.Selector.MatchLabels)
require.Equal(t, wantSelector, dep.Spec.Template.Labels)

require.Equal(t, "code-hub-operator", dep.ObjectMeta.Labels["app.kubernetes.io/managed-by"])

c := dep.Spec.Template.Spec.Containers[0]
require.Equal(t, "runtime", c.Name)
require.Equal(t, "ghcr.io/x/y:1.0", c.Image)
require.Equal(t, corev1.PullIfNotPresent, c.ImagePullPolicy)
require.Equal(t, int32(8080), c.Ports[0].ContainerPort)
```

**왜 이것들**: 이 값들은 Deployment.Spec.Selector와 Pod.Template.Labels가 **일치해야 한다**는 쿠버네티스 불변 조건을 지키는지 확인한다. 여기가 깨지면 Pod가 매칭되지 않아 Deployment가 무한히 Pod를 만들어낸다.

또한 `managed-by` 라벨은 `podLabels`(selector)에는 **들어가지 않고** `objectLabels`(metadata)에만 들어가야 함을 검증한다. Selector에 들어가면 라벨 체계를 바꿀 때 Deployment를 삭제·재생성해야 하기 때문이다.

### `TestBuildDeployment_EnvIsDeterministic`

```go
cr.Spec.Env = map[string]string{"Z": "1", "A": "2", "M": "3"}

a := buildDeployment(cr, 1).Spec.Template.Spec.Containers[0].Env
b := buildDeployment(cr, 1).Spec.Template.Spec.Containers[0].Env
require.Equal(t, a, b)

require.Equal(t, []corev1.EnvVar{
    {Name: "A", Value: "2"},
    {Name: "M", Value: "3"},
    {Name: "Z", Value: "1"},
}, a)
```

**왜 중요**: Go 맵 순회는 비결정적이다. 빌더가 순회 순서를 그대로 쓰면 reconcile마다 `Env` 슬라이스 순서가 달라져 `podTemplateEquivalent` 비교가 매번 false → Deployment가 계속 update → etcd write 폭주. 이 버그는 프로덕션에서 하루 지나야 티가 나기 때문에 단위 테스트로 못 박는다.

### `TestValidateForDeployment`

테이블 기반:

```go
cases := []struct {
    name    string
    cr      *runtimev1alpha1.CodeHubWorkspace
    wantErr bool
}{
    {"ok",              crWithAll(),        false},
    {"missing image",   crWithoutImage(),   true},
    {"bad container port", crWithoutCP(),  true},
    {"bad service port",   crWithoutSP(),  true},
    {"max < min",           crWithMaxLtMin(), true},
}
```

**왜 필요**: CRD OpenAPI validation은 클러스터에서만 작동한다. 단위 테스트에서 CR 객체 직접 생성할 때는 거치지 않으므로, Go 레벨 안전망이 필요하다. 또한 reconciler 앞단에 이 함수를 두어 잘못된 데이터로 Deployment를 만드는 것을 막는다.

### `TestPodTemplateEquivalent`

```go
a := buildDeployment(base, 1)
b := buildDeployment(base, 0) // replicas만 다름
require.True(t, podTemplateEquivalent(a, b))

changed := base.DeepCopy()
changed.Spec.Image = "i:2"
c := buildDeployment(changed, 1)
require.False(t, podTemplateEquivalent(a, c))

envChanged := base.DeepCopy()
envChanged.Spec.Env = map[string]string{"A": "2"}
d := buildDeployment(envChanged, 1)
require.False(t, podTemplateEquivalent(a, d))
```

**왜 중요**: `ensureDeployment`의 idempotency는 이 함수의 정확성에 달려 있다. `replicas`만 다른 경우는 "같음"으로 보고(스케일은 별도 결정), 이미지나 env 변경은 "다름"으로 보아야 한다. 두 케이스가 뒤바뀌면 업데이트가 누락되거나 반대로 매 reconcile마다 불필요한 update가 발생한다.

## Service 테스트 목록

| # | 함수 | 무엇을 보장 |
|---|---|---|
| 1 | `TestBuildService_Shape` | 이름, 네임스페이스, ClusterIP 타입, selector, Port/TargetPort, Protocol |
| 2 | `TestBuildService_SelectorMatchesDeployment` | Service selector == Deployment Pod template labels |
| 3 | `TestServicePortsEqual` | 동일 포트 true, Port 다름 false, Protocol 다름 false |
| 4 | `TestSelectorsEqual` | 같음 true, 값 다름 false, 키 개수 다름 false |

### `TestBuildService_SelectorMatchesDeployment`

```go
svc := buildService(cr)
dep := buildDeployment(cr, 1)

require.Equal(t, svc.Spec.Selector, dep.Spec.Selector.MatchLabels)
require.Equal(t, svc.Spec.Selector, dep.Spec.Template.Labels)
```

**왜 중요**: Service는 selector로 Pod를 찾는다. selector와 Pod 라벨이 어긋나면 엔드포인트가 생성되지 않아 트래픽이 Pod에 도달하지 못한다. 이 테스트는 "빌더 하나를 고쳤는데 다른 빌더가 안 고쳐졌다"라는 리그레션을 잡는다.

### `TestServicePortsEqual`

```go
a := []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP}}
b := []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP}}
require.True(t, servicePortsEqual(a, b))

c := []corev1.ServicePort{{Port: 81, Protocol: corev1.ProtocolTCP}}
require.False(t, servicePortsEqual(a, c))

d := []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolUDP}}
require.False(t, servicePortsEqual(a, d))
```

짧지만 `ensureService` 멱등성의 근거. 빌더 결과와 기존 Service를 비교할 때 "다르다"를 정확히 반환해야 불필요한 update를 막는다.

## 커버 안 하는 것

- **`corev1.PullPolicy`의 유효성 enum 체크**: CRD OpenAPI schema가 처리. 빌더는 CR이 유효하다는 가정 하에 동작.
- **Resources의 내부 필드 조합**: `corev1.ResourceRequirements`는 k8s 표준 타입이므로 우리가 테스트할 필요 없음.
- **Pod template의 `terminationMessagePath` 등 k8s defaults**: API 서버가 채우는 필드는 빌더가 만들지 않고, `podTemplateEquivalent`도 무시한다. 그렇지 않으면 fake client vs real API 결과가 달라 테스트가 거짓 양성으로 실패한다.
