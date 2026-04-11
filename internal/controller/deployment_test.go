package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	runtimev1alpha1 "github.com/cagojeiger/code-hub-operator/api/v1alpha1"
)

func TestBuildDeployment_BasicShape(t *testing.T) {
	cr := &runtimev1alpha1.CodeHubRuntime{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "ns"},
		Spec: runtimev1alpha1.CodeHubRuntimeSpec{
			Image:         "ghcr.io/x/y:1.0",
			ContainerPort: 8080,
		},
	}

	dep := buildDeployment(cr, 1)

	require.Equal(t, "demo", dep.Name)
	require.Equal(t, "ns", dep.Namespace)
	require.NotNil(t, dep.Spec.Replicas)
	require.Equal(t, int32(1), *dep.Spec.Replicas)

	wantSelector := map[string]string{
		"app.kubernetes.io/name":     "codehubruntime",
		"app.kubernetes.io/instance": "demo",
	}
	require.Equal(t, wantSelector, dep.Spec.Selector.MatchLabels)
	require.Equal(t, wantSelector, dep.Spec.Template.Labels)

	require.Contains(t, dep.Labels, "app.kubernetes.io/managed-by")
	require.Equal(t, "code-hub-operator", dep.Labels["app.kubernetes.io/managed-by"])

	require.Len(t, dep.Spec.Template.Spec.Containers, 1)
	c := dep.Spec.Template.Spec.Containers[0]
	require.Equal(t, "runtime", c.Name)
	require.Equal(t, "ghcr.io/x/y:1.0", c.Image)
	require.Equal(t, corev1.PullIfNotPresent, c.ImagePullPolicy)
	require.Len(t, c.Ports, 1)
	require.Equal(t, int32(8080), c.Ports[0].ContainerPort)
	require.Equal(t, corev1.ProtocolTCP, c.Ports[0].Protocol)
}

func TestBuildDeployment_HonorsExplicitPullPolicy(t *testing.T) {
	cr := &runtimev1alpha1.CodeHubRuntime{
		ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "ns"},
		Spec: runtimev1alpha1.CodeHubRuntimeSpec{
			Image:           "i",
			ContainerPort:   1,
			ImagePullPolicy: corev1.PullAlways,
		},
	}
	c := buildDeployment(cr, 1).Spec.Template.Spec.Containers[0]
	require.Equal(t, corev1.PullAlways, c.ImagePullPolicy)
}

func TestBuildDeployment_EnvIsDeterministic(t *testing.T) {
	cr := &runtimev1alpha1.CodeHubRuntime{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "ns"},
		Spec: runtimev1alpha1.CodeHubRuntimeSpec{
			Image:         "img",
			ContainerPort: 80,
			Env: map[string]string{
				"Z": "1",
				"A": "2",
				"M": "3",
			},
		},
	}

	// Build twice and require identical output — this would be flaky if we
	// iterated the map without sorting.
	a := buildDeployment(cr, 1).Spec.Template.Spec.Containers[0].Env
	b := buildDeployment(cr, 1).Spec.Template.Spec.Containers[0].Env
	require.Equal(t, a, b)

	require.Equal(t, []corev1.EnvVar{
		{Name: "A", Value: "2"},
		{Name: "M", Value: "3"},
		{Name: "Z", Value: "1"},
	}, a)
}

func TestBuildDeployment_ReplicasRespected(t *testing.T) {
	cr := &runtimev1alpha1.CodeHubRuntime{
		ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "ns"},
		Spec:       runtimev1alpha1.CodeHubRuntimeSpec{Image: "i", ContainerPort: 1},
	}
	require.Equal(t, int32(0), *buildDeployment(cr, 0).Spec.Replicas)
	require.Equal(t, int32(1), *buildDeployment(cr, 1).Spec.Replicas)
}

func TestBuildDeployment_ResourcesApplied(t *testing.T) {
	cr := &runtimev1alpha1.CodeHubRuntime{
		ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "ns"},
		Spec: runtimev1alpha1.CodeHubRuntimeSpec{
			Image:         "i",
			ContainerPort: 1,
			Resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("250m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("500m"),
				},
			},
		},
	}
	res := buildDeployment(cr, 1).Spec.Template.Spec.Containers[0].Resources
	cpu := res.Requests[corev1.ResourceCPU]
	mem := res.Requests[corev1.ResourceMemory]
	lim := res.Limits[corev1.ResourceCPU]
	require.Equal(t, "250m", cpu.String())
	require.Equal(t, "128Mi", mem.String())
	require.Equal(t, "500m", lim.String())
}

func TestValidateForDeployment(t *testing.T) {
	cases := []struct {
		name    string
		cr      *runtimev1alpha1.CodeHubRuntime
		wantErr bool
	}{
		{
			name: "ok",
			cr: &runtimev1alpha1.CodeHubRuntime{
				Spec: runtimev1alpha1.CodeHubRuntimeSpec{
					Image: "i", ContainerPort: 80, ServicePort: 80, MinReplicas: 0, MaxReplicas: 1,
				},
			},
		},
		{
			name: "missing image",
			cr: &runtimev1alpha1.CodeHubRuntime{
				Spec: runtimev1alpha1.CodeHubRuntimeSpec{ContainerPort: 80, ServicePort: 80},
			},
			wantErr: true,
		},
		{
			name: "bad container port",
			cr: &runtimev1alpha1.CodeHubRuntime{
				Spec: runtimev1alpha1.CodeHubRuntimeSpec{Image: "i", ServicePort: 80},
			},
			wantErr: true,
		},
		{
			name: "bad service port",
			cr: &runtimev1alpha1.CodeHubRuntime{
				Spec: runtimev1alpha1.CodeHubRuntimeSpec{Image: "i", ContainerPort: 80},
			},
			wantErr: true,
		},
		{
			name: "max < min",
			cr: &runtimev1alpha1.CodeHubRuntime{
				Spec: runtimev1alpha1.CodeHubRuntimeSpec{
					Image: "i", ContainerPort: 80, ServicePort: 80, MinReplicas: 1, MaxReplicas: 0,
				},
			},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateForDeployment(tc.cr)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPodTemplateEquivalent(t *testing.T) {
	base := &runtimev1alpha1.CodeHubRuntime{
		ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "ns"},
		Spec: runtimev1alpha1.CodeHubRuntimeSpec{
			Image: "i:1", ContainerPort: 80,
			Env: map[string]string{"A": "1"},
		},
	}
	a := buildDeployment(base, 1)
	b := buildDeployment(base, 0) // replicas differ but template should match
	require.True(t, podTemplateEquivalent(a, b))

	// Image change should break equivalence.
	changed := base.DeepCopy()
	changed.Spec.Image = "i:2"
	c := buildDeployment(changed, 1)
	require.False(t, podTemplateEquivalent(a, c))

	// Env change should break equivalence.
	envChanged := base.DeepCopy()
	envChanged.Spec.Env = map[string]string{"A": "2"}
	d := buildDeployment(envChanged, 1)
	require.False(t, podTemplateEquivalent(a, d))

	// Resources change should break equivalence.
	resChanged := base.DeepCopy()
	resChanged.Spec.Resources = &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("250m"),
		},
	}
	e := buildDeployment(resChanged, 1)
	require.False(t, podTemplateEquivalent(a, e))
}
