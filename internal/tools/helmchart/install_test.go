//go:build integration
// +build integration

package helmchart

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/gobuffalo/flect"
	"github.com/krateoplatformops/unstructured-runtime/pkg/pluralizer"
	"helm.sh/helm/v3/pkg/release"

	"github.com/krateoplatformops/plumbing/e2e"
	xenv "github.com/krateoplatformops/plumbing/env"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"
	"sigs.k8s.io/e2e-framework/pkg/features"
	"sigs.k8s.io/e2e-framework/support/kind"
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

var (
	testenv     env.Environment
	clusterName string
	namespace   string
)

const (
	testdataPath = "../../../testdata"
)

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

func TestInstall(t *testing.T) {

	f := features.New("Setup").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("Install", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {

		helmClient := newHelmClient()

		// Create a dummy resource
		res := createDummyResource()

		dynamicClient, err := dynamic.NewForConfig(cfg.Client().RESTConfig())

		// Set up the install options
		opts := InstallOptions{
			HelmClient: helmClient,
			ChartName:  "https://charts.bitnami.com/bitnami",
			Resource:   res,
			Repo:       "postgresql",
			Version:    "12.8.3",
			CheckResourceOptions: CheckResourceOptions{
				DynamicClient: dynamicClient,
				Pluralizer:    FakePluralizer{},
			},
		}

		// Call the Install function
		rel, _, err := Install(ctx, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Check the returned release
		expectedRelease := &release.Release{
			Name:      "demo",
			Namespace: "demo-system",
			Version:   1,
		}

		if rel.Name != expectedRelease.Name || rel.Namespace != expectedRelease.Namespace || rel.Version != expectedRelease.Version {
			t.Fatalf("expected release %v, got %v", expectedRelease, rel)
		}
		return ctx
	}).Feature()

	testenv.Test(t, f)
}
