package composition

import (
	"fmt"

	compositionCondition "github.com/krateoplatformops/composition-dynamic-controller/internal/condition"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/tools/dynamic"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/tools/processor"
	"github.com/krateoplatformops/plumbing/maps"

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
	chartURL       string
	chartVersion   string
	resources      []processor.MinimalMetadata
	previousDigest string
	digest         string
	message        string
	conditionType  ConditionType
}

func (h *handler) setStatus(mg *unstructured.Unstructured, opts *statusManagerOpts) error {
	if opts == nil {
		return fmt.Errorf("status manager options are nil")
	}

	if len(opts.resources) > 0 {
		managed, err := h.populateManagedResources(opts.resources)
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

func (h *handler) populateManagedResources(resources []processor.MinimalMetadata) ([]any, error) {
	var managed []interface{}
	for _, ref := range resources {
		gvr, err := h.pluralizer.GVKtoGVR(schema.FromAPIVersionAndKind(ref.GetAPIVersion(), ref.GetKind()))
		if err != nil {
			return nil, fmt.Errorf("getting GVR for %s/%s with name %s and namespace %s: %w", ref.GetAPIVersion(), ref.GetKind(), ref.GetName(), ref.GetNamespace(), err)
		}

		gvk := schema.FromAPIVersionAndKind(ref.GetAPIVersion(), ref.GetKind())
		isNamespaced, err := dynamic.IsNamespaced(h.mapper, gvk)
		if err != nil {
			return nil, fmt.Errorf("getting REST mapping for %s: %w", gvk.String(), err)
		}
		if !isNamespaced {
			ref.SetNamespace("")
		}

		buildpath := func() string {
			prefix := "/apis/" + gvr.Group + "/" + gvr.Version
			// Core group resources
			if len(gvr.Group) == 0 {
				prefix = "/api/" + gvr.Version
			}

			suffix := "/namespaces/" + ref.GetNamespace() + "/" + gvr.Resource + "/" + ref.GetName()
			// Cluster scoped resources
			if len(ref.GetNamespace()) == 0 {
				suffix = "/" + gvr.Resource + "/" + ref.GetName()
			}
			if len(gvr.Group) == 0 {
				return prefix + suffix
			}
			return prefix + suffix
		}

		managed = append(managed, ManagedResource{
			APIVersion: ref.GetAPIVersion(),
			Resource:   gvr.Resource,
			Name:       ref.GetName(),
			Namespace:  ref.GetNamespace(),
			Path:       buildpath(),
		})
	}

	return managed, nil
}
