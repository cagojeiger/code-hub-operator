package controller

import (
	runtimev1alpha1 "github.com/cagojeiger/code-hub-operator/api/v1alpha1"
)

// Well-known labels applied to all resources created for a CodeHubRuntime.
const (
	labelName      = "app.kubernetes.io/name"
	labelInstance  = "app.kubernetes.io/instance"
	labelManagedBy = "app.kubernetes.io/managed-by"

	kindLabelValue = "codehubruntime"
	managedByValue = "code-hub-operator"

	defaultContainerName = "runtime"
)

// podLabels returns the selector labels used on the owned Deployment's pod
// template and Service selector. They MUST be stable across reconciles
// because they are part of the immutable Deployment.Spec.Selector.
func podLabels(cr *runtimev1alpha1.CodeHubRuntime) map[string]string {
	return map[string]string{
		labelName:     kindLabelValue,
		labelInstance: cr.Name,
	}
}

// objectLabels returns the labels stamped on generated objects (superset of
// podLabels, adds managed-by).
func objectLabels(cr *runtimev1alpha1.CodeHubRuntime) map[string]string {
	l := podLabels(cr)
	l[labelManagedBy] = managedByValue
	return l
}
