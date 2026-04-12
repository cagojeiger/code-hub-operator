package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	runtimev1alpha1 "github.com/cagojeiger/code-hub-operator/api/v1alpha1"
	"github.com/cagojeiger/code-hub-operator/internal/store"
)

// fixedClock is a test Clock we can step manually.
type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time          { return c.t }
func (c *fixedClock) advance(d time.Duration) { c.t = c.t.Add(d) }

type testEnv struct {
	t      *testing.T
	client client.Client
	store  *store.FakeStore
	clock  *fixedClock
	rec    *CodeHubWorkspaceReconciler
}

func newTestEnv(t *testing.T, objs ...client.Object) *testEnv {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, runtimev1alpha1.AddToScheme(scheme))

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&runtimev1alpha1.CodeHubWorkspace{}).
		Build()

	fs := store.NewFakeStore()
	clk := &fixedClock{t: time.Date(2026, 4, 11, 12, 0, 0, 0, time.UTC)}

	rec := &CodeHubWorkspaceReconciler{
		Client: c,
		Scheme: scheme,
		Store:  fs,
		Clock:  clk,
	}
	return &testEnv{t: t, client: c, store: fs, clock: clk, rec: rec}
}

func (e *testEnv) reconcile(name, ns string) ctrl.Result {
	e.t.Helper()
	res, err := e.rec.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: ns},
	})
	require.NoError(e.t, err)
	return res
}

func (e *testEnv) reconcileExpectErr(name, ns string) (ctrl.Result, error) {
	e.t.Helper()
	return e.rec.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: ns},
	})
}

func (e *testEnv) getCR(name, ns string) *runtimev1alpha1.CodeHubWorkspace {
	e.t.Helper()
	cr := &runtimev1alpha1.CodeHubWorkspace{}
	require.NoError(e.t, e.client.Get(context.Background(),
		types.NamespacedName{Name: name, Namespace: ns}, cr))
	return cr
}

func (e *testEnv) getDeployment(name, ns string) *appsv1.Deployment {
	e.t.Helper()
	dep := &appsv1.Deployment{}
	require.NoError(e.t, e.client.Get(context.Background(),
		types.NamespacedName{Name: name, Namespace: ns}, dep))
	return dep
}

func (e *testEnv) getDeploymentMaybe(name, ns string) (*appsv1.Deployment, bool) {
	e.t.Helper()
	dep := &appsv1.Deployment{}
	err := e.client.Get(context.Background(),
		types.NamespacedName{Name: name, Namespace: ns}, dep)
	if err != nil {
		return nil, false
	}
	return dep, true
}

func (e *testEnv) getService(name, ns string) *corev1.Service {
	e.t.Helper()
	svc := &corev1.Service{}
	require.NoError(e.t, e.client.Get(context.Background(),
		types.NamespacedName{Name: name, Namespace: ns}, svc))
	return svc
}

func sampleRuntime() *runtimev1alpha1.CodeHubWorkspace {
	return &runtimev1alpha1.CodeHubWorkspace{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "demo",
			Namespace:  "default",
			Generation: 1,
		},
		Spec: runtimev1alpha1.CodeHubWorkspaceSpec{
			Image:              "ghcr.io/acme/demo:0.1.0",
			ImagePullPolicy:    corev1.PullIfNotPresent,
			ServicePort:        80,
			ContainerPort:      8080,
			IdleTimeoutSeconds: 1800, // 30m
			MinReplicas:        0,
			MaxReplicas:        1,
			LastUsedKey:        "runtime:default:demo:last_used_at",
		},
	}
}

func findCondition(conds []metav1.Condition, t string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == t {
			return &conds[i]
		}
	}
	return nil
}

// --- Tests -------------------------------------------------------------

func TestReconcile_CreatesDeploymentAndService(t *testing.T) {
	cr := sampleRuntime()
	env := newTestEnv(t, cr)

	// No last-used entry → treated as active on first reconcile.
	res := env.reconcile(cr.Name, cr.Namespace)
	require.Equal(t, requeueAfter, res.RequeueAfter)

	dep := env.getDeployment(cr.Name, cr.Namespace)
	require.NotNil(t, dep.Spec.Replicas)
	require.Equal(t, int32(1), *dep.Spec.Replicas)
	require.Equal(t, cr.Spec.Image, dep.Spec.Template.Spec.Containers[0].Image)
	require.Len(t, dep.OwnerReferences, 1, "deployment should have ownerRef")
	require.Equal(t, cr.Name, dep.OwnerReferences[0].Name)
	require.Equal(t, "CodeHubWorkspace", dep.OwnerReferences[0].Kind)

	svc := env.getService(cr.Name, cr.Namespace)
	require.Equal(t, int32(80), svc.Spec.Ports[0].Port)
	require.Equal(t, int32(8080), svc.Spec.Ports[0].TargetPort.IntVal)
	require.Len(t, svc.OwnerReferences, 1, "service should have ownerRef")

	got := env.getCR(cr.Name, cr.Namespace)
	require.Equal(t, runtimev1alpha1.PhaseRunning, got.Status.Phase)
	require.Equal(t, int32(1), got.Status.DesiredReplicas)
	require.Equal(t, cr.Generation, got.Status.ObservedGeneration)

	storeCond := findCondition(got.Status.Conditions, runtimev1alpha1.ConditionExternalStoreReachable)
	require.NotNil(t, storeCond)
	require.Equal(t, metav1.ConditionTrue, storeCond.Status)
}

func TestReconcile_ActiveWithRecentUsage(t *testing.T) {
	cr := sampleRuntime()
	env := newTestEnv(t, cr)

	// Last used 10 minutes ago — well inside the 30m timeout.
	env.store.Set(cr.Spec.LastUsedKey, env.clock.t.Add(-10*time.Minute))

	env.reconcile(cr.Name, cr.Namespace)

	dep := env.getDeployment(cr.Name, cr.Namespace)
	require.Equal(t, int32(1), *dep.Spec.Replicas)

	got := env.getCR(cr.Name, cr.Namespace)
	require.Equal(t, runtimev1alpha1.PhaseRunning, got.Status.Phase)
	require.Nil(t, got.Status.IdleSince)
}

func TestReconcile_IdleScalesDownToZero(t *testing.T) {
	cr := sampleRuntime()
	env := newTestEnv(t, cr)

	// Last used an hour ago; timeout is 30m → idle.
	lastUsed := env.clock.t.Add(-1 * time.Hour)
	env.store.Set(cr.Spec.LastUsedKey, lastUsed)

	env.reconcile(cr.Name, cr.Namespace)

	dep := env.getDeployment(cr.Name, cr.Namespace)
	require.Equal(t, int32(0), *dep.Spec.Replicas)

	got := env.getCR(cr.Name, cr.Namespace)
	require.Equal(t, runtimev1alpha1.PhaseScaledDown, got.Status.Phase)
	require.Equal(t, int32(0), got.Status.DesiredReplicas)
	require.Equal(t, runtimev1alpha1.ScaleActionScaleToZero, got.Status.LastScaleAction)
	require.NotNil(t, got.Status.IdleSince)
	// idleSince should be lastUsed + timeout.
	require.True(t, got.Status.IdleSince.Time.Equal(lastUsed.Add(30*time.Minute)))
}

func TestReconcile_IdleWithMinReplicasOneReportsIdle(t *testing.T) {
	cr := sampleRuntime()
	cr.Spec.MinReplicas = 1
	cr.Spec.MaxReplicas = 1
	env := newTestEnv(t, cr)

	env.store.Set(cr.Spec.LastUsedKey, env.clock.t.Add(-2*time.Hour))

	env.reconcile(cr.Name, cr.Namespace)

	dep := env.getDeployment(cr.Name, cr.Namespace)
	// With min=max=1 we stay at 1 even when idle, but phase is Idle (not Running).
	require.Equal(t, int32(1), *dep.Spec.Replicas)

	got := env.getCR(cr.Name, cr.Namespace)
	require.Equal(t, runtimev1alpha1.PhaseIdle, got.Status.Phase)
}

func TestReconcile_IdleThenResumed(t *testing.T) {
	cr := sampleRuntime()
	env := newTestEnv(t, cr)

	// Step 1: idle → scale to 0.
	env.store.Set(cr.Spec.LastUsedKey, env.clock.t.Add(-1*time.Hour))
	env.reconcile(cr.Name, cr.Namespace)
	require.Equal(t, int32(0), *env.getDeployment(cr.Name, cr.Namespace).Spec.Replicas)

	// Step 2: usage is now current → scale back up to 1.
	env.store.Set(cr.Spec.LastUsedKey, env.clock.t)
	env.reconcile(cr.Name, cr.Namespace)

	dep := env.getDeployment(cr.Name, cr.Namespace)
	require.Equal(t, int32(1), *dep.Spec.Replicas)

	got := env.getCR(cr.Name, cr.Namespace)
	require.Equal(t, runtimev1alpha1.PhaseRunning, got.Status.Phase)
	require.Equal(t, runtimev1alpha1.ScaleActionScaleToOne, got.Status.LastScaleAction)
	require.Nil(t, got.Status.IdleSince)
}

func TestReconcile_NoLastUsedTreatedAsActive(t *testing.T) {
	cr := sampleRuntime()
	env := newTestEnv(t, cr)

	// Store has no entry for the key at all.
	env.reconcile(cr.Name, cr.Namespace)

	dep := env.getDeployment(cr.Name, cr.Namespace)
	require.Equal(t, int32(1), *dep.Spec.Replicas,
		"missing last-used must be treated as active to avoid scaling down fresh runtimes")

	got := env.getCR(cr.Name, cr.Namespace)
	require.Equal(t, runtimev1alpha1.PhaseRunning, got.Status.Phase)
}

func TestReconcile_StoreErrorPreservesReplicas(t *testing.T) {
	cr := sampleRuntime()
	env := newTestEnv(t, cr)

	// First reconcile: creates Deployment at 1 (no last-used → active).
	env.reconcile(cr.Name, cr.Namespace)
	require.Equal(t, int32(1), *env.getDeployment(cr.Name, cr.Namespace).Spec.Replicas)

	// Now store is broken.
	env.store.SetError(errors.New("boom"))
	env.reconcile(cr.Name, cr.Namespace)

	dep := env.getDeployment(cr.Name, cr.Namespace)
	require.Equal(t, int32(1), *dep.Spec.Replicas,
		"replicas MUST be preserved when the external store is unreachable")

	got := env.getCR(cr.Name, cr.Namespace)
	require.Equal(t, runtimev1alpha1.PhaseError, got.Status.Phase)

	storeCond := findCondition(got.Status.Conditions, runtimev1alpha1.ConditionExternalStoreReachable)
	require.NotNil(t, storeCond)
	require.Equal(t, metav1.ConditionFalse, storeCond.Status)
	require.Equal(t, "StoreError", storeCond.Reason)
}

func TestReconcile_StoreErrorOnFirstReconcileStillCreatesDeployment(t *testing.T) {
	cr := sampleRuntime()
	env := newTestEnv(t, cr)

	// Store is broken from the start. The operator should still create the
	// Deployment (at MaxReplicas) so that fixing the store later doesn't
	// require manual intervention.
	env.store.SetError(errors.New("boom"))
	env.reconcile(cr.Name, cr.Namespace)

	dep, ok := env.getDeploymentMaybe(cr.Name, cr.Namespace)
	require.True(t, ok, "deployment should be created even when store fails")
	require.Equal(t, int32(1), *dep.Spec.Replicas)

	got := env.getCR(cr.Name, cr.Namespace)
	require.Equal(t, runtimev1alpha1.PhaseError, got.Status.Phase)
}

func TestReconcile_NotFoundIsNoop(t *testing.T) {
	env := newTestEnv(t)
	res, err := env.reconcileExpectErr("missing", "default")
	require.NoError(t, err)
	require.Equal(t, ctrl.Result{}, res)
}

func TestReconcile_InvalidSpecStopsWithoutRequeueLoop(t *testing.T) {
	cr := sampleRuntime()
	cr.Spec.Image = "" // invalid
	env := newTestEnv(t, cr)

	res, err := env.reconcileExpectErr(cr.Name, cr.Namespace)
	require.NoError(t, err)
	require.Equal(t, ctrl.Result{}, res,
		"invalid spec is a user error and should not trigger a tight requeue loop")

	got := env.getCR(cr.Name, cr.Namespace)
	require.Equal(t, runtimev1alpha1.PhaseError, got.Status.Phase)
}

func TestReconcile_IdempotentUpdate(t *testing.T) {
	cr := sampleRuntime()
	env := newTestEnv(t, cr)

	env.reconcile(cr.Name, cr.Namespace)
	rv1 := env.getDeployment(cr.Name, cr.Namespace).ResourceVersion
	svcRv1 := env.getService(cr.Name, cr.Namespace).ResourceVersion

	// Second reconcile with no state change: no spurious updates to the
	// Deployment or the Service.
	env.reconcile(cr.Name, cr.Namespace)
	require.Equal(t, rv1, env.getDeployment(cr.Name, cr.Namespace).ResourceVersion,
		"deployment should not be updated on a no-op reconcile")
	require.Equal(t, svcRv1, env.getService(cr.Name, cr.Namespace).ResourceVersion,
		"service should not be updated on a no-op reconcile")
}

func TestReconcile_ImageUpdatePropagates(t *testing.T) {
	cr := sampleRuntime()
	env := newTestEnv(t, cr)

	env.reconcile(cr.Name, cr.Namespace)

	// User updates the image in the CR.
	fresh := env.getCR(cr.Name, cr.Namespace)
	fresh.Spec.Image = "ghcr.io/acme/demo:0.2.0"
	require.NoError(t, env.client.Update(context.Background(), fresh))

	env.reconcile(cr.Name, cr.Namespace)

	dep := env.getDeployment(cr.Name, cr.Namespace)
	require.Equal(t, "ghcr.io/acme/demo:0.2.0", dep.Spec.Template.Spec.Containers[0].Image)
}

func TestReconcile_ResourcesUpdatePropagates(t *testing.T) {
	cr := sampleRuntime()
	env := newTestEnv(t, cr)

	env.reconcile(cr.Name, cr.Namespace)

	fresh := env.getCR(cr.Name, cr.Namespace)
	fresh.Spec.Resources = &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("200m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("500m"),
		},
	}
	require.NoError(t, env.client.Update(context.Background(), fresh))

	env.reconcile(cr.Name, cr.Namespace)

	dep := env.getDeployment(cr.Name, cr.Namespace)
	got := dep.Spec.Template.Spec.Containers[0].Resources
	reqCPU := got.Requests[corev1.ResourceCPU]
	reqMem := got.Requests[corev1.ResourceMemory]
	limCPU := got.Limits[corev1.ResourceCPU]
	require.Equal(t, "200m", reqCPU.String())
	require.Equal(t, "128Mi", reqMem.String())
	require.Equal(t, "500m", limCPU.String())
}

func TestReconcile_EmitsScaleEvents(t *testing.T) {
	cr := sampleRuntime()
	env := newTestEnv(t, cr)
	rec := record.NewFakeRecorder(10)
	env.rec.Recorder = rec

	env.reconcile(cr.Name, cr.Namespace)

	var evt string
	require.Eventually(t, func() bool {
		select {
		case evt = <-rec.Events:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	require.Contains(t, evt, eventReasonScaledUp)
	require.Contains(t, evt, "Scaled deployment to 1 replica(s)")
}

func TestReconcile_EmitsScaleDownEvents(t *testing.T) {
	cr := sampleRuntime()
	env := newTestEnv(t, cr)
	rec := record.NewFakeRecorder(10)
	env.rec.Recorder = rec

	// Idle condition: last used far enough in the past to scale to zero.
	env.store.Set(cr.Spec.LastUsedKey, env.clock.t.Add(-2*time.Hour))
	env.reconcile(cr.Name, cr.Namespace)

	var evt string
	require.Eventually(t, func() bool {
		select {
		case evt = <-rec.Events:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	require.Contains(t, evt, eventReasonScaledDown)
	require.Contains(t, evt, "Scaled deployment to 0 replica(s)")
}

func TestReconcile_StoreErrorEmitsWarningEvent(t *testing.T) {
	cr := sampleRuntime()
	env := newTestEnv(t, cr)
	rec := record.NewFakeRecorder(10)
	env.rec.Recorder = rec
	env.store.SetError(errors.New("boom 100% unavailable"))

	env.reconcile(cr.Name, cr.Namespace)

	var evt string
	require.Eventually(t, func() bool {
		select {
		case evt = <-rec.Events:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	require.Contains(t, evt, eventReasonStoreUnreachable)
	require.Contains(t, evt, "boom 100% unavailable")
	require.NotContains(t, evt, "%!(", "event formatting should not treat payload as format string")
}

func TestReconcile_ServiceMetadataDriftIsReconciled(t *testing.T) {
	cr := sampleRuntime()
	env := newTestEnv(t, cr)

	env.reconcile(cr.Name, cr.Namespace)

	svc := env.getService(cr.Name, cr.Namespace)
	svc.Labels = map[string]string{"unexpected": "label"}
	svc.OwnerReferences = nil
	require.NoError(t, env.client.Update(context.Background(), svc))

	env.reconcile(cr.Name, cr.Namespace)

	reconciled := env.getService(cr.Name, cr.Namespace)
	require.Equal(t, objectLabels(cr), reconciled.Labels)
	require.Len(t, reconciled.OwnerReferences, 1, "service ownerRef drift should be repaired")
	require.Equal(t, cr.Name, reconciled.OwnerReferences[0].Name)
	require.Equal(t, "CodeHubWorkspace", reconciled.OwnerReferences[0].Kind)
}

func TestReconcile_ClockAdvanceTriggersIdle(t *testing.T) {
	cr := sampleRuntime()
	env := newTestEnv(t, cr)

	// Start: last-used is "now" so it's clearly active.
	env.store.Set(cr.Spec.LastUsedKey, env.clock.t)
	env.reconcile(cr.Name, cr.Namespace)
	require.Equal(t, int32(1), *env.getDeployment(cr.Name, cr.Namespace).Spec.Replicas)

	// Wall clock advances past the idle timeout without any new last-used value.
	env.clock.advance(31 * time.Minute)
	env.reconcile(cr.Name, cr.Namespace)
	require.Equal(t, int32(0), *env.getDeployment(cr.Name, cr.Namespace).Spec.Replicas)
}
