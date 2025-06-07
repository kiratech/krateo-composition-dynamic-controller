package helmchart

import (
	"context"
	"fmt"
	"io"
	"testing"

	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/kube"

	helmstorage "helm.sh/helm/v3/pkg/storage"

	"github.com/krateoplatformops/composition-dynamic-controller/internal/helmclient"
	"github.com/krateoplatformops/unstructured-runtime/pkg/meta"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/getter"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/registry"
	"helm.sh/helm/v3/pkg/storage/driver"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	pkgURL = "../../../testdata/dummy-chart-0.2.0.tgz"
)

func ExampleExtractValuesFromSpec() {
	res := createDummyResource()

	dat, err := ExtractValuesFromSpec(res)
	if err != nil {
		panic(err)
	}

	fmt.Println(string(dat))
	// Output:
	// data:
	//   counter: 1
	//   greeting: Hello World!
	//   like: false
}

func TestRenderTemplate(t *testing.T) {
	res := createDummyResource()

	// cli, err := connect(&zerolog.Logger{}, res)
	// if err != nil {
	// 	t.Fatal(err)
	// }
	cli := newHelmClient()

	opts := RenderTemplateOptions{
		PackageUrl:     "oci://registry-1.docker.io/bitnamicharts/postgresql",
		PackageVersion: "12.8.3",
		HelmClient:     cli,
		Resource:       res,
	}

	_, all, err := RenderTemplate(context.TODO(), opts)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, "v1", all[0].APIVersion)
	assert.Equal(t, "Secret", all[0].Kind)
	assert.Equal(t, "demo-postgresql", all[0].Name)
	assert.Equal(t, "demo-system", all[0].Namespace)

	assert.Equal(t, "v1", all[1].APIVersion)
	assert.Equal(t, "Service", all[1].Kind)
	assert.Equal(t, "demo-postgresql-hl", all[1].Name)
	assert.Equal(t, "demo-system", all[1].Namespace)
}

func createDummyResource() *unstructured.Unstructured {
	data :=
		map[string]interface{}{
			"like":     false,
			"greeting": "Hello World!",
			"counter":  int64(1),
		}

	res := &unstructured.Unstructured{}
	meta.SetReleaseName(res, "demo")
	res.SetGroupVersionKind(schema.FromAPIVersionAndKind("dummy-charts.krateo.io/v0-2-0", "DummyChart"))
	res.SetName("demo")
	res.SetNamespace("demo-system")
	unstructured.SetNestedField(res.Object, data, "spec", "data")

	return res
}

func connect(logger *zerolog.Logger, cr *unstructured.Unstructured) (helmclient.Client, error) {
	opts := &helmclient.Options{
		Namespace:        cr.GetNamespace(),
		RepositoryCache:  "/tmp/.helmcache",
		RepositoryConfig: "/tmp/.helmrepo",
		Debug:            true,
		Linting:          false,
		DebugLog: func(format string, v ...interface{}) {
			if !meta.IsVerbose(cr) {
				return
			}
			if len(v) > 0 {
				logger.Debug().Msgf(format, v)
			} else {
				logger.Debug().Msg(format)
			}
		},
	}

	return helmclient.New(opts)
}

func newHelmClientWithClient(client kube.Interface) helmclient.Client {
	settings := cli.New()
	// storage := repo.File{}

	actionConfig := &action.Configuration{
		Releases:     helmstorage.Init(driver.NewMemory()),
		KubeClient:   &kubefake.FailingKubeClient{PrintingKubeClient: kubefake.PrintingKubeClient{Out: io.Discard}},
		Capabilities: chartutil.DefaultCapabilities,
		Log: func(format string, v ...interface{}) {
			// t.Helper()
			// if true {
			// 	t.Logf(format, v...)
			// }
		},
	}

	registryClient, err := registry.NewClient(
		registry.ClientOptDebug(settings.Debug),
		registry.ClientOptCredentialsFile(settings.RegistryConfig),
	)
	if err != nil {
		// t.Fatal(err)
		return nil
	}
	actionConfig.RegistryClient = registryClient

	return &helmclient.HelmClient{
		Settings:  settings,
		Providers: getter.All(settings),
		// storage:      &storage,
		ActionConfig: actionConfig,
		// linting:      options.Linting,
		DebugLog: actionConfig.Log,
		// output:       options.Output,
		// RegistryAuth: options.RegistryAuth,
	}
}

func newHelmClient() helmclient.Client {
	settings := cli.New()
	// storage := repo.File{}

	actionConfig := actionConfigFixture()

	registryClient, err := registry.NewClient(
		registry.ClientOptDebug(settings.Debug),
		registry.ClientOptCredentialsFile(settings.RegistryConfig),
	)
	if err != nil {
		// t.Fatal(err)
		return nil
	}
	actionConfig.RegistryClient = registryClient

	return &helmclient.HelmClient{
		Settings:  settings,
		Providers: getter.All(settings),
		// storage:      &storage,
		ActionConfig: actionConfig,
		// linting:      options.Linting,
		DebugLog: actionConfig.Log,
		// output:       options.Output,
		// RegistryAuth: options.RegistryAuth,
	}
}

func actionConfigFixture() *action.Configuration {
	// t.Helper()

	registryClient, err := registry.NewClient()
	if err != nil {
		// t.Fatal(err)
	}

	return &action.Configuration{
		Releases:       helmstorage.Init(driver.NewMemory()),
		KubeClient:     &kubefake.FailingKubeClient{PrintingKubeClient: kubefake.PrintingKubeClient{Out: io.Discard}},
		Capabilities:   chartutil.DefaultCapabilities,
		RegistryClient: registryClient,
		Log: func(format string, v ...interface{}) {
			// t.Helper()
			// if true {
			// 	t.Logf(format, v...)
			// }
		},
	}
}
func TestFindRelease(t *testing.T) {
	hc := newHelmClient()

	releaseName := "my-release"

	// Call the FindRelease function
	actualRelease, err := FindRelease(hc, releaseName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check if the actual release matches the expected release
	if actualRelease != nil {
		t.Fatalf("expected release %v, got %v", nil, actualRelease)
	}
}
