package composition

import (
	"fmt"

	compositionCondition "github.com/krateoplatformops/composition-dynamic-controller/internal/condition"
	"github.com/krateoplatformops/plumbing/maps"

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

func setAvaibleStatus(mg *unstructured.Unstructured, message string, force bool) error {
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

func setGracefullyPausedCondition(mg *unstructured.Unstructured, force bool) error {
	if !force {
		currentCondition := unstructuredtools.GetCondition(mg, compositionCondition.ReconcileGracefullyPaused().Type, compositionCondition.ReconcileGracefullyPaused().Reason)

		if currentCondition != nil && currentCondition.Message == "Composition is gracefully paused." {
			return nil
		}
	}

	cond := compositionCondition.ReconcileGracefullyPaused()
	cond.Message = "Composition is gracefully paused."
	err := unstructuredtools.SetConditions(mg, cond)
	if err != nil {
		return fmt.Errorf("setting condition: %w", err)
	}
	return nil
}

type ConditionType string

const (
	ConditionTypeAvailable                 ConditionType = "Available"
	ConditionTypeReconcileGracefullyPaused ConditionType = "ReconcileGracefullyPaused"
)

type statusManagerOpts struct {
	force          bool
	pluralizer     pluralizer.PluralizerInterface
	chartURL       string
	chartVersion   string
	resources      []objectref.ObjectRef
	previousDigest string
	digest         string
	message        string
	conditionType  ConditionType
}

func setStatus(mg *unstructured.Unstructured, opts *statusManagerOpts) error {
	if opts == nil {
		return fmt.Errorf("status manager options are nil")
	}

	if len(opts.resources) > 0 {
		managed, err := populateManagedResources(opts.pluralizer, opts.resources)
		if err != nil {
			return fmt.Errorf("populating managed resources: %w", err)
		}

		setManagedResources(mg, managed)
	}

	err := maps.SetNestedField(mg.Object, opts.previousDigest, "status", "previousDigest")
	if err != nil {
		return fmt.Errorf("setting previous digest in status: %w", err)
	}

	err = maps.SetNestedField(mg.Object, opts.digest, "status", "digest")
	if err != nil {
		return fmt.Errorf("setting digest in status: %w", err)
	}

	err = maps.SetNestedField(mg.Object, opts.chartURL, "status", "helmChartUrl")
	if err != nil {
		return fmt.Errorf("setting chart URL in status: %w", err)
	}

	err = maps.SetNestedField(mg.Object, opts.chartVersion, "status", "helmChartVersion")
	if err != nil {
		return fmt.Errorf("setting chart version in status: %w", err)
	}

	switch opts.conditionType {
	case ConditionTypeReconcileGracefullyPaused:
		return setGracefullyPausedCondition(mg, opts.force)
	case ConditionTypeAvailable:
		return setAvaibleStatus(mg, opts.message, opts.force)
	}
	return fmt.Errorf("unknown condition type: %s", opts.conditionType)
}

func setManagedResources(mg *unstructured.Unstructured, managed []any) {
	status := mg.Object["status"]
	if status == nil {
		status = map[string]interface{}{}
	}
	mapstatus := status.(map[string]interface{})

	mapstatus["managed"] = managed
	mg.Object["status"] = mapstatus
}

func populateManagedResources(pluralizer pluralizer.PluralizerInterface, resources []objectref.ObjectRef) ([]any, error) {
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
