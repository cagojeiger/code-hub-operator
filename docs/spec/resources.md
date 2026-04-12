# Owned Resources Spec — Deployment & Service

`CodeHubWorkspace` 하나는 정확히 **Deployment 1개**와 **Service 1개**를 소유한다. 둘 다 CR와 같은 네임스페이스에 생성되고 이름은 CR 이름과 동일하다.

## 라벨 규약

파일: `internal/controller/labels.go`

상수:

```go
labelName      = "app.kubernetes.io/name"       // "codehubworkspace"
labelInstance  = "app.kubernetes.io/instance"   // CR name
labelManagedBy = "app.kubernetes.io/managed-by" // "code-hub-operator"
```

두 가지 라벨 세트:

| 함수 | 포함 | 용도 |
|---|---|---|
| `podLabels(cr)` | `name`, `instance` | **Selector**에 들어가는 값. Deployment.Spec.Selector와 Service.Spec.Selector, 그리고 Pod template labels. |
| `objectLabels(cr)` | 위 + `managed-by` | Deployment/Service `metadata.labels`. |

**왜 분리**: Deployment.Spec.Selector는 **immutable**이므로 한 번 정해지면 바꿀 수 없다. `managed-by`처럼 운영 편의 라벨을 selector에 넣으면 나중에 라벨 체계를 바꿀 수 없게 된다. 그래서 selector 라벨은 최소 세트로 고정한다.

## Deployment

빌더: `buildDeployment(cr *CodeHubWorkspace, replicas int32) *appsv1.Deployment` in `internal/controller/deployment.go`.

### 구조

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: <cr.Name>
  namespace: <cr.Namespace>
  labels:
    app.kubernetes.io/name: codehubworkspace
    app.kubernetes.io/instance: <cr.Name>
    app.kubernetes.io/managed-by: code-hub-operator
  ownerReferences:
    - apiVersion: codehub.project-jelly.io/v1alpha1
      kind: CodeHubWorkspace
      name: <cr.Name>
      uid: <cr.UID>
      controller: true
      blockOwnerDeletion: true
spec:
  replicas: <computed by reconciler>   # 0 or 1
  selector:
    matchLabels:
      app.kubernetes.io/name: codehubworkspace
      app.kubernetes.io/instance: <cr.Name>
  template:
    metadata:
      labels:
        app.kubernetes.io/name: codehubworkspace
        app.kubernetes.io/instance: <cr.Name>
    spec:
      containers:
        - name: runtime
          image: <cr.Spec.Image>
          imagePullPolicy: <cr.Spec.ImagePullPolicy or IfNotPresent>
          ports:
            - name: http
              containerPort: <cr.Spec.ContainerPort>
              protocol: TCP
          env:
            - name: <sorted keys from cr.Spec.Env>
              value: ...
          resources: <cr.Spec.Resources or empty>
```

### 필드별 메모

- **`replicas`**: 빌더는 호출자가 넘긴 값을 그대로 쓴다. CR의 `minReplicas`/`maxReplicas`를 직접 읽지 않음. 이렇게 한 이유는 reconciler가 idle 판정 결과를 기반으로 계산한 값을 그대로 넣기 위함이다. 순수함수로 유지.
- **컨테이너 이름**: `runtime` 상수. 이름이 바뀌면 Deployment는 Pod를 재생성하므로 상수로 고정.
- **`env` 순서**: `envFromMap()`가 키를 `sort.Strings`로 정렬한다. 맵 순회 순서가 비결정적이면 reconcile마다 spec diff가 생겨 무의미한 update가 발생하기 때문이다.
- **`ownerReferences`**: `controllerutil.SetControllerReference(cr, dep, r.Scheme)`로 설정. 이 덕분에 CR 삭제 시 Deployment도 자동 GC되며 v1에서 finalizer를 사용하지 않는다.

### Update 로직

`ensureDeployment` (in `codehubworkspace_controller.go`):

1. Deployment가 없으면 → `buildDeployment` + `Create`
2. 있으면 → 다음 2가지 중 하나라도 다르면 `Update`, 아니면 노터치
   - `replicas` 값이 desired와 다름
   - `podTemplateEquivalent()`가 false (아래 비교 기준)

### `podTemplateEquivalent()` 비교 기준

의도적으로 **좁은 비교**:

- 컨테이너 개수
- 각 컨테이너의 `Image`, `ImagePullPolicy`
- `ContainerPort`, `Protocol`
- `Env` 슬라이스 (이미 정렬돼 있으므로 index 비교로 충분)
- `Resources` (`requests`/`limits`)

**비교하지 않는 필드** (API 서버가 기본값을 채우는 것들):

- `terminationMessagePath`, `terminationMessagePolicy`
- `ImagePullPolicy` default fill
- `RestartPolicy`, `DNSPolicy`, `SchedulerName` 등 `PodSpec` defaults

"관리하는 필드만 비교한다" 원칙을 지켜 **update 충돌과 churn을 최소화**한다.

## Service

빌더: `buildService(cr *CodeHubWorkspace) *corev1.Service` in `internal/controller/service.go`.

### 구조

```yaml
apiVersion: v1
kind: Service
metadata:
  name: <cr.Name>
  namespace: <cr.Namespace>
  labels: <objectLabels(cr)>
  ownerReferences: [ CR ]
spec:
  type: ClusterIP
  selector: <podLabels(cr)>
  ports:
    - name: http
      port: <cr.Spec.ServicePort>
      targetPort: <cr.Spec.ContainerPort>   # intstr.FromInt32
      protocol: TCP
```

### Update 로직

`ensureService`:

1. 없으면 생성
2. 있으면 `servicePortsEqual` + `selectorsEqual` 비교 후 하나라도 다르면 in-place `Update`
3. 동일하면 노터치
4. `clusterIP`, `clusterIPs` 등 API 서버가 할당한 필드는 읽지도 쓰지도 않는다 (기존 객체를 그대로 유지)

### 비교 기준

`servicePortsEqual` (좁은 비교):

- 포트 개수
- 각 포트의 `Port`, `TargetPort`, `Protocol`

`selectorsEqual`: map 동등성.

## v1에 **없는** 것

- **Ingress 자동 생성**: 외부 노출은 사용자 책임.
- **Headless Service / ExternalName**: `ClusterIP` 고정.
- **Multi-port**: v1은 포트 1개만. 멀티포트가 필요해지면 CRD에 `ports[]` 필드를 추가하고 v1beta1로 올린다.
- **PodDisruptionBudget**: replicas가 항상 0/1이라 의미가 제한적.
- **HorizontalPodAutoscaler 연동**: v1 스케일링은 오퍼레이터가 전적으로 관리. HPA와 공존하면 결정 주체가 충돌하기 때문.
