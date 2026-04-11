package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	runtimev1alpha1 "github.com/cagojeiger/code-hub-operator/api/v1alpha1"
	"github.com/cagojeiger/code-hub-operator/internal/store"
)

// requeueAfter is the periodic re-evaluation interval. Each reconcile also
// schedules its next run after this duration even when nothing changed.
const requeueAfter = 30 * time.Second

const (
	eventReasonScaledUp       = "ScaledUp"
	eventReasonScaledDown     = "ScaledDown"
	eventReasonReconcileError = "ReconcileError"
)

// Clock is an injectable time source. Tests provide a fake clock; production
// uses the real one.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// CodeHubRuntimeReconciler reconciles a CodeHubRuntime object.
type CodeHubRuntimeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Store  store.LastUsedStore
	// Recorder is optional; nil disables Event recording.
	Recorder record.EventRecorder
	// Clock is optional; nil means use the real clock.
	Clock Clock
}

// +kubebuilder:rbac:groups=runtime.project-jelly.io,resources=codehubruntimes,verbs=get;list;watch
// +kubebuilder:rbac:groups=runtime.project-jelly.io,resources=codehubruntimes/status,verbs=get;update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

// Reconcile drives a CodeHubRuntime towards its desired state.
//
// Flow (see plan/§5.2):
//  1. Fetch CR; missing = noop
//  2. Validate spec
//  3. Ensure Service
//  4. Query last-used store
//  5. Decide idle vs active; compute desired replicas
//  6. Ensure Deployment with desired replicas
//  7. Write status
//
// Error paths are meant to be non-fatal: replicas are preserved when the
// external store is unreachable.
func (r *CodeHubRuntimeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	clock := r.clock()

	cr := &runtimev1alpha1.CodeHubRuntime{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if err := validateForDeployment(cr); err != nil {
		r.writeErrorStatus(ctx, cr, fmt.Sprintf("invalid spec: %v", err), clock)
		// Invalid spec is a user error; don't requeue on a tight loop.
		return ctrl.Result{}, nil
	}

	if err := r.ensureService(ctx, cr); err != nil {
		logger.Error(err, "ensure service")
		r.writeErrorStatus(ctx, cr, fmt.Sprintf("service: %v", err), clock)
		return ctrl.Result{RequeueAfter: requeueAfter}, err
	}

	lastUsed, found, storeErr := r.Store.Get(ctx, cr.Spec.LastUsedKey)
	if storeErr != nil {
		// Store unreachable: preserve current replicas, report the condition,
		// and requeue. Never scale up or down on store errors.
		logger.Error(storeErr, "last-used store unreachable")
		if err := r.ensureDeploymentPreserveReplicas(ctx, cr); err != nil {
			r.writeErrorStatus(ctx, cr, fmt.Sprintf("deployment: %v", err), clock)
			return ctrl.Result{RequeueAfter: requeueAfter}, err
		}
		ready := r.observeReady(ctx, cr)
		r.writeStoreErrorStatus(ctx, cr, storeErr, ready, clock)
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	now := clock.Now()
	idleTimeout := time.Duration(cr.Spec.IdleTimeoutSeconds) * time.Second
	// A missing last-used entry is treated as active. This keeps freshly
	// created runtimes from being scaled down on their first reconcile.
	isIdle := found && now.Sub(lastUsed) > idleTimeout

	desired := cr.Spec.MaxReplicas
	phase := runtimev1alpha1.PhaseRunning
	if isIdle {
		desired = cr.Spec.MinReplicas
		if desired == 0 {
			phase = runtimev1alpha1.PhaseScaledDown
		} else {
			phase = runtimev1alpha1.PhaseIdle
		}
	}

	scaleAction, ready, err := r.ensureDeployment(ctx, cr, desired)
	if err != nil {
		logger.Error(err, "ensure deployment")
		r.writeErrorStatus(ctx, cr, fmt.Sprintf("deployment: %v", err), clock)
		return ctrl.Result{RequeueAfter: requeueAfter}, err
	}

	var idleSince *metav1.Time
	if isIdle && found {
		t := metav1.NewTime(lastUsed.Add(idleTimeout))
		idleSince = &t
	}
	r.writeSuccessStatus(ctx, cr, phase, desired, ready, scaleAction, idleSince, clock)

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *CodeHubRuntimeReconciler) clock() Clock {
	if r.Clock != nil {
		return r.Clock
	}
	return realClock{}
}

// ensureService creates or updates the owned Service.
func (r *CodeHubRuntimeReconciler) ensureService(ctx context.Context, cr *runtimev1alpha1.CodeHubRuntime) error {
	existing := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, existing)
	if apierrors.IsNotFound(err) {
		desired := buildService(cr)
		if err := controllerutil.SetControllerReference(cr, desired, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	candidate := buildService(cr)
	if servicePortsEqual(existing.Spec.Ports, candidate.Spec.Ports) &&
		selectorsEqual(existing.Spec.Selector, candidate.Spec.Selector) &&
		selectorsEqual(existing.Labels, candidate.Labels) &&
		metav1.IsControlledBy(existing, cr) {
		return nil
	}
	// Update in place. ClusterIP and other API-server-assigned fields are
	// preserved because we only mutate the fields we own.
	existing.Spec.Ports = candidate.Spec.Ports
	existing.Spec.Selector = candidate.Spec.Selector
	existing.Labels = candidate.Labels
	// Ensure ownerRef is reconciled as part of service drift recovery.
	if err := controllerutil.SetControllerReference(cr, existing, r.Scheme); err != nil {
		return err
	}
	return r.Update(ctx, existing)
}

// ensureDeployment creates the Deployment with the given desired replicas,
// or updates the replicas and template of an existing one. It returns the
// scale action taken, the ready replica count, and an error.
func (r *CodeHubRuntimeReconciler) ensureDeployment(
	ctx context.Context,
	cr *runtimev1alpha1.CodeHubRuntime,
	desired int32,
) (string, int32, error) {
	existing := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, existing)
	if apierrors.IsNotFound(err) {
		dep := buildDeployment(cr, desired)
		if err := controllerutil.SetControllerReference(cr, dep, r.Scheme); err != nil {
			return "", 0, err
		}
		if err := r.Create(ctx, dep); err != nil {
			return "", 0, err
		}
		if desired == 0 {
			return runtimev1alpha1.ScaleActionScaleToZero, 0, nil
		}
		return runtimev1alpha1.ScaleActionScaleToOne, 0, nil
	}
	if err != nil {
		return "", 0, err
	}

	candidate := buildDeployment(cr, desired)

	current := int32(0)
	if existing.Spec.Replicas != nil {
		current = *existing.Spec.Replicas
	}

	replicasChanged := current != desired
	templateChanged := !podTemplateEquivalent(existing, candidate)

	if !replicasChanged && !templateChanged {
		return runtimev1alpha1.ScaleActionNoChange, existing.Status.ReadyReplicas, nil
	}

	// Patch the existing object in place so we keep its ResourceVersion and
	// immutable Selector. Replicas and template are fields we own.
	desiredReplicas := desired
	existing.Spec.Replicas = &desiredReplicas
	existing.Spec.Template = candidate.Spec.Template
	existing.Labels = candidate.Labels
	// Make sure ownerRef is set (idempotent on same controller).
	if err := controllerutil.SetControllerReference(cr, existing, r.Scheme); err != nil {
		return "", 0, err
	}
	if err := r.Update(ctx, existing); err != nil {
		return "", 0, err
	}

	scaleAction := runtimev1alpha1.ScaleActionNoChange
	if replicasChanged {
		if desired == 0 {
			scaleAction = runtimev1alpha1.ScaleActionScaleToZero
		} else {
			scaleAction = runtimev1alpha1.ScaleActionScaleToOne
		}
	}
	return scaleAction, existing.Status.ReadyReplicas, nil
}

// ensureDeploymentPreserveReplicas is used on the store-error path. It
// creates the Deployment at MaxReplicas if it is missing, otherwise leaves
// it alone. It never touches replicas on an existing Deployment.
func (r *CodeHubRuntimeReconciler) ensureDeploymentPreserveReplicas(ctx context.Context, cr *runtimev1alpha1.CodeHubRuntime) error {
	existing := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, existing)
	if apierrors.IsNotFound(err) {
		dep := buildDeployment(cr, cr.Spec.MaxReplicas)
		if err := controllerutil.SetControllerReference(cr, dep, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, dep)
	}
	return err
}

// observeReady returns the Deployment's readyReplicas, or 0 if the
// Deployment can't be read.
func (r *CodeHubRuntimeReconciler) observeReady(ctx context.Context, cr *runtimev1alpha1.CodeHubRuntime) int32 {
	dep := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, dep); err != nil {
		return 0
	}
	return dep.Status.ReadyReplicas
}

// writeSuccessStatus is the happy-path status writer.
func (r *CodeHubRuntimeReconciler) writeSuccessStatus(
	ctx context.Context,
	cr *runtimev1alpha1.CodeHubRuntime,
	phase string,
	desired, ready int32,
	scaleAction string,
	idleSince *metav1.Time,
	clock Clock,
) {
	cr.Status.Phase = phase
	cr.Status.DesiredReplicas = desired
	cr.Status.ReadyReplicas = ready
	cr.Status.LastScaleAction = scaleAction
	cr.Status.ObservedGeneration = cr.Generation
	cr.Status.LastEvaluatedTime = metav1.NewTime(clock.Now())
	cr.Status.IdleSince = idleSince

	readyStatus := metav1.ConditionFalse
	if ready >= desired && desired > 0 {
		readyStatus = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type:    runtimev1alpha1.ConditionReady,
		Status:  readyStatus,
		Reason:  "ReplicasObserved",
		Message: fmt.Sprintf("%d/%d ready", ready, desired),
	})
	meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type:   runtimev1alpha1.ConditionExternalStoreReachable,
		Status: metav1.ConditionTrue,
		Reason: "Reachable",
	})

	switch scaleAction {
	case runtimev1alpha1.ScaleActionScaleToOne:
		r.recordNormal(cr, eventReasonScaledUp, "Scaled deployment to %d replica(s)", desired)
	case runtimev1alpha1.ScaleActionScaleToZero:
		r.recordNormal(cr, eventReasonScaledDown, "Scaled deployment to %d replica(s)", desired)
	}

	_ = r.Status().Update(ctx, cr)
}

// writeStoreErrorStatus reports that the external store is unreachable
// without asserting any phase other than Error.
func (r *CodeHubRuntimeReconciler) writeStoreErrorStatus(
	ctx context.Context,
	cr *runtimev1alpha1.CodeHubRuntime,
	storeErr error,
	ready int32,
	clock Clock,
) {
	cr.Status.Phase = runtimev1alpha1.PhaseError
	cr.Status.ReadyReplicas = ready
	cr.Status.ObservedGeneration = cr.Generation
	cr.Status.LastEvaluatedTime = metav1.NewTime(clock.Now())

	meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type:    runtimev1alpha1.ConditionExternalStoreReachable,
		Status:  metav1.ConditionFalse,
		Reason:  "StoreError",
		Message: storeErr.Error(),
	})

	_ = r.Status().Update(ctx, cr)
}

// writeErrorStatus records a generic reconcile error.
func (r *CodeHubRuntimeReconciler) writeErrorStatus(
	ctx context.Context,
	cr *runtimev1alpha1.CodeHubRuntime,
	msg string,
	clock Clock,
) {
	cr.Status.Phase = runtimev1alpha1.PhaseError
	cr.Status.ObservedGeneration = cr.Generation
	cr.Status.LastEvaluatedTime = metav1.NewTime(clock.Now())

	meta.SetStatusCondition(&cr.Status.Conditions, metav1.Condition{
		Type:    runtimev1alpha1.ConditionReady,
		Status:  metav1.ConditionFalse,
		Reason:  "ReconcileError",
		Message: msg,
	})
	r.recordWarning(cr, eventReasonReconcileError, msg)

	_ = r.Status().Update(ctx, cr)
}

func (r *CodeHubRuntimeReconciler) recordNormal(cr *runtimev1alpha1.CodeHubRuntime, reason, msg string, args ...any) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(cr, corev1.EventTypeNormal, reason, msg, args...)
}

func (r *CodeHubRuntimeReconciler) recordWarning(cr *runtimev1alpha1.CodeHubRuntime, reason, msg string, args ...any) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(cr, corev1.EventTypeWarning, reason, msg, args...)
}

// SetupWithManager registers this reconciler with the manager and wires
// up watches for the primary resource and owned children.
func (r *CodeHubRuntimeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.CodeHubRuntime{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
