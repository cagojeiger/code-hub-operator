# Envtest 테스트

**Tier 2** — real `kube-apiserver` + `etcd` 바이너리를 로컬 프로세스로 띄워 reconcile 을 검증한다.

대상 파일: `internal/controller/envtest_test.go` (build tag `envtest`)

## envtest 이란

`sigs.k8s.io/controller-runtime/pkg/envtest` 는 kubebuilder 팀이 배포하는 `kube-apiserver` + `etcd` 바이너리 번들을 프로세스 내부에서 기동한다. kubelet·노드·스케줄러·CNI 는 **없다** — 그래서 Pod 는 영원히 `Pending` 이고, Deployment 의 `status.readyReplicas` 는 0 으로 머무른다. 대신 API 서버·watch·admission·CRD 검증 같은 "컨트롤 플레인 측면" 은 전부 진짜다.

| 구성요소 | Unit (fake) | **Envtest** | E2E (kind) |
|---|---|---|---|
| kube-apiserver | ❌ | ✅ 실제 바이너리 | ✅ |
| etcd | ❌ | ✅ 실제 바이너리 | ✅ |
| CRD OpenAPI validation | ❌ | ✅ | ✅ |
| admission / webhook | ❌ | ✅ | ✅ |
| controller-runtime watch / cache | ❌ | ✅ | ✅ |
| owner-ref 설정 | ❌ | ✅ | ✅ |
| owner-ref 기반 실 GC cascade | ❌ | ❌ (kube-controller-manager 없음) | ✅ |
| kubelet / Pod 실행 | ❌ | ❌ | ✅ |
| 이미지 pull / 네트워크 | ❌ | ❌ | ✅ |

## 이 tier 가 보장하는 것

Unit tier 가 우회하는 "진짜 API server 동작" 을 검증한다.

### 1. CRD 스키마 validation 이 실제로 먹히는가

`TestEnvtest_CRDRejectsShortIdleTimeout`
- `idleTimeoutSeconds: 30` 로 CR 을 생성 시도
- 기대: `idleTimeoutSeconds in body should be greater than or equal to 60` 에러
- 왜 필요한가: fake client 는 OpenAPI validator 를 돌리지 않아 이 에러를 못 잡는다. 이번 PR 실제 개발 중에 kind 에서 처음 발견됐음.

### 2. Reconcile 이 실제 API 서버에 Deployment/Service 를 만든다

`TestEnvtest_ReconcileCreatesChildren`
- 네임스페이스 + CR 생성 → Reconcile 1회 직접 호출
- 기대: `apps/v1 Deployment` 와 `v1 Service` 가 각각 CR 이름으로 생성됨
- OwnerReference 가 CR 을 가리킴
- Unit 에서도 같은 것을 보지만, 여기는 진짜 API 서버가 JSON schema 를 거친 것만 저장한다는 추가 보장이 있다.

### 3. Owner reference 메타데이터가 정확히 박힌다

`TestEnvtest_ChildrenHaveControllerOwnerRef`
- CR 생성 → 1회 Reconcile → 자식 리소스 ownerRef 검사
- 기대:
  - `Controller=true` (cascade GC 대상 표시)
  - `BlockOwnerDeletion=true` (finalizer 동작 보장)
  - `Kind=CodeHubRuntime`, Name이 CR 이름과 일치
- 왜 필요한가: fake client 는 `SetControllerReference` 호출을 흉내만 내고, 실제 API server 가 받아들이는 정확한 형태인지 검증하지 않는다.
- **주의**: envtest 는 kube-controller-manager 를 띄우지 않으므로, 실제 cascade GC (CR 삭제 → 자식 삭제) 는 여기서 관찰할 수 없다. 그 경로는 e2e tier 에서 검증한다. 여기서는 "GC 가 발동할 수 있는 *메타데이터가* 제대로 써졌는가" 까지만 본다.

### 4. Store unreachable 경로가 replicas 를 보존한다

`TestEnvtest_StoreErrorPreservesReplicas`
- `errStore` 를 주입 (항상 error 반환) → CR 생성 → Reconcile
- 기대:
  - Deployment 는 `maxReplicas` 로 생성됨 (없으면 생성, 있으면 replicas 건드리지 않음)
  - CR status 의 `ExternalStoreReachable` condition 이 False
- 왜 필요한가: 이 경로는 fake client 로도 테스트 가능하지만, envtest 에서 실제 status subresource update 가 정말로 반영되는지도 같이 본다.

### 5. Status subresource 가 분리돼 동작하는가

`TestEnvtest_StatusSubresource`
- `Status().Update(cr)` 호출이 `spec` 을 건드리지 않는지 확인
- fake 는 이 구분을 흉내 내지만 실제 API server 는 `/status` 엔드포인트를 분리 처리한다.

## 어떻게 동작하나

`internal/controller/envtest_test.go` 의 `TestMain` 이 한 번만 환경을 기동한다:

```go
//go:build envtest

var testCfg *rest.Config

func TestMain(m *testing.M) {
    env := &envtest.Environment{
        CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
        ErrorIfCRDPathMissing: true,
    }
    cfg, err := env.Start()
    if err != nil { panic(err) }
    testCfg = cfg

    _ = runtimev1alpha1.AddToScheme(scheme.Scheme)

    code := m.Run()
    _ = env.Stop()
    os.Exit(code)
}
```

각 테스트는 `client.New(testCfg, ...)` 로 client 를 만들고, Reconciler 는 직접 생성해서 `Reconcile()` 을 호출한다. `Owns(&Deployment{})` watch 가 필요한 테스트만 `ctrl.NewManager` 로 매니저를 띄운다.

## 빌드 태그

envtest 파일은 전부 `//go:build envtest` 로 시작한다. 일반 `go test ./...` 에서는 컴파일 대상에 포함되지 않아, envtest 바이너리가 없는 개발자 머신에서도 Unit tier 는 정상 동작한다.

```bash
# Unit 만
go test ./...

# Envtest 포함
go test -tags=envtest ./internal/controller/...
```

## 바이너리 설치

`setup-envtest` 이 `kube-apiserver` 와 `etcd` 바이너리를 `~/.local/share/kubebuilder-envtest/` 에 내려받는다. 한 번만 실행하면 된다.

```bash
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
export KUBEBUILDER_ASSETS=$(setup-envtest use 1.30.0 -p path)
```

`Makefile` 타겟:

```bash
make envtest-setup   # setup-envtest 설치 + 바이너리 prefetch
make test-envtest    # KUBEBUILDER_ASSETS export 후 go test -tags=envtest 실행
```

## 왜 `//go:build envtest` 가드를 쓰나

1. **로컬 DX**: envtest 바이너리가 없어도 `go test ./...` 가 깨지지 않는다.
2. **CI 단계 분리**: Unit job 과 Envtest job 을 분리해서 Unit 실패 시 envtest prefetch 단계를 건너뛸 수 있다.
3. **테스트 의도 명시**: 파일 맨 위 태그를 보고 "이건 envtest 필요" 라고 즉시 알아챌 수 있다.

## 제약

- **Pod 는 Ready 안 됨**. kubelet 없음. `wait-for-ready` 같은 assertion 을 여기 쓰지 말 것.
- **Deployment controller 없음**. `status.replicas`, `status.readyReplicas` 는 자동 업데이트되지 않는다. Deployment status 를 검증하려면 테스트 안에서 직접 `Update` 해야 한다.
- **네트워크 없음**. Service ClusterIP 는 할당되지만 실제 라우팅은 없다.
- **시간**: 실제 `time.Now()` 를 쓴다. 타임아웃 기반 polling 은 `require.Eventually` / `EventuallyWithT` 로 감싸고 deadline 5초 이내.

## 추가 가이드

새 envtest 를 추가할 때:

1. 파일 맨 위에 `//go:build envtest` 를 반드시 둔다.
2. 테스트 이름은 `TestEnvtest_` 프리픽스를 붙여 grep 하기 쉽게 한다.
3. 네임스페이스는 테스트마다 유니크 suffix 를 쓰고, `t.Cleanup` 에서 `client.Delete(ns)` 한다. (envtest 는 `testCfg` 를 TestMain 레벨로 공유)
4. Reconcile 은 가능하면 직접 호출 (`rec.Reconcile(ctx, req)`) — manager 기동보다 빠르고 결정적이다. watch 동작 자체를 테스트할 때만 `ctrl.NewManager` 를 쓴다.
