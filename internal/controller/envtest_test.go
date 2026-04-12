//go:build envtest

package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	runtimev1alpha1 "github.com/cagojeiger/code-hub-operator/api/v1alpha1"
	"github.com/cagojeiger/code-hub-operator/internal/store"
)

var (
	testCfg    *rest.Config
	testScheme = runtime.NewScheme()
)

func TestMain(m *testing.M) {
	env := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
		},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := env.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start envtest: %v\n", err)
		os.Exit(1)
	}
	testCfg = cfg

	_ = scheme.AddToScheme(testScheme)
	_ = runtimev1alpha1.AddToScheme(testScheme)

	code := m.Run()

	_ = env.Stop()
	os.Exit(code)
}

// newEnvtestClient builds a client bound to the envtest apiserver.
func newEnvtestClient(t *testing.T) client.Client {
	t.Helper()
	c, err := client.New(testCfg, client.Options{Scheme: testScheme})
	require.NoError(t, err)
	return c
}

// newEnvtestNamespace creates a unique namespace and registers a cleanup.
func newEnvtestNamespace(t *testing.T, c client.Client, prefix string) string {
	t.Helper()
	name := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	require.NoError(t, c.Create(context.Background(), ns))
	t.Cleanup(func() {
		_ = c.Delete(context.Background(), ns)
	})
	return name
}

func newEnvtestReconciler(c client.Client, s store.LastUsedStore, clk Clock) *CodeHubWorkspaceReconciler {
	return &CodeHubWorkspaceReconciler{
		Client: c,
		Scheme: testScheme,
		Store:  s,
		Clock:  clk,
	}
}

func baseCR(name, ns string) *runtimev1alpha1.CodeHubWorkspace {
	return &runtimev1alpha1.CodeHubWorkspace{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: runtimev1alpha1.CodeHubWorkspaceSpec{
			Image:              "nginx:alpine",
			ImagePullPolicy:    corev1.PullIfNotPresent,
			ServicePort:        80,
			ContainerPort:      80,
			IdleTimeoutSeconds: 60,
			MinReplicas:        0,
			MaxReplicas:        1,
			LastUsedKey:        "runtime:test:last_used_at",
		},
	}
}

// errStore always returns a transport error; used for store-unreachable tests.
type errStore struct{}

func (errStore) Get(_ context.Context, _ string) (time.Time, bool, error) {
	return time.Time{}, false, errors.New("simulated redis outage")
}

// TestEnvtest_CRDRejectsShortIdleTimeout proves the CRD schema rejects
// idleTimeoutSeconds below 60 on create. Unit tests with fake client skip
// OpenAPI validation and cannot catch this.
func TestEnvtest_CRDRejectsShortIdleTimeout(t *testing.T) {
	c := newEnvtestClient(t)
	ns := newEnvtestNamespace(t, c, "reject-short-idle")

	cr := baseCR("demo", ns)
	cr.Spec.IdleTimeoutSeconds = 30

	err := c.Create(context.Background(), cr)
	require.Error(t, err, "CRD schema must reject idleTimeoutSeconds < 60")
	require.Contains(t, err.Error(), "idleTimeoutSeconds", "error must mention the offending field")
}

// TestEnvtest_ReconcileCreatesChildren proves a real reconcile round against
// a real apiserver creates a Deployment and a Service as owned children.
func TestEnvtest_ReconcileCreatesChildren(t *testing.T) {
	c := newEnvtestClient(t)
	ns := newEnvtestNamespace(t, c, "reconcile-children")

	cr := baseCR("demo", ns)
	require.NoError(t, c.Create(context.Background(), cr))

	rec := newEnvtestReconciler(c, store.NewFakeStore(), nil)
	_, err := rec.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: ns},
	})
	require.NoError(t, err)

	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: cr.Name, Namespace: ns}, &dep))
	require.NotNil(t, dep.Spec.Replicas)
	require.Equal(t, int32(1), *dep.Spec.Replicas, "fresh CR with no last-used must scale to maxReplicas")
	require.Len(t, dep.OwnerReferences, 1, "Deployment must be owned by the CR")
	require.Equal(t, cr.Name, dep.OwnerReferences[0].Name)

	var svc corev1.Service
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: cr.Name, Namespace: ns}, &svc))
	require.Len(t, svc.OwnerReferences, 1, "Service must be owned by the CR")

	// Status subresource must be populated by reconcile.
	var got runtimev1alpha1.CodeHubWorkspace
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: cr.Name, Namespace: ns}, &got))
	require.Equal(t, runtimev1alpha1.PhaseRunning, got.Status.Phase)
	require.Equal(t, int32(1), got.Status.DesiredReplicas)
	require.Equal(t, cr.Generation, got.Status.ObservedGeneration)
}

// TestEnvtest_ChildrenHaveControllerOwnerRef proves that the reconciler
// wires the ownerReference + controller flag + blockOwnerDeletion bits that
// kube-controller-manager needs for cascading GC. We cannot observe real GC
// in envtest because kube-controller-manager is not running — the actual
// cascade is covered by the e2e tier. What we *can* verify here is that the
// controller set the ownership metadata correctly against a real apiserver.
func TestEnvtest_ChildrenHaveControllerOwnerRef(t *testing.T) {
	c := newEnvtestClient(t)
	ns := newEnvtestNamespace(t, c, "ownerref")
	ctx := context.Background()

	cr := baseCR("demo", ns)
	require.NoError(t, c.Create(ctx, cr))

	rec := newEnvtestReconciler(c, store.NewFakeStore(), nil)
	_, err := rec.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: ns},
	})
	require.NoError(t, err)

	key := types.NamespacedName{Name: cr.Name, Namespace: ns}

	var dep appsv1.Deployment
	require.NoError(t, c.Get(ctx, key, &dep))
	require.Len(t, dep.OwnerReferences, 1, "Deployment must have exactly one ownerRef")
	depOwner := dep.OwnerReferences[0]
	require.Equal(t, cr.Name, depOwner.Name)
	require.Equal(t, "CodeHubWorkspace", depOwner.Kind)
	require.NotNil(t, depOwner.Controller, "Deployment ownerRef must set Controller=true for GC to cascade")
	require.True(t, *depOwner.Controller)
	require.NotNil(t, depOwner.BlockOwnerDeletion)
	require.True(t, *depOwner.BlockOwnerDeletion)

	var svc corev1.Service
	require.NoError(t, c.Get(ctx, key, &svc))
	require.Len(t, svc.OwnerReferences, 1, "Service must have exactly one ownerRef")
	svcOwner := svc.OwnerReferences[0]
	require.Equal(t, cr.Name, svcOwner.Name)
	require.NotNil(t, svcOwner.Controller)
	require.True(t, *svcOwner.Controller)
}

// TestEnvtest_StoreErrorPreservesReplicas proves the store-unreachable path
// creates the Deployment at maxReplicas and writes an ExternalStoreReachable
// = False condition, without crashing the reconciler.
func TestEnvtest_StoreErrorPreservesReplicas(t *testing.T) {
	c := newEnvtestClient(t)
	ns := newEnvtestNamespace(t, c, "store-error")
	ctx := context.Background()

	cr := baseCR("demo", ns)
	require.NoError(t, c.Create(ctx, cr))

	rec := newEnvtestReconciler(c, errStore{}, nil)
	_, err := rec.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: ns},
	})
	// Reconcile returns nil on store errors (just requeues); the bug we
	// are guarding against is scaling down to 0 on store outage.
	require.NoError(t, err)

	var dep appsv1.Deployment
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: ns}, &dep))
	require.NotNil(t, dep.Spec.Replicas)
	require.Equal(t, int32(1), *dep.Spec.Replicas,
		"store-unreachable path must create Deployment at maxReplicas, never 0")

	var got runtimev1alpha1.CodeHubWorkspace
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: ns}, &got))

	var reachable *metav1.Condition
	for i := range got.Status.Conditions {
		if got.Status.Conditions[i].Type == runtimev1alpha1.ConditionExternalStoreReachable {
			reachable = &got.Status.Conditions[i]
			break
		}
	}
	require.NotNil(t, reachable, "ExternalStoreReachable condition must be set")
	require.Equal(t, metav1.ConditionFalse, reachable.Status)
}

// TestEnvtest_StatusSubresource proves that Status().Update does not
// mutate spec, which is something the fake client can fake but which we
// want to confirm against a real apiserver.
func TestEnvtest_StatusSubresource(t *testing.T) {
	c := newEnvtestClient(t)
	ns := newEnvtestNamespace(t, c, "status-subresource")
	ctx := context.Background()

	cr := baseCR("demo", ns)
	require.NoError(t, c.Create(ctx, cr))

	// Drive one reconcile to populate status.
	rec := newEnvtestReconciler(c, store.NewFakeStore(), nil)
	_, err := rec.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: ns},
	})
	require.NoError(t, err)

	// Mutate spec locally, then call Status().Update — spec change must
	// be ignored by the apiserver because status subresource is declared.
	var fetched runtimev1alpha1.CodeHubWorkspace
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: ns}, &fetched))
	originalImage := fetched.Spec.Image
	fetched.Spec.Image = "should-not-persist"
	fetched.Status.Phase = "CustomPhaseForTest"
	require.NoError(t, c.Status().Update(ctx, &fetched))

	var reread runtimev1alpha1.CodeHubWorkspace
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: ns}, &reread))
	require.Equal(t, originalImage, reread.Spec.Image,
		"Status().Update must not persist spec mutations")
	require.Equal(t, "CustomPhaseForTest", reread.Status.Phase,
		"Status().Update must persist status mutations")
}
