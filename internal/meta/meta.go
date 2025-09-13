package meta

import (
	"time"

	"github.com/krateoplatformops/unstructured-runtime/pkg/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// Labels for Krateo Composition
	CompositionDefinitionNameLabel      = "krateo.io/composition-definition-name"
	CompositionDefinitionNamespaceLabel = "krateo.io/composition-definition-namespace"
	CompositionDefinitionGroupLabel     = "krateo.io/composition-definition-group"
	CompositionDefinitionVersionLabel   = "krateo.io/composition-definition-version"
	CompositionDefinitionResourceLabel  = "krateo.io/composition-definition-resource"
	CompositionVersionLabel             = "krateo.io/composition-version"

	// ReleaseNameLabel is the label used to identify the release name of a Helm chart that can be different from the name of the resource.
	ReleaseNameLabel = "krateo.io/release-name"

	// AnnotationKeyReconciliationGracefullyPaused is the key in the annotations map
	// that indicates whether the reconciliation of the resource is gracefully paused.
	AnnotationKeyReconciliationGracefullyPaused = "krateo.io/gracefully-paused"

	// AnnotationKeyReconciliationGracefullyPausedTime is the key in the annotations map
	// that indicates the time when the reconciliation was gracefully paused.
	// This is used to track how long the resource has been paused.
	AnnotationKeyReconciliationGracefullyPausedTime = "krateo.io/gracefully-paused-time"
)

func GetReleaseName(o metav1.Object) string {
	return o.GetLabels()[ReleaseNameLabel]
}

// Set the release name as a label on the Composition resource.
// Release name will be "name" if the annotation has not been already populated.
func SetReleaseName(o metav1.Object, name string) {
	mglabels := o.GetLabels()
	if mglabels == nil {
		mglabels = make(map[string]string)
	}
	if _, ok := mglabels[ReleaseNameLabel]; !ok {
		mglabels[ReleaseNameLabel] = name
	}
	o.SetLabels(mglabels)
}

type CompositionDefinitionInfo struct {
	Namespace string
	Name      string
	GVR       schema.GroupVersionResource
}

// SetCompositionDefinitionLabels sets the labels for the Composition Definition on the given object.
// It sets the labels for the composition definition name, namespace, group, version, and resource.
// If the labels already exist, they will be updated.
// If the labels do not exist, they will be created.
func SetCompositionDefinitionLabels(o metav1.Object, cdInfo CompositionDefinitionInfo) {
	labels := o.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}

	labels[CompositionDefinitionNameLabel] = cdInfo.Name
	labels[CompositionDefinitionNamespaceLabel] = cdInfo.Namespace
	labels[CompositionDefinitionGroupLabel] = cdInfo.GVR.Group
	labels[CompositionDefinitionVersionLabel] = cdInfo.GVR.Version
	labels[CompositionDefinitionResourceLabel] = cdInfo.GVR.Resource

	o.SetLabels(labels)
}

// IsGracefullyPaused returns true if the object has the AnnotationKeyGracefullyPaused
// annotation set to `true`.
func IsGracefullyPaused(o metav1.Object) bool {
	return o.GetAnnotations()[AnnotationKeyReconciliationGracefullyPaused] == "true"
}

func SetGracefullyPausedTime(o metav1.Object, t time.Time) {
	meta.AddAnnotations(o, map[string]string{AnnotationKeyReconciliationGracefullyPausedTime: t.Format(time.RFC3339)})
}

func GetGracefullyPausedTime(o metav1.Object) (time.Time, bool) {
	t, ok := o.GetAnnotations()[AnnotationKeyReconciliationGracefullyPausedTime]
	if !ok {
		return time.Time{}, false
	}
	pausedTime, err := time.Parse(time.RFC3339, t)
	if err != nil {
		return time.Time{}, false
	}
	return pausedTime, true
}
