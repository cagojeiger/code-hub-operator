package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	runtimev1alpha1 "github.com/cagojeiger/code-hub-operator/api/v1alpha1"
)

// defaultImagePullPolicy is the final fallback used by applyClassDefaults
// when neither the Workspace nor its referenced Class sets ImagePullPolicy.
const defaultImagePullPolicy = corev1.PullIfNotPresent

// applyClassDefaults mutates ws in place so that any field it left unset is
// populated either from a referenced CodeHubWorkspaceClass or from a built-in
// fallback. Workspace values always win over Class values; Class values
// always win over built-in fallbacks.
//
// The caller is expected to pass a *copy* of the fetched Workspace (via
// DeepCopy) so that the authoritative spec in etcd is not polluted with
// resolved defaults — Status().Update ignores spec mutations thanks to the
// status subresource, but we still never want to persist spec rewrites.
//
// Returns an error only when spec.classRef is set but the Class cannot be
// fetched (missing, RBAC denied, transient API error). A missing classRef
// is a normal no-op.
func applyClassDefaults(ctx context.Context, c client.Client, ws *runtimev1alpha1.CodeHubWorkspace) (*runtimev1alpha1.CodeHubWorkspaceClass, error) {
	var resolved *runtimev1alpha1.CodeHubWorkspaceClass

	if ws.Spec.ClassRef != "" {
		class := &runtimev1alpha1.CodeHubWorkspaceClass{}
		if err := c.Get(ctx, types.NamespacedName{Name: ws.Spec.ClassRef}, class); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("CodeHubWorkspaceClass %q not found", ws.Spec.ClassRef)
			}
			return nil, fmt.Errorf("fetch CodeHubWorkspaceClass %q: %w", ws.Spec.ClassRef, err)
		}
		mergeFromClass(ws, class)
		resolved = class
	}

	// Final fallback for ImagePullPolicy — mirrors the old CRD default that
	// was removed when we relaxed the field to truly optional.
	if ws.Spec.ImagePullPolicy == "" {
		ws.Spec.ImagePullPolicy = defaultImagePullPolicy
	}

	return resolved, nil
}

// mergeFromClass copies Class defaults into a Workspace. Workspace wins on
// every field: a value is only taken from Class when the Workspace left the
// corresponding field at its zero value.
func mergeFromClass(ws *runtimev1alpha1.CodeHubWorkspace, class *runtimev1alpha1.CodeHubWorkspaceClass) {
	if ws.Spec.Image == "" {
		ws.Spec.Image = class.Spec.Image
	}
	if ws.Spec.ImagePullPolicy == "" {
		ws.Spec.ImagePullPolicy = class.Spec.ImagePullPolicy
	}
	if ws.Spec.ServicePort == 0 {
		ws.Spec.ServicePort = class.Spec.ServicePort
	}
	if ws.Spec.ContainerPort == 0 {
		ws.Spec.ContainerPort = class.Spec.ContainerPort
	}
	if ws.Spec.IdleTimeoutSeconds == 0 {
		ws.Spec.IdleTimeoutSeconds = class.Spec.IdleTimeoutSeconds
	}
	if ws.Spec.Resources == nil && class.Spec.Resources != nil {
		ws.Spec.Resources = class.Spec.Resources.DeepCopy()
	}
	// Env is intentionally NOT merged — workspace envs are instance-level
	// and merging would create confusing half-inherited maps.
}
