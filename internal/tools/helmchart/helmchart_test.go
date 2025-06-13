//go:build integration
// +build integration

package helmchart

import (
	"context"
	"fmt"
	"testing"

	"github.com/krateoplatformops/composition-dynamic-controller/internal/helmclient"
	"github.com/krateoplatformops/plumbing/e2e"
	"github.com/krateoplatformops/unstructured-runtime/pkg/meta"
	"github.com/stretchr/testify/assert"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
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
	f := features.New("RenderTemplate").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("RenderTemplate", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {

		helmClient, err := helmclient.NewClientFromRestConf(&helmclient.RestConfClientOptions{
			RestConfig: cfg.Client().RESTConfig(),
			Options: &helmclient.Options{
				Namespace: namespace,
			},
		})
		if err != nil {
			t.Fatalf("failed to create helm client: %v", err)
		}

		res := createDummyResource()

		opts := RenderTemplateOptions{
			PackageUrl:     "oci://registry-1.docker.io/bitnamicharts/postgresql",
			PackageVersion: "12.8.3",
			HelmClient:     helmClient,
			Resource:       res,
		}

		_, all, err := RenderTemplate(ctx, opts)
		if err != nil {
			t.Fatalf("RenderTemplate failed: %v", err)
		}

		assert.Equal(t, "v1", all[0].APIVersion)
		assert.Equal(t, "Secret", all[0].Kind)
		assert.Equal(t, "demo-postgresql", all[0].Name)
		assert.Equal(t, "demo-system", all[0].Namespace)

		assert.Equal(t, "v1", all[1].APIVersion)
		assert.Equal(t, "Service", all[1].Kind)
		assert.Equal(t, "demo-postgresql-hl", all[1].Name)
		assert.Equal(t, "demo-system", all[1].Namespace)

		return ctx
	}).Feature()

	testenv.Test(t, f)
}

func TestFindRelease(t *testing.T) {
	f := features.New("FindRelease").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("FindRelease", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {

		helmClient, err := helmclient.NewClientFromRestConf(&helmclient.RestConfClientOptions{
			RestConfig: cfg.Client().RESTConfig(),
			Options: &helmclient.Options{
				Namespace: namespace,
			},
		})
		if err != nil {
			t.Fatalf("failed to create helm client: %v", err)
		}

		releaseName := "my-release"

		// Call the FindRelease function
		actualRelease, err := FindRelease(helmClient, releaseName)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Check if the actual release matches the expected release (should be nil for non-existent release)
		if actualRelease != nil {
			t.Fatalf("expected release %v, got %v", nil, actualRelease)
		}

		return ctx
	}).Feature()

	testenv.Test(t, f)
}

func TestFindAllReleases(t *testing.T) {
	f := features.New("FindAllReleases").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("FindAllReleases", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {

		helmClient, err := helmclient.NewClientFromRestConf(&helmclient.RestConfClientOptions{
			RestConfig: cfg.Client().RESTConfig(),
			Options: &helmclient.Options{
				Namespace: namespace,
			},
		})
		if err != nil {
			t.Fatalf("failed to create helm client: %v", err)
		}

		// First, check that we start with no releases
		releases, err := FindAllReleases(helmClient)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assert.NotNil(t, releases)
		assert.Len(t, releases, 0, "Expected no releases initially")

		dynamicClient, err := dynamic.NewForConfig(cfg.Client().RESTConfig())
		if err != nil {
			t.Fatalf("failed to create dynamic client: %v", err)
		}

		// Install a successful test release using Install function
		successfulReleaseName := "test-release-success"
		successfulRes := createDummyResource()

		ls := successfulRes.GetLabels()
		if ls == nil {
			ls = make(map[string]string)
		}
		ls[meta.ReleaseNameLabel] = successfulReleaseName
		successfulRes.SetLabels(ls)
		successfulRes.SetName("success-resource")
		successfulRes.SetUID("12345678-1234-1234-1234-123456789001")

		// Create invalid configuration that will cause the PostgreSQL chart to fail
		validData := map[string]interface{}{
			"service": map[string]interface{}{
				"type": "NodePort",
				"port": int64(31180),
			},
		}
		unstructured.SetNestedField(successfulRes.Object, validData, "spec")

		successfulOpts := InstallOptions{
			HelmClient: helmClient,
			ChartName:  "https://charts.krateo.io",
			Resource:   successfulRes,
			Repo:       "nginx",
			Version:    "0.1.0",
			CheckResourceOptions: CheckResourceOptions{
				DynamicClient: dynamicClient,
				Pluralizer:    FakePluralizer{},
			},
			KrateoNamespace: namespace,
		}

		// Install the successful release
		_, _, err = Install(ctx, successfulOpts)
		if err != nil {
			t.Fatalf("failed to install successful test chart: %v", err)
		}

		// Install a release that will fail using Install function with invalid configuration
		failedReleaseName := "test-release-failed"
		failedRes := createDummyResource()

		lsFailed := failedRes.GetLabels()
		if lsFailed == nil {
			lsFailed = make(map[string]string)
		}
		lsFailed[meta.ReleaseNameLabel] = failedReleaseName
		failedRes.SetLabels(lsFailed)
		failedRes.SetName("failed-resource")
		failedRes.SetUID("12345678-1234-1234-1234-123456789002")
		// Create invalid configuration that will cause the PostgreSQL chart to fail
		invalidData := map[string]interface{}{
			"service": map[string]interface{}{
				"type": "NodePort",
				"port": int64(31180),
			},
		}
		unstructured.SetNestedField(failedRes.Object, invalidData, "spec")

		failedOpts := InstallOptions{
			HelmClient: helmClient,
			ChartName:  "https://charts.krateo.io",
			Resource:   failedRes,
			Repo:       "nginx",
			Version:    "0.1.0",
			CheckResourceOptions: CheckResourceOptions{
				DynamicClient: dynamicClient,
				Pluralizer:    FakePluralizer{},
			},
			KrateoNamespace: namespace,
		}

		// Try to install the potentially failing release
		_, _, err = Install(ctx, failedOpts)
		// We don't treat this as fatal since it might fail by design
		if err != nil {
			t.Logf("Expected potential failure when installing failing release: %v", err)
		}

		// Now check that FindAllReleases finds the installed releases
		releases, err = FindAllReleases(helmClient)
		if err != nil {
			t.Fatalf("unexpected error after installing releases: %v", err)
		}

		assert.NotNil(t, releases)
		// We should have at least 1 release (successful one), possibly 2 if the failed one was also created
		assert.GreaterOrEqual(t, len(releases), 1, "Expected to find at least 1 release after installations")

		// Log all found releases and their statuses
		t.Logf("Found %d releases:", len(releases))
		for _, release := range releases {
			t.Logf("Release '%s' with status: %s", release.Name, release.Info.Status.String())
		}

		// Verify we can find the successful release
		var successfulRelease *release.Release
		var failedRelease *release.Release

		for _, rel := range releases {
			if rel.Name == successfulReleaseName {
				successfulRelease = rel
			}
			if rel.Name == failedReleaseName {
				failedRelease = rel
			}
		}

		// Check successful release
		assert.NotNil(t, successfulRelease, "Should find the successful release")
		if successfulRelease != nil {
			assert.Equal(t, successfulReleaseName, successfulRelease.Name)
			assert.Equal(t, namespace, successfulRelease.Namespace)
			assert.NotEmpty(t, successfulRelease.Info.Status.String(), "Successful release should have a status")
		}

		// Check if failed release was created and found
		if failedRelease != nil {
			t.Logf("Found failed release '%s' with status: %s", failedRelease.Name, failedRelease.Info.Status.String())
			assert.Equal(t, failedReleaseName, failedRelease.Name)
			assert.Equal(t, namespace, failedRelease.Namespace)
			assert.NotEmpty(t, failedRelease.Info.Status.String(), "Failed release should have a status")
		} else {
			t.Logf("Failed release was not created or not found - this might be expected depending on the failure mode")
		}

		// Clean up test releases
		err = helmClient.UninstallRelease(&helmclient.ChartSpec{
			ReleaseName: successfulReleaseName,
			Namespace:   namespace,
		})
		if err != nil {
			t.Logf("Warning: failed to clean up successful test release: %v", err)
		}

		if failedRelease != nil {
			err = helmClient.UninstallRelease(&helmclient.ChartSpec{
				ReleaseName: failedReleaseName,
				Namespace:   namespace,
			})
			if err != nil {
				t.Logf("Warning: failed to clean up failed test release: %v", err)
			}
		}

		return ctx
	}).Feature()

	testenv.Test(t, f)
}

func TestFindAnyRelease(t *testing.T) {
	f := features.New("FindAnyRelease").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("FindAnyRelease", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {

		helmClient, err := helmclient.NewClientFromRestConf(&helmclient.RestConfClientOptions{
			RestConfig: cfg.Client().RESTConfig(),
			Options: &helmclient.Options{
				Namespace: namespace,
			},
		})
		if err != nil {
			t.Fatalf("failed to create helm client: %v", err)
		}

		// Test finding non-existent release
		nonExistentReleaseName := "non-existent-release"
		actualRelease, err := FindAnyRelease(helmClient, nonExistentReleaseName)
		assert.Nil(t, actualRelease)
		dynamicClient, err := dynamic.NewForConfig(cfg.Client().RESTConfig())
		if err != nil {
			t.Fatalf("failed to create dynamic client: %v", err)
		}

		// Install a test release
		testReleaseName := "test-findany-release"
		testRes := createDummyResource()

		ls := testRes.GetLabels()
		if ls == nil {
			ls = make(map[string]string)
		}
		ls[meta.ReleaseNameLabel] = testReleaseName
		testRes.SetLabels(ls)
		testRes.SetName("findany-resource")
		testRes.SetUID("12345678-1234-1234-1234-123456789003")

		validData := map[string]interface{}{
			"service": map[string]interface{}{
				"type": "NodePort",
				"port": int64(31180),
			},
		}
		unstructured.SetNestedField(testRes.Object, validData, "spec")

		installOpts := InstallOptions{
			HelmClient: helmClient,
			ChartName:  "https://charts.krateo.io",
			Resource:   testRes,
			Repo:       "nginx",
			Version:    "0.1.0",
			CheckResourceOptions: CheckResourceOptions{
				DynamicClient: dynamicClient,
				Pluralizer:    FakePluralizer{},
			},
			KrateoNamespace: namespace,
		}

		// Install the test release
		_, _, err = Install(ctx, installOpts)
		if err != nil {
			t.Fatalf("failed to install test chart: %v", err)
		}

		// Test finding existing release
		foundRelease, err := FindAnyRelease(helmClient, testReleaseName)
		assert.NoError(t, err)
		assert.NotNil(t, foundRelease)
		assert.Equal(t, testReleaseName, foundRelease.Name)
		assert.Equal(t, namespace, foundRelease.Namespace)

		// Clean up test release
		err = helmClient.UninstallRelease(&helmclient.ChartSpec{
			ReleaseName: testReleaseName,
			Namespace:   namespace,
		})
		if err != nil {
			t.Logf("Warning: failed to clean up test release: %v", err)
		}

		return ctx
	}).Feature()

	testenv.Test(t, f)
}

func createDummyResource() *unstructured.Unstructured {
	data := map[string]interface{}{
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
