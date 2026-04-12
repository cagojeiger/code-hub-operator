package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	runtimev1alpha1 "github.com/cagojeiger/code-hub-operator/api/v1alpha1"
)

// newMergeClient builds a fake client with the v1alpha1 scheme registered.
// Caller passes any objects (typically a CodeHubWorkspaceClass) to seed.
func newMergeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, runtimev1alpha1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func TestApplyClassDefaults_NoClassRef_IsNoOp(t *testing.T) {
	ws := &runtimev1alpha1.CodeHubWorkspace{
		Spec: runtimev1alpha1.CodeHubWorkspaceSpec{
			Image:              "ghcr.io/x/y:1",
			ServicePort:        80,
			ContainerPort:      8080,
			IdleTimeoutSeconds: 600,
		},
	}
	c := newMergeClient(t)

	resolved, err := applyClassDefaults(context.Background(), c, ws)
	require.NoError(t, err)
	require.Nil(t, resolved, "no class should be resolved when classRef is empty")
	require.Equal(t, "ghcr.io/x/y:1", ws.Spec.Image)
	require.Equal(t, corev1.PullIfNotPresent, ws.Spec.ImagePullPolicy,
		"ImagePullPolicy must fall back to IfNotPresent even without a class")
}

func TestApplyClassDefaults_ClassMissing_ReturnsError(t *testing.T) {
	ws := &runtimev1alpha1.CodeHubWorkspace{
		Spec: runtimev1alpha1.CodeHubWorkspaceSpec{ClassRef: "nope"},
	}
	c := newMergeClient(t)

	resolved, err := applyClassDefaults(context.Background(), c, ws)
	require.Error(t, err)
	require.Nil(t, resolved)
	require.Contains(t, err.Error(), "nope")
	require.Contains(t, err.Error(), "not found")
}

func TestApplyClassDefaults_ClassFillsUnsetFields(t *testing.T) {
	class := &runtimev1alpha1.CodeHubWorkspaceClass{
		ObjectMeta: metav1.ObjectMeta{Name: "standard"},
		Spec: runtimev1alpha1.CodeHubWorkspaceClassSpec{
			Image:              "ghcr.io/class/image:v2",
			ImagePullPolicy:    corev1.PullAlways,
			ServicePort:        8000,
			ContainerPort:      9000,
			IdleTimeoutSeconds: 1800,
			Resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("100m"),
				},
			},
		},
	}
	ws := &runtimev1alpha1.CodeHubWorkspace{
		Spec: runtimev1alpha1.CodeHubWorkspaceSpec{
			ClassRef:    "standard",
			MinReplicas: 0,
			MaxReplicas: 1,
			LastUsedKey: "k",
		},
	}
	c := newMergeClient(t, class)

	resolved, err := applyClassDefaults(context.Background(), c, ws)
	require.NoError(t, err)
	require.NotNil(t, resolved)
	require.Equal(t, "standard", resolved.Name)

	require.Equal(t, "ghcr.io/class/image:v2", ws.Spec.Image)
	require.Equal(t, corev1.PullAlways, ws.Spec.ImagePullPolicy)
	require.Equal(t, int32(8000), ws.Spec.ServicePort)
	require.Equal(t, int32(9000), ws.Spec.ContainerPort)
	require.Equal(t, int64(1800), ws.Spec.IdleTimeoutSeconds)
	require.NotNil(t, ws.Spec.Resources)
	require.Equal(t, "100m", ws.Spec.Resources.Requests.Cpu().String())
}

func TestApplyClassDefaults_WorkspaceWinsOverClass(t *testing.T) {
	class := &runtimev1alpha1.CodeHubWorkspaceClass{
		ObjectMeta: metav1.ObjectMeta{Name: "standard"},
		Spec: runtimev1alpha1.CodeHubWorkspaceClassSpec{
			Image:              "ghcr.io/class/image:v2",
			ImagePullPolicy:    corev1.PullAlways,
			ServicePort:        8000,
			ContainerPort:      9000,
			IdleTimeoutSeconds: 1800,
		},
	}
	ws := &runtimev1alpha1.CodeHubWorkspace{
		Spec: runtimev1alpha1.CodeHubWorkspaceSpec{
			ClassRef:           "standard",
			Image:              "ghcr.io/user/custom:dev",
			ImagePullPolicy:    corev1.PullNever,
			ServicePort:        80,
			ContainerPort:      8080,
			IdleTimeoutSeconds: 300,
		},
	}
	c := newMergeClient(t, class)

	_, err := applyClassDefaults(context.Background(), c, ws)
	require.NoError(t, err)

	require.Equal(t, "ghcr.io/user/custom:dev", ws.Spec.Image, "Workspace image must win over Class")
	require.Equal(t, corev1.PullNever, ws.Spec.ImagePullPolicy)
	require.Equal(t, int32(80), ws.Spec.ServicePort)
	require.Equal(t, int32(8080), ws.Spec.ContainerPort)
	require.Equal(t, int64(300), ws.Spec.IdleTimeoutSeconds)
}

func TestApplyClassDefaults_PartialOverride(t *testing.T) {
	class := &runtimev1alpha1.CodeHubWorkspaceClass{
		ObjectMeta: metav1.ObjectMeta{Name: "standard"},
		Spec: runtimev1alpha1.CodeHubWorkspaceClassSpec{
			Image:              "ghcr.io/class/image:v2",
			ServicePort:        8000,
			ContainerPort:      9000,
			IdleTimeoutSeconds: 1800,
		},
	}
	ws := &runtimev1alpha1.CodeHubWorkspace{
		Spec: runtimev1alpha1.CodeHubWorkspaceSpec{
			ClassRef: "standard",
			// only Image is overridden; everything else should come from Class
			Image: "ghcr.io/user/custom:dev",
		},
	}
	c := newMergeClient(t, class)

	_, err := applyClassDefaults(context.Background(), c, ws)
	require.NoError(t, err)

	require.Equal(t, "ghcr.io/user/custom:dev", ws.Spec.Image)
	require.Equal(t, int32(8000), ws.Spec.ServicePort)
	require.Equal(t, int32(9000), ws.Spec.ContainerPort)
	require.Equal(t, int64(1800), ws.Spec.IdleTimeoutSeconds)
	// ImagePullPolicy: class did not set it, workspace did not set it — fallback
	require.Equal(t, corev1.PullIfNotPresent, ws.Spec.ImagePullPolicy)
}

func TestApplyClassDefaults_EnvIsNeverMerged(t *testing.T) {
	// Env is explicitly workspace-only. Class has no Env field, but double-
	// check that a workspace's Env is preserved regardless of class presence.
	class := &runtimev1alpha1.CodeHubWorkspaceClass{
		ObjectMeta: metav1.ObjectMeta{Name: "standard"},
		Spec: runtimev1alpha1.CodeHubWorkspaceClassSpec{
			Image: "ghcr.io/class/image:v2",
		},
	}
	ws := &runtimev1alpha1.CodeHubWorkspace{
		Spec: runtimev1alpha1.CodeHubWorkspaceSpec{
			ClassRef: "standard",
			Env:      map[string]string{"FOO": "bar"},
		},
	}
	c := newMergeClient(t, class)

	_, err := applyClassDefaults(context.Background(), c, ws)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"FOO": "bar"}, ws.Spec.Env)
}
