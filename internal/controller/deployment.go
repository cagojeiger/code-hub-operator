package controller

import (
	"fmt"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	runtimev1alpha1 "github.com/cagojeiger/code-hub-operator/api/v1alpha1"
)

// buildDeployment renders the desired Deployment for a CodeHubRuntime.
// replicas is passed explicitly so the reconciler can decide the value
// from idle state without mutating the CR spec.
func buildDeployment(cr *runtimev1alpha1.CodeHubRuntime, replicas int32) *appsv1.Deployment {
	pullPolicy := cr.Spec.ImagePullPolicy
	if pullPolicy == "" {
		pullPolicy = corev1.PullIfNotPresent
	}

	container := corev1.Container{
		Name:            defaultContainerName,
		Image:           cr.Spec.Image,
		ImagePullPolicy: pullPolicy,
		Ports: []corev1.ContainerPort{{
			Name:          "http",
			ContainerPort: cr.Spec.ContainerPort,
			Protocol:      corev1.ProtocolTCP,
		}},
		Env: envFromMap(cr.Spec.Env),
	}
	if cr.Spec.Resources != nil {
		container.Resources = *cr.Spec.Resources
	}

	selector := podLabels(cr)
	replicasCopy := replicas

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
			Labels:    objectLabels(cr),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicasCopy,
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selector,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{container},
				},
			},
		},
	}
}

// envFromMap converts the user-supplied env map to a deterministically
// ordered slice so reconciles are stable (no spurious diffs from map ordering).
func envFromMap(m map[string]string) []corev1.EnvVar {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]corev1.EnvVar, 0, len(keys))
	for _, k := range keys {
		out = append(out, corev1.EnvVar{Name: k, Value: m[k]})
	}
	return out
}

// validateForDeployment returns an error if the CR cannot be rendered into
// a Deployment. This is a safety net on top of CRD OpenAPI validation.
func validateForDeployment(cr *runtimev1alpha1.CodeHubRuntime) error {
	if cr.Spec.Image == "" {
		return fmt.Errorf("spec.image is required")
	}
	if cr.Spec.ContainerPort <= 0 {
		return fmt.Errorf("spec.containerPort must be > 0")
	}
	if cr.Spec.ServicePort <= 0 {
		return fmt.Errorf("spec.servicePort must be > 0")
	}
	if cr.Spec.MaxReplicas < cr.Spec.MinReplicas {
		return fmt.Errorf("spec.maxReplicas must be >= spec.minReplicas")
	}
	return nil
}

// podTemplateEquivalent is a narrow equality check on the fields that this
// operator actually manages on the Deployment template: container image,
// pull policy, container port, env, and resources. It intentionally ignores
// fields the API server may default, to avoid update churn.
func podTemplateEquivalent(a, b *appsv1.Deployment) bool {
	ac := a.Spec.Template.Spec.Containers
	bc := b.Spec.Template.Spec.Containers
	if len(ac) != len(bc) {
		return false
	}
	for i := range ac {
		if ac[i].Image != bc[i].Image {
			return false
		}
		if ac[i].ImagePullPolicy != bc[i].ImagePullPolicy {
			return false
		}
		if len(ac[i].Ports) != len(bc[i].Ports) {
			return false
		}
		for j := range ac[i].Ports {
			if ac[i].Ports[j].ContainerPort != bc[i].Ports[j].ContainerPort {
				return false
			}
			if ac[i].Ports[j].Protocol != bc[i].Ports[j].Protocol {
				return false
			}
		}
		if !envSlicesEqual(ac[i].Env, bc[i].Env) {
			return false
		}
	}
	return true
}

func envSlicesEqual(a, b []corev1.EnvVar) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Value != b[i].Value {
			return false
		}
	}
	return true
}
