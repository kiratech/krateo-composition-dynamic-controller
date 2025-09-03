package composition

import (
	"fmt"

	compositionCondition "github.com/krateoplatformops/composition-dynamic-controller/internal/condition"

	"github.com/krateoplatformops/composition-dynamic-controller/internal/tools/helmchart/archive"
	"github.com/krateoplatformops/unstructured-runtime/pkg/controller/objectref"
	"github.com/krateoplatformops/unstructured-runtime/pkg/pluralizer"
	unstructuredtools "github.com/krateoplatformops/unstructured-runtime/pkg/tools/unstructured"
	"github.com/krateoplatformops/unstructured-runtime/pkg/tools/unstructured/condition"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type ManagedResource struct {
	APIVersion string `json:"apiVersion"`
	Resource   string `json:"resource"`
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Path       string `json:"path"`
}

func setAvaibleStatus(mg *unstructured.Unstructured, pkg *archive.Info, message string, force bool) error {
	if pkg == nil {
		return fmt.Errorf("package info is nil")
	}

	unstructured.SetNestedField(mg.Object, pkg.Version, "status", "helmChartVersion")
	unstructured.SetNestedField(mg.Object, pkg.URL, "status", "helmChartUrl")

	if !force {
		currentCondition := unstructuredtools.GetCondition(mg, condition.Available().Type, condition.Available().Reason)

		if currentCondition != nil && currentCondition.Message == message {
			return nil
		}
	}

	cond := condition.Available()
	cond.Message = message
	err := unstructuredtools.SetConditions(mg, cond)
	if err != nil {
		return fmt.Errorf("setting condition: %w", err)
	}
	return nil
}

func setGracefullyPausedCondition(mg *unstructured.Unstructured, pkg *archive.Info) error {
	if pkg == nil {
		return fmt.Errorf("package info is nil")
	}

	currentCondition := unstructuredtools.GetCondition(mg, compositionCondition.ReconcileGracefullyPaused().Type, compositionCondition.ReconcileGracefullyPaused().Reason)

	if currentCondition != nil && currentCondition.Message == "Composition is gracefully paused." {
		return nil
	}
	cond := compositionCondition.ReconcileGracefullyPaused()
	cond.Message = "Composition is gracefully paused."
	err := unstructuredtools.SetConditions(mg, cond)
	if err != nil {
		return fmt.Errorf("setting condition: %w", err)
	}
	return nil
}

func setManagedResources(mg *unstructured.Unstructured, managed []interface{}) {
	status := mg.Object["status"]
	if status == nil {
		status = map[string]interface{}{}
	}
	mapstatus := status.(map[string]interface{})

	mapstatus["managed"] = managed
	mg.Object["status"] = mapstatus
}

func populateManagedResources(pluralizer pluralizer.PluralizerInterface, resources []objectref.ObjectRef) ([]interface{}, error) {
	var managed []interface{}
	for _, ref := range resources {
		gvr, err := pluralizer.GVKtoGVR(schema.FromAPIVersionAndKind(ref.APIVersion, ref.Kind))
		if err != nil {
			return nil, fmt.Errorf("getting GVR for %s: %w", ref.String(), err)
		}

		buildpath := func() string {
			prefix := "/apis/" + gvr.Group + "/" + gvr.Version
			// Core group resources
			if len(gvr.Group) == 0 {
				prefix = "/api/" + gvr.Version
			}

			suffix := "/namespaces/" + ref.Namespace + "/" + gvr.Resource + "/" + ref.Name
			// Cluster scoped resources
			if len(ref.Namespace) == 0 {
				suffix = "/" + gvr.Resource + "/" + ref.Name
			}
			if len(gvr.Group) == 0 {
				return prefix + suffix
			}
			return prefix + suffix
		}
		managed = append(managed, ManagedResource{
			APIVersion: ref.APIVersion,
			Resource:   gvr.Resource,
			Name:       ref.Name,
			Namespace:  ref.Namespace,
			Path:       buildpath(),
		})
	}

	return managed, nil
}
