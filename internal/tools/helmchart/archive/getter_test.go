//go:build integration
// +build integration

package archive_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/gobuffalo/flect"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/tools/helmchart/archive"
	"github.com/krateoplatformops/plumbing/e2e"
	xenv "github.com/krateoplatformops/plumbing/env"
	"github.com/krateoplatformops/unstructured-runtime/pkg/pluralizer"
	apis "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/scheme"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	cacheddiscovery "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/e2e-framework/klient/decoder"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"
	"sigs.k8s.io/e2e-framework/pkg/features"
	"sigs.k8s.io/e2e-framework/support/kind"
)

var (
	testenv     env.Environment
	clusterName string
	namespace   string
)

const (
	testdataPath = "../../../../testdata"
)

type FakePluralizer struct {
}

var _ pluralizer.PluralizerInterface = &FakePluralizer{}

func (p FakePluralizer) GVKtoGVR(gvk schema.GroupVersionKind) (schema.GroupVersionResource, error) {
	return schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: flect.Pluralize(strings.ToLower(gvk.Kind)),
	}, nil
}

func TestMain(m *testing.M) {
	xenv.SetTestMode(true)

	namespace = "demo-system"
	altNamespace := "krateo-system"
	clusterName = "krateo"
	testenv = env.New()

	testenv.Setup(
		envfuncs.CreateCluster(kind.NewProvider(), clusterName),
		e2e.CreateNamespace(namespace),
		e2e.CreateNamespace(altNamespace),

		func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			r, err := resources.New(cfg.Client().RESTConfig())
			if err != nil {
				return ctx, err
			}

			r.WithNamespace(namespace)

			return ctx, nil
		},
	).Finish(
		envfuncs.DeleteNamespace(namespace),
		envfuncs.DestroyCluster(clusterName),
	)

	os.Exit(testenv.Run(m))
}

func TestGetter(t *testing.T) {
	os.Setenv("DEBUG", "1")
	tests := []struct {
		compositionDefinition string
		composition           string
	}{
		{
			compositionDefinition: "fireworksapp.yaml",
			composition:           "fireworksapp.yaml",
		},
	}

	f := features.New("Setup").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			r, err := resources.New(cfg.Client().RESTConfig())
			if err != nil {
				t.Fail()
			}

			err = decoder.DecodeEachFile(
				ctx, os.DirFS(filepath.Join(testdataPath, "crds", "finops")), "*.yaml",
				decoder.CreateIgnoreAlreadyExists(r),
			)
			if err != nil {
				t.Log("Error decoding CRDs: ", err)
				t.Fail()
			}

			err = decoder.DecodeEachFile(
				ctx, os.DirFS(filepath.Join(testdataPath, "crds", "core")), "*.yaml",
				decoder.CreateIgnoreAlreadyExists(r),
			)
			if err != nil {
				t.Log("Error decoding CRDs: ", err)
				t.Fail()
			}

			resli, err := decoder.DecodeAllFiles(ctx, os.DirFS(filepath.Join(testdataPath, "crds", "core")), "*.yaml")
			if err != nil {
				t.Log("Error decoding CRDs: ", err)
				t.Fail()
			}

			ress := unstructured.UnstructuredList{}
			for _, res := range resli {
				res, err := runtime.DefaultUnstructuredConverter.ToUnstructured(res)
				if err != nil {
					t.Log("Error converting CRD: ", err)
					t.Fail()
				}
				ress.Items = append(ress.Items, unstructured.Unstructured{Object: res})
			}
			err = wait.For(
				conditions.New(r).ResourcesFound(&ress),
				wait.WithInterval(100*time.Millisecond),
			)
			if err != nil {
				t.Log("Error waiting for CRD: ", err)
				t.Fail()
			}

			apis.AddToScheme(r.GetScheme())

			err = decoder.DecodeEachFile(
				ctx, os.DirFS(filepath.Join(testdataPath, "compositiondefinitions")), "*.yaml",
				decoder.CreateIgnoreAlreadyExists(r),
			)
			if err != nil {
				t.Log("Error decoding CompositionDefinitions: ", err)
				t.Fail()
			}

			err = decoder.DecodeEachFile(
				ctx, os.DirFS(filepath.Join(testdataPath, "compositions")), "*.yaml",
				decoder.CreateIgnoreAlreadyExists(r),
			)
			if err != nil {
				t.Log("Error decoding Compositions: ", err)
				t.Fail()
			}

			resli, err = decoder.DecodeAllFiles(ctx, os.DirFS(filepath.Join(testdataPath, "compositiondefinitions")), "*.yaml")
			if err != nil {
				t.Log("Error decoding CRDs: ", err)
				t.Fail()
			}

			for _, res := range resli {
				uns, err := runtime.DefaultUnstructuredConverter.ToUnstructured(res)
				if err != nil {
					t.Log("Error converting CRD: ", err)
					t.Fail()
				}

				apiVersion, ok, err := unstructured.NestedString(uns, "status", "apiVersion")
				if !ok || err != nil {
					t.Log("Error getting apiVersion: ", err)
					t.Fail()
				}
				kind, ok, err := unstructured.NestedString(uns, "status", "kind")
				if !ok || err != nil {
					t.Log("Error getting kind: ", err)
					t.Fail()
				}
				err = r.PatchStatus(ctx, res, k8s.Patch{
					PatchType: types.MergePatchType,
					Data:      []byte(fmt.Sprintf(`{"status": {"apiVersion": "%s", "kind": "%s"}}`, apiVersion, kind)),
				})
				if err != nil {
					t.Log("Error patching Composition: ", err)
					t.Fail()
					return ctx
				}
			}

			r.WithNamespace(namespace)
			return ctx
		}).Assess("Testing CompositionDefinition Getter", func(ctx context.Context, t *testing.T, c *envconf.Config) context.Context {
		r, err := resources.New(c.Client().RESTConfig())
		if err != nil {
			t.Fail()
		}
		r.WithNamespace(namespace)

		apis.AddToScheme(r.GetScheme())

		for _, tt := range tests {

			composition := &unstructured.Unstructured{}
			err := decoder.DecodeFile(os.DirFS(filepath.Join(testdataPath, "compositions")), tt.composition, composition)
			if err != nil {
				t.Log("Error decoding Composition: ", err)
				t.Fail()
			}

			uns, err := getUnstructured(c.Client().RESTConfig(), getUnstructuredOptions{
				gvk:       schema.FromAPIVersionAndKind(composition.GetAPIVersion(), composition.GetKind()),
				name:      composition.GetName(),
				namespace: composition.GetNamespace(),
			})
			if err != nil {
				t.Fatal(err)
				return ctx
			}
			pluralizer := FakePluralizer{}

			gt, err := archive.Dynamic(c.Client().RESTConfig(), pluralizer)
			if err != nil {
				t.Fatal(err)
				return ctx
			}

			nfo, err := gt.Get(uns)
			if err != nil {
				t.Fatal(err)
			}

			spew.Dump(nfo)
		}

		return ctx
	}).Feature()

	testenv.Test(t, f)
}

type getUnstructuredOptions struct {
	gvk       schema.GroupVersionKind
	name      string
	namespace string
}

func getUnstructured(rc *rest.Config, opts getUnstructuredOptions) (*unstructured.Unstructured, error) {
	dynamicClient, err := dynamic.NewForConfig(rc)
	if err != nil {
		return nil, err
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(rc)
	if err != nil {
		return nil, err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(
		cacheddiscovery.NewMemCacheClient(discoveryClient),
	)

	restMapping, err := mapper.RESTMapping(opts.gvk.GroupKind(), opts.gvk.Version)
	if err != nil {
		return nil, err
	}

	var ri dynamic.ResourceInterface
	if restMapping.Scope.Name() == meta.RESTScopeNameRoot {
		ri = dynamicClient.Resource(restMapping.Resource)
	} else {
		ri = dynamicClient.Resource(restMapping.Resource).
			Namespace(opts.namespace)
	}

	return ri.Get(context.TODO(), opts.name, metav1.GetOptions{})
}
