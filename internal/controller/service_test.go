package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	runtimev1alpha1 "github.com/cagojeiger/code-hub-operator/api/v1alpha1"
)

func TestBuildService_Shape(t *testing.T) {
	cr := &runtimev1alpha1.CodeHubRuntime{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "ns"},
		Spec: runtimev1alpha1.CodeHubRuntimeSpec{
			ServicePort:   80,
			ContainerPort: 8080,
		},
	}

	svc := buildService(cr)

	require.Equal(t, "demo", svc.Name)
	require.Equal(t, "ns", svc.Namespace)
	require.Equal(t, corev1.ServiceTypeClusterIP, svc.Spec.Type)
	require.Equal(t, map[string]string{
		"app.kubernetes.io/name":     "codehubruntime",
		"app.kubernetes.io/instance": "demo",
	}, svc.Spec.Selector)

	require.Len(t, svc.Spec.Ports, 1)
	p := svc.Spec.Ports[0]
	require.Equal(t, int32(80), p.Port)
	require.Equal(t, int32(8080), p.TargetPort.IntVal)
	require.Equal(t, corev1.ProtocolTCP, p.Protocol)
}

func TestBuildService_SelectorMatchesDeployment(t *testing.T) {
	// The Service selector must match the Deployment pod labels, otherwise
	// no endpoints will be produced. This guards against someone changing
	// one and forgetting the other.
	cr := &runtimev1alpha1.CodeHubRuntime{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "ns"},
		Spec: runtimev1alpha1.CodeHubRuntimeSpec{
			Image:         "i",
			ServicePort:   80,
			ContainerPort: 8080,
		},
	}

	svc := buildService(cr)
	dep := buildDeployment(cr, 1)

	require.Equal(t, svc.Spec.Selector, dep.Spec.Selector.MatchLabels)
	require.Equal(t, svc.Spec.Selector, dep.Spec.Template.Labels)
}

func TestServicePortsEqual(t *testing.T) {
	a := []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP}}
	b := []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP}}
	require.True(t, servicePortsEqual(a, b))

	c := []corev1.ServicePort{{Port: 81, Protocol: corev1.ProtocolTCP}}
	require.False(t, servicePortsEqual(a, c))

	d := []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolUDP}}
	require.False(t, servicePortsEqual(a, d))
}

func TestSelectorsEqual(t *testing.T) {
	require.True(t, selectorsEqual(map[string]string{"a": "1"}, map[string]string{"a": "1"}))
	require.False(t, selectorsEqual(map[string]string{"a": "1"}, map[string]string{"a": "2"}))
	require.False(t, selectorsEqual(map[string]string{"a": "1"}, map[string]string{"a": "1", "b": "2"}))
}
