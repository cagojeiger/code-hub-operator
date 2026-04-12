package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CodeHubWorkspaceClassSpec holds platform-level defaults that a
// CodeHubWorkspace instance inherits when it references this class via
// spec.classRef. Every field is optional. The reconciler merges a referenced
// Class into each Workspace with Workspace values winning over Class values,
// so a Class is a "default image sheet", not a mandatory template.
type CodeHubWorkspaceClassSpec struct {
	// Image is the default container image used when a referring Workspace
	// does not set one.
	// +optional
	Image string `json:"image,omitempty"`

	// ImagePullPolicy for the container.
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// ServicePort is the default port the Service exposes.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	ServicePort int32 `json:"servicePort,omitempty"`

	// ContainerPort is the default port the container listens on.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	ContainerPort int32 `json:"containerPort,omitempty"`

	// IdleTimeoutSeconds is the default idle timeout before scale-down.
	// +kubebuilder:validation:Minimum=60
	// +optional
	IdleTimeoutSeconds int64 `json:"idleTimeoutSeconds,omitempty"`

	// Resources are the default container resource requirements.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=chwsc
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CodeHubWorkspaceClass is a cluster-scoped bundle of defaults applied to any
// CodeHubWorkspace that references it via spec.classRef. Platform admins
// curate one or more Classes (e.g. "python-small", "node-large") and users
// pick one in their Workspace instead of copy/pasting image + resources +
// port defaults into every manifest.
type CodeHubWorkspaceClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec CodeHubWorkspaceClassSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// CodeHubWorkspaceClassList contains a list of CodeHubWorkspaceClass.
type CodeHubWorkspaceClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CodeHubWorkspaceClass `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CodeHubWorkspaceClass{}, &CodeHubWorkspaceClassList{})
}
