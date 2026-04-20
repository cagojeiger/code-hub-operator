# code-hub-operator

A Kubernetes operator that manages single-instance runtimes and scales them
to zero when idle, based on a `last_used_at` timestamp kept in an external
store (Redis).

## CRD

- Group / Version: `codehub.project-jelly.io/v1alpha1`
- Kind: `CodeHubWorkspace` (short: `chws`)
- Scope: Namespaced

Each `CodeHubWorkspace` owns one `Deployment` + one `Service`. The controller
scales the Deployment to `maxReplicas` (1) when the runtime has been used
within `idleTimeoutSeconds`, and to `minReplicas` (0) otherwise. A missing
last-used value is treated as active so freshly-created runtimes are not
scaled down on their first reconcile.

See `config/samples/codehub_v1alpha1_codehubworkspace.yaml` for an example CR.
Class defaults are defined by `CodeHubWorkspaceClass` and referenced via `spec.classRef`.

## Build / test

```bash
make fmt vet test   # unit tests (fake client, no envtest binary required)
make build          # build ./cmd/manager
make run            # run the manager against the current kubeconfig
```

Tests use `sigs.k8s.io/controller-runtime/pkg/client/fake` and an in-memory
`FakeStore`, so `go test ./...` runs without Kind or etcd.

## Deploy

```bash
make install                                                       # install CRD
make docker-build docker-push IMG=<your-registry>/code-hub-operator:dev
make deploy                                                        # apply rbac + manager
kubectl apply -f config/samples/codehub_v1alpha1_codehubworkspace.yaml
```

Runtime state:

```bash
kubectl get chws -A
kubectl describe chws demo-workspace -n demo
```

## How idle detection works

The operator does NOT record request timestamps itself. Any component that
observes "meaningful use" (the app, gateway, or a sidecar) writes the
current Unix epoch seconds to Redis at the key named in `spec.lastUsedKey`:

```bash
redis-cli SET workspace:demo:demo-workspace:last_used_at $(date +%s)
```

Every reconcile (every 30s by default) the operator reads that key, compares
`now - lastUsed` to `spec.idleTimeoutSeconds`, and scales accordingly.

On store errors the operator preserves the current replica count and records
`ExternalStoreReachable=False` on the CR — it never scales on uncertain data.
