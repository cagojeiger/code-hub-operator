package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CodeHubWorkspaceSpec defines the desired state of a single workspace
// instance. Fields that can reasonably come from a CodeHubWorkspaceClass are
// optional here — the reconciler merges the referenced Class before validating.
// Workspace values always win over Class values.
type CodeHubWorkspaceSpec struct {
	// ClassRef is the name of a cluster-scoped CodeHubWorkspaceClass whose
	// defaults should be inherited by this Workspace. Optional. When set,
	// the reconciler fetches the Class and fills any unset fields below
	// before reconciling children. When unset, only values directly on
	// this Spec are used.
	// +optional
	ClassRef string `json:"classRef,omitempty"`

	// Image is the container image to run. Optional — may come from the
	// referenced Class. The merged value must be non-empty or reconcile
	// fails with a Ready=False condition.
	// +optional
	Image string `json:"image,omitempty"`

	// ImagePullPolicy for the container. Optional — may come from the
	// referenced Class. When neither Workspace nor Class sets it, the
	// reconciler falls back to IfNotPresent.
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// ServicePort is the port the Service exposes. Optional — may come
	// from the referenced Class.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	ServicePort int32 `json:"servicePort,omitempty"`

	// ContainerPort is the port the container listens on. Optional — may
	// come from the referenced Class.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	ContainerPort int32 `json:"containerPort,omitempty"`

	// IdleTimeoutSeconds is the time since last use after which the
	// workspace is considered idle and scaled down to MinReplicas.
	// Optional — may come from the referenced Class.
	// +kubebuilder:validation:Minimum=60
	// +optional
	IdleTimeoutSeconds int64 `json:"idleTimeoutSeconds,omitempty"`

	// MinReplicas is the replica count when idle. Must be 0 or 1 in v1.
	// Required — instance-specific scaling policy.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	MinReplicas int32 `json:"minReplicas"`

	// MaxReplicas is the replica count when active. Must be 1 in v1.
	// Required — instance-specific scaling policy.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1
	MaxReplicas int32 `json:"maxReplicas"`

	// LastUsedKey is the key in the external store that holds the
	// last-used timestamp (Unix epoch seconds) for this workspace.
	// Required — the key identifies a specific instance and cannot be
	// inherited from a Class.
	// +kubebuilder:validation:MinLength=1
	LastUsedKey string `json:"lastUsedKey"`

	// Env are environment variables passed to the container. Workspace-only,
	// not inherited from Class (merge semantics would be ambiguous).
	// +optional
	Env map[string]string `json:"env,omitempty"`

	// Resources are the container resource requirements. Optional — may
	// come from the referenced Class.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

// CodeHubWorkspaceStatus is the observed state of a CodeHubWorkspace.
// Status holds only summary values; the authoritative last-used timestamp
// lives in the external store, never here.
type CodeHubWorkspaceStatus struct {
	// Phase is a high-level summary of the workspace state.
	// +optional
	Phase string `json:"phase,omitempty"`

	// ReadyReplicas is the number of ready pods observed on the Deployment.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// DesiredReplicas is the replica count the operator last applied.
	// +optional
	DesiredReplicas int32 `json:"desiredReplicas,omitempty"`

	// LastScaleAction records what the operator did on its most recent
	// reconcile: ScaleToOne, ScaleToZero, or NoChange.
	// +optional
	LastScaleAction string `json:"lastScaleAction,omitempty"`

	// ObservedGeneration is the .metadata.generation the controller last acted on.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastEvaluatedTime is when the controller last ran reconcile for this CR.
	// +optional
	LastEvaluatedTime metav1.Time `json:"lastEvaluatedTime,omitempty"`

	// IdleSince is the time at which the workspace first became idle. Nil while active.
	// +optional
	IdleSince *metav1.Time `json:"idleSince,omitempty"`

	// ResolvedClass is the name of the CodeHubWorkspaceClass that was
	// successfully merged into this Workspace on the most recent reconcile.
	// Empty when spec.classRef is unset.
	// +optional
	ResolvedClass string `json:"resolvedClass,omitempty"`

	// Conditions represent the latest available observations of state.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// Phase values for CodeHubWorkspaceStatus.Phase.
const (
	PhaseRunning    = "Running"
	PhaseIdle       = "Idle"
	PhaseScaledDown = "ScaledDown"
	PhaseError      = "Error"
)

// LastScaleAction values.
const (
	ScaleActionScaleToOne  = "ScaleToOne"
	ScaleActionScaleToZero = "ScaleToZero"
	ScaleActionNoChange    = "NoChange"
)

// Condition type values.
const (
	ConditionReady                  = "Ready"
	ConditionExternalStoreReachable = "ExternalStoreReachable"
	ConditionClassResolved          = "ClassResolved"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=chws
// +kubebuilder:printcolumn:name="Class",type=string,JSONPath=`.spec.classRef`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Desired",type=integer,JSONPath=`.status.desiredReplicas`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CodeHubWorkspace is the Schema for the codehubworkspaces API.
type CodeHubWorkspace struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CodeHubWorkspaceSpec   `json:"spec,omitempty"`
	Status CodeHubWorkspaceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CodeHubWorkspaceList contains a list of CodeHubWorkspace.
type CodeHubWorkspaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CodeHubWorkspace `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CodeHubWorkspace{}, &CodeHubWorkspaceList{})
}
