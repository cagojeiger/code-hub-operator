# Configuration Spec — Manager Runtime

오퍼레이터 매니저(`cmd/main.go`)의 기동 옵션.

## CLI 플래그

| Flag | Default | 환경변수 fallback | 설명 |
|---|---|---|---|
| `--metrics-bind-address` | `:8080` | — | controller-runtime metrics 엔드포인트 |
| `--health-probe-bind-address` | `:8081` | — | `/healthz`, `/readyz` 엔드포인트 |
| `--leader-elect` | `false` | — | 리더 선출 활성화 (HA 배포 시 필수) |
| `--redis-addr` | `redis:6379` | `REDIS_ADDR` | Redis 주소 `host:port` |
| `--redis-password` | `""` | `REDIS_PASSWORD` | Redis 비밀번호 |
| `--redis-db` | `0` | — | Redis DB 인덱스 |

또한 controller-runtime의 `zap` 로그 옵션이 `opts.BindFlags(flag.CommandLine)`로 등록돼 `--zap-log-level`, `--zap-encoder` 같은 플래그를 쓸 수 있다.

### 우선순위

`envOr` 함수(`cmd/main.go` 하단):

```go
func envOr(key, def string) string {
    if v := os.Getenv(key); v != "" { return v }
    return def
}
```

적용 규칙:

1. CLI 플래그가 명시되면 플래그 값
2. 아니면 환경변수
3. 둘 다 없으면 기본값

## Manager 설정

```go
mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
    Scheme:                 scheme,
    Metrics:                metricsserver.Options{BindAddress: metricsAddr},
    HealthProbeBindAddress: probeAddr,
    LeaderElection:         enableLeaderElection,
    LeaderElectionID:       "code-hub-operator.runtime.project-jelly.io",
})
```

- **`Scheme`**: 빌트인 k8s 타입(`clientgoscheme`) + `runtime.project-jelly.io/v1alpha1`
- **Leader election ID**: `code-hub-operator.runtime.project-jelly.io` (고정)
- `ctrl.GetConfigOrDie()`: `KUBECONFIG` env / `~/.kube/config` / in-cluster config 순으로 찾는다

## Scheme 등록

파일: `cmd/main.go`의 `init()` 블록.

```go
utilruntime.Must(clientgoscheme.AddToScheme(scheme))
utilruntime.Must(runtimev1alpha1.AddToScheme(scheme))
```

- `clientgoscheme`: `apps/v1`, `core/v1` 등 k8s 빌트인 타입
- `runtimev1alpha1`: 우리 CRD

## Reconciler 주입

```go
redisClient := redis.NewClient(&redis.Options{
    Addr:     redisAddr,
    Password: redisPassword,
    DB:       redisDB,
})
lastUsedStore := store.NewRedisStore(redisClient)

if err := (&controller.CodeHubRuntimeReconciler{
    Client: mgr.GetClient(),
    Scheme: mgr.GetScheme(),
    Store:  lastUsedStore,
}).SetupWithManager(mgr); err != nil { ... }
```

- **`Clock`은 주입하지 않는다** → `nil` → `realClock{}` 사용
- **Redis 클라이언트는 단 하나** 생성해 공유. `go-redis`는 내부 커넥션 풀을 관리한다.

## Health Probes

```go
mgr.AddHealthzCheck("healthz", healthz.Ping)
mgr.AddReadyzCheck("readyz", healthz.Ping)
```

- 둘 다 단순 `Ping` — 프로세스가 살아 있으면 OK
- 실제 헬스 체크 로직(Redis 연결 확인 등)은 v1에 없음. v1beta1 후보.

## Graceful Shutdown

```go
if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil { ... }
```

`SetupSignalHandler`가 SIGTERM/SIGINT를 받아 컨텍스트를 취소하고, `mgr.Start`는 진행 중인 reconcile을 마친 후 종료한다.

## 배포 시 설정 예시

### 로컬 개발

```bash
REDIS_ADDR=localhost:6379 go run ./cmd
```

### 클러스터 배포

`config/manager/manager.yaml`의 Deployment에 다음 env/args가 들어 있다:

```yaml
args:
  - --leader-elect
env:
  - name: REDIS_ADDR
    value: "redis.code-hub-operator-system.svc:6379"
ports:
  - name: metrics
    containerPort: 8080
  - name: health
    containerPort: 8081
livenessProbe:
  httpGet: { path: /healthz, port: health }
readinessProbe:
  httpGet: { path: /readyz, port: health }
```

## 운영 권고값

| 항목 | 권고 | 이유 |
|---|---|---|
| `idleTimeoutSeconds` | 1800 (30분) | 대부분의 개발환경 유휴 패턴에 안전 |
| Redis `dial timeout` | 기본값 (5s) | 오퍼레이터가 requeue로 재시도하므로 길게 둘 필요 없음 |
| Metrics scrape 주기 | 30s 이상 | reconcile 주기와 맞춤 |
| Leader election lease duration | controller-runtime 기본값 | HA 배포 시만 의미 있음 |

## v1에 **없는** 것

- **ConfigMap/Secret 기반 구성**: Redis 비밀번호는 env 또는 flag로만. K8s Secret 마운트는 v1beta1 후보.
- **Webhook 포트**: validation/mutating webhook 없음.
- **Profiling 엔드포인트** (`pprof`): 필요하면 controller-runtime `Metrics.ExtraHandlers`로 추가 가능.
- **OpenTelemetry trace export**: v1 범위 밖.
