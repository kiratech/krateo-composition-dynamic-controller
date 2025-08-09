package meta

import (
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

	// ReleaseNameLabel is the label used to identify the release name of a Helm chart that can be different from the name of the resource.
	ReleaseNameLabel = "krateo.io/release-name"
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
