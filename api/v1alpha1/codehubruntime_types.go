package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CodeHubRuntimeSpec defines the desired state of a single runtime instance.
type CodeHubRuntimeSpec struct {
	// Image is the container image to run.
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// ImagePullPolicy for the container. Defaults to IfNotPresent.
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	// +kubebuilder:default=IfNotPresent
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// ServicePort is the port the Service exposes.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	ServicePort int32 `json:"servicePort"`

	// ContainerPort is the port the container listens on.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	ContainerPort int32 `json:"containerPort"`

	// IdleTimeoutSeconds is the time since last use after which the runtime
	// is considered idle and scaled down to MinReplicas.
	// +kubebuilder:validation:Minimum=60
	IdleTimeoutSeconds int64 `json:"idleTimeoutSeconds"`

	// MinReplicas is the replica count when idle. Must be 0 or 1 in v1.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	MinReplicas int32 `json:"minReplicas"`

	// MaxReplicas is the replica count when active. Must be 1 in v1.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1
	MaxReplicas int32 `json:"maxReplicas"`

	// LastUsedKey is the key in the external store that holds the last-used
	// timestamp (Unix epoch seconds) for this runtime.
	// +kubebuilder:validation:MinLength=1
	LastUsedKey string `json:"lastUsedKey"`

	// Env are environment variables passed to the container.
	// +optional
	Env map[string]string `json:"env,omitempty"`

	// Resources are the container resource requirements.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

// CodeHubRuntimeStatus is the observed state of a CodeHubRuntime.
// Status holds only summary values; the authoritative last-used timestamp
// lives in the external store, never here.
type CodeHubRuntimeStatus struct {
	// Phase is a high-level summary of the runtime state.
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

	// IdleSince is the time at which the runtime first became idle. Nil while active.
	// +optional
	IdleSince *metav1.Time `json:"idleSince,omitempty"`

	// Conditions represent the latest available observations of state.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// Phase values for CodeHubRuntimeStatus.Phase.
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
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=chr
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Desired",type=integer,JSONPath=`.status.desiredReplicas`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CodeHubRuntime is the Schema for the codehubruntimes API.
type CodeHubRuntime struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CodeHubRuntimeSpec   `json:"spec,omitempty"`
	Status CodeHubRuntimeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CodeHubRuntimeList contains a list of CodeHubRuntime.
type CodeHubRuntimeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CodeHubRuntime `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CodeHubRuntime{}, &CodeHubRuntimeList{})
}
