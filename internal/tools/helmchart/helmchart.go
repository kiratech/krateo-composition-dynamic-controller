package helmchart

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/krateoplatformops/plumbing/maps"
	"github.com/krateoplatformops/unstructured-runtime/pkg/pluralizer"
	unstructuredtools "github.com/krateoplatformops/unstructured-runtime/pkg/tools/unstructured"

	"github.com/krateoplatformops/composition-dynamic-controller/internal/helmclient"
	"github.com/krateoplatformops/unstructured-runtime/pkg/controller/objectref"
	"github.com/krateoplatformops/unstructured-runtime/pkg/meta"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigsyaml "sigs.k8s.io/yaml"

	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
)

func ExtractValuesFromSpec(un *unstructured.Unstructured) ([]byte, error) {
	if un == nil {
		return nil, nil
	}

	spec, ok, err := unstructured.NestedMap(un.UnstructuredContent(), "spec")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	return sigsyaml.Marshal(spec)
}

func AddOrUpdateFieldInValues(values []byte, value interface{}, fields ...string) ([]byte, error) {
	var valuesMap map[string]interface{}
	if err := sigsyaml.Unmarshal(values, &valuesMap); err != nil {
		return nil, err
	}

	// Recursive function to add the value to the map creating nested maps if needed
	var addOrUpdateField func(map[string]interface{}, []string, interface{}) error
	addOrUpdateField = func(m map[string]interface{}, fields []string, value interface{}) error {
		if len(fields) == 1 {
			m[fields[0]] = value
			return nil
		}

		if _, ok := m[fields[0]]; !ok {
			m[fields[0]] = map[string]interface{}{}
		}

		if nestedMap, ok := m[fields[0]].(map[string]interface{}); ok {
			return addOrUpdateField(nestedMap, fields[1:], value)
		} else {
			return fmt.Errorf("field %s is not a map", fields[0])
		}
	}

	if err := addOrUpdateField(valuesMap, fields, value); err != nil {
		return nil, err
	}

	return sigsyaml.Marshal(valuesMap)
}

type RenderTemplateOptions struct {
	HelmClient     helmclient.Client
	PackageUrl     string
	PackageVersion string
	Resource       *unstructured.Unstructured
	Repo           string
	Credentials    *Credentials
	Pluralizer     pluralizer.PluralizerInterface
}

func RenderTemplate(ctx context.Context, opts RenderTemplateOptions) (*release.Release, []objectref.ObjectRef, error) {
	dat, err := ExtractValuesFromSpec(opts.Resource)
	if err != nil {
		return nil, nil, err
	}

	chartSpec := helmclient.ChartSpec{
		ReleaseName: meta.GetReleaseName(opts.Resource),
		Namespace:   opts.Resource.GetNamespace(),
		ChartName:   opts.PackageUrl,
		Version:     opts.PackageVersion,
		ValuesYaml:  string(dat),
		Repo:        opts.Repo,
	}
	if opts.Credentials != nil {
		chartSpec.Username = opts.Credentials.Username
		chartSpec.Password = opts.Credentials.Password
	}

	rel, err := opts.HelmClient.TemplateChartRaw(&chartSpec, nil)
	if err != nil {
		return nil, nil, err
	}

	all, err := GetResourcesRefFromRelease(rel, opts.Resource.GetNamespace())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get resources from release: %w", err)
	}

	return rel, all, nil
}

func GetResourcesRefFromRelease(rel *release.Release, defaultNamespace string) ([]objectref.ObjectRef, error) {
	// build an io.Reader that streams manifest + hooks without concatenating into a single []byte
	var readers []io.Reader
	if rel != nil {
		if strings.TrimSpace(rel.Manifest) != "" {
			readers = append(readers, strings.NewReader(rel.Manifest))
		}
		for _, h := range rel.Hooks {
			// include hook marker to preserve document boundary
			hdr := fmt.Sprintf("\n---\n# Source: %s\n", h.Path)
			readers = append(readers, strings.NewReader(hdr+h.Manifest))
		}
	}

	all := []objectref.ObjectRef{}
	if len(readers) == 0 {
		return all, nil
	}

	combined := io.MultiReader(readers...)
	decoder := yamlutil.NewYAMLOrJSONDecoder(combined, 4096)
	for {
		var doc map[string]interface{}
		if err := decoder.Decode(&doc); err != nil {
			if err == io.EOF {
				break
			}
			// skip invalid doc but continue processing others
			continue
		}
		if doc == nil {
			continue
		}

		// extract minimal metadata without building runtime.Object using maps helper
		apiVersion, _ := maps.NestedString(doc, "apiVersion")
		kind, _ := maps.NestedString(doc, "kind")
		name, _ := maps.NestedString(doc, "metadata", "name")
		namespace, _ := maps.NestedString(doc, "metadata", "namespace")
		hook, _ := maps.NestedString(doc, "metadata", "annotations", "helm.sh/hook")

		if namespace == "" {
			namespace = defaultNamespace
		}
		// skip helm hooks if present
		if hook != "" {
			continue
		}
		if apiVersion == "" && kind == "" && name == "" {
			continue
		}

		all = append(all, objectref.ObjectRef{
			APIVersion: apiVersion,
			Kind:       kind,
			Name:       name,
			Namespace:  namespace,
		})

		apiVersion = ""
		kind = ""
		name = ""
		namespace = ""
		hook = ""
	}
	return all, nil
}

type CheckResourceOptions struct {
	DynamicClient dynamic.Interface
	Pluralizer    pluralizer.PluralizerInterface
}

func CheckResource(ctx context.Context, ref objectref.ObjectRef, opts CheckResourceOptions) (*objectref.ObjectRef, error) {
	gvr, err := opts.Pluralizer.GVKtoGVR(schema.FromAPIVersionAndKind(ref.APIVersion, ref.Kind))
	if err != nil {
		return nil, err
	}

	un, err := opts.DynamicClient.Resource(gvr).
		Namespace(ref.Namespace).
		Get(ctx, ref.Name, metav1.GetOptions{})
	if un == nil {
		// Try to get the resource without the namespace. This is useful for cluster-scoped resources.
		un, err = opts.DynamicClient.Resource(gvr).
			Get(ctx, ref.Name, metav1.GetOptions{})
	}

	if err != nil {
		return nil, err
	}

	_, err = unstructuredtools.IsAvailable(un)
	if err != nil {
		if ex, ok := err.(*unstructuredtools.NotAvailableError); ok {
			return ex.FailedObjectRef, ex.Err
		}
	}

	return &ref, err
}

func FindRelease(hc helmclient.Client, name string) (*release.Release, error) {
	rel, err := hc.GetRelease(name)
	if err != nil {
		if strings.Contains(err.Error(), "release: not found") {
			return nil, nil
		}
		return nil, err
	}
	return rel, nil
}

func FindAllReleases(hc helmclient.Client) ([]*release.Release, error) {
	all, err := hc.ListReleasesByStateMask(action.ListAll)
	if err != nil {
		return nil, err
	}

	res := make([]*release.Release, 0, len(all))
	for _, el := range all {
		res = append(res, el)
	}

	return res, nil
}

func FindAnyRelease(hc helmclient.Client, name string) (*release.Release, error) {
	all, err := FindAllReleases(hc)
	if err != nil {
		return nil, fmt.Errorf("failed to list releases: %w", err)
	}

	for _, el := range all {
		if name == el.Name {
			return el, nil
		}
	}

	return nil, nil
}
