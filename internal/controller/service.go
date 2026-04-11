package controller

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	runtimev1alpha1 "github.com/cagojeiger/code-hub-operator/api/v1alpha1"
)

// buildService renders the desired Service for a CodeHubRuntime.
func buildService(cr *runtimev1alpha1.CodeHubRuntime) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
			Labels:    objectLabels(cr),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: podLabels(cr),
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       cr.Spec.ServicePort,
				TargetPort: intstr.FromInt32(cr.Spec.ContainerPort),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
}

// servicePortsEqual checks the subset of Service.Spec.Ports that this
// operator actually manages.
func servicePortsEqual(a, b []corev1.ServicePort) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Port != b[i].Port {
			return false
		}
		if a[i].TargetPort != b[i].TargetPort {
			return false
		}
		if a[i].Protocol != b[i].Protocol {
			return false
		}
	}
	return true
}

func selectorsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
