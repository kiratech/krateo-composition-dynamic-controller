package helmchart

import (
	"context"
	"fmt"
	"strings"

	"github.com/gertd/go-pluralize"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/controller"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/helmclient"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/text"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/tools"
	unstructuredtools "github.com/krateoplatformops/composition-dynamic-controller/internal/tools/unstructured"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/kubectl/pkg/scheme"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

type PackageInfo struct {
	// URL of the helm chart package that is being requested.
	URL string `json:"url"`

	// Version of the chart release.
	// +optional
	Version *string `json:"version,omitempty"`
}

type PackageInfoGetter interface {
	GetPackage(ctx context.Context) (PackageInfo, error)
}

var _ PackageInfoGetter = (*staticPackageInfoGetter)(nil)

type staticPackageInfoGetter struct {
	chartName string
}

func (pig staticPackageInfoGetter) GetPackage(ctx context.Context) (PackageInfo, error) {
	return PackageInfo{
		URL: pig.chartName,
	}, nil
}

func NewStaticPackageInfoGetter(chart string) PackageInfoGetter {
	return staticPackageInfoGetter{chartName: chart}
}

func DeriveGroupVersionKind(cli helmclient.Client, url string) (schema.GroupVersionKind, error) {
	chart, _, err := cli.GetChart(url, &action.ChartPathOptions{})
	if err != nil {
		return schema.GroupVersionKind{}, err
	}

	name := chart.Metadata.Name
	version := chart.Metadata.Version

	pc := pluralize.NewClient()
	plural := strings.ToLower(pc.Plural(name))

	gvk := schema.GroupVersionKind{
		Group:   fmt.Sprintf("%s.krateo.io", plural),
		Version: fmt.Sprintf("v%s", strings.ReplaceAll(version, ".", "-")),
		Kind:    text.ToGolangName(name),
	}

	return gvk, nil
}

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

	return yaml.Marshal(spec)
}

type RenderTemplateOptions struct {
	HelmClient helmclient.Client
	PackageURL string
	Resource   *unstructured.Unstructured
}

func RenderTemplate(ctx context.Context, opts RenderTemplateOptions) ([]controller.ObjectRef, error) {
	dat, err := ExtractValuesFromSpec(opts.Resource)
	if err != nil {
		return nil, err
	}

	chartSpec := helmclient.ChartSpec{
		ReleaseName: opts.Resource.GetName(),
		Namespace:   opts.Resource.GetNamespace(),
		ChartName:   opts.PackageURL,
		ValuesYaml:  string(dat),
	}

	tpl, err := opts.HelmClient.TemplateChart(&chartSpec, nil)
	if err != nil {
		return nil, err
	}

	all := []controller.ObjectRef{}

	decode := scheme.Codecs.UniversalDeserializer().Decode
	for _, spec := range strings.Split(string(tpl), "---") {
		if len(spec) == 0 {
			continue
		}
		obj, gvk, err := decode([]byte(spec), nil, nil)
		if err != nil {
			return all, err
		}

		el, ok := obj.(object)
		if !ok {
			continue
		}

		apiVersion, kind := gvk.ToAPIVersionAndKind()
		all = append(all, controller.ObjectRef{
			APIVersion: apiVersion,
			Kind:       kind,
			Name:       el.GetName(),
			Namespace:  el.GetNamespace(),
		})
	}

	return all, nil
}

type CheckResourceOptions struct {
	DynamicClient   dynamic.Interface
	DiscoveryClient *discovery.DiscoveryClient
}

func CheckResource(ctx context.Context, ref controller.ObjectRef, opts CheckResourceOptions) (*controller.ObjectRef, error) {
	gvr, err := tools.GVKtoGVR(opts.DiscoveryClient, schema.FromAPIVersionAndKind(ref.APIVersion, ref.Kind))
	if err != nil {
		return nil, err
	}

	un, err := opts.DynamicClient.Resource(gvr).
		Namespace(ref.Namespace).
		Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	_, err = unstructuredtools.IsAvailable(un)
	if err != nil {
		if ex, ok := err.(*unstructuredtools.NotAvailableError); ok {
			return ex.FailedObjectRef, ex.Err
		}
	}

	return nil, err
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

type object interface {
	metav1.Object
	runtime.Object
}
