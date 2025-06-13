package helmchart

import (
	"bytes"
	"context"
	"fmt"
	"strings"

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
	"k8s.io/apimachinery/pkg/runtime"
	sigsyaml "sigs.k8s.io/yaml"

	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
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
	out := new(bytes.Buffer)

	// We ignore a potential error here because, when the --debug flag was specified,
	// we always want to print the YAML, even if it is not valid. The error is still returned afterwards.
	if rel != nil {
		var manifests bytes.Buffer
		fmt.Fprintln(&manifests, strings.TrimSpace(rel.Manifest))

		for _, m := range rel.Hooks {
			fmt.Fprintf(&manifests, "---\n# Source: %s\n%s\n", m.Path, m.Manifest)
		}

		// if we have a list of files to render, then check that each of the
		// provided files exists in the chart.
		fmt.Fprintf(out, "%s", manifests.String())
	}

	all := []objectref.ObjectRef{}
	tpl := out.Bytes()

	for _, spec := range strings.Split(string(tpl), "---") {
		if len(spec) == 0 {
			continue
		}

		decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader([]byte(spec)), 100)

		var rawObj runtime.RawExtension
		if err := decoder.Decode(&rawObj); err != nil {
			return all, err
		}
		if rawObj.Raw == nil {
			continue
		}

		obj, gvk, err := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme).Decode(rawObj.Raw, nil, nil)
		if err != nil {
			return all, err
		}

		unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if err != nil {
			return all, err
		}

		unstructuredObj := &unstructured.Unstructured{Object: unstructuredMap}
		if unstructuredObj.GetNamespace() == "" {
			unstructuredObj.SetNamespace(defaultNamespace)
		}

		_, ok, err := unstructured.NestedString(unstructuredMap, "metadata", "annotations", "helm.sh/hook")
		if ok || err != nil {
			continue
		}

		apiVersion, kind := gvk.ToAPIVersionAndKind()
		all = append(all, objectref.ObjectRef{
			APIVersion: apiVersion,
			Kind:       kind,
			Name:       unstructuredObj.GetName(),
			Namespace:  unstructuredObj.GetNamespace(),
		})
	}

	return all, nil
}

type CheckResourceOptions struct {
	DynamicClient dynamic.Interface
	Pluralizer    pluralizer.PluralizerInterface
	// DiscoveryClient discovery.DiscoveryInterface
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
	all, err := hc.ListDeployedReleases()
	if err != nil {
		return nil, err
	}

	var res *release.Release
	for _, el := range all {
		if name == el.Name {
			res = el
			break
		}
	}

	return res, nil
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
