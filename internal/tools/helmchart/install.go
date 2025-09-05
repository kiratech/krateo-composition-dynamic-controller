package helmchart

import (
	"context"
	"fmt"

	"github.com/krateoplatformops/composition-dynamic-controller/internal/helmclient"
	compositionMeta "github.com/krateoplatformops/composition-dynamic-controller/internal/meta"
	"github.com/krateoplatformops/unstructured-runtime/pkg/meta"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type Credentials struct {
	Username string
	Password string
}

type InstallOptions struct {
	CheckResourceOptions
	HelmClient      helmclient.Client
	ChartName       string
	Resource        *unstructured.Unstructured
	Repo            string
	Version         string
	Credentials     *Credentials
	KrateoNamespace string
	MaxHistory      int
}

func Install(ctx context.Context, opts InstallOptions) (*release.Release, int64, error) {
	chartSpec := helmclient.ChartSpec{
		ReleaseName:     meta.GetReleaseName(opts.Resource),
		Namespace:       opts.Resource.GetNamespace(),
		Version:         opts.Version,
		Repo:            opts.Repo,
		ChartName:       opts.ChartName,
		CreateNamespace: true,
		UpgradeCRDs:     true,
		Wait:            false,
		MaxHistory:      opts.MaxHistory,
	}
	if opts.Credentials != nil {
		chartSpec.Username = opts.Credentials.Username
		chartSpec.Password = opts.Credentials.Password
	}

	dat, err := ExtractValuesFromSpec(opts.Resource)
	if err != nil {
		return nil, 0, err
	}
	if len(dat) == 0 {
		return nil, 0, nil
	}
	uid := opts.Resource.GetUID()

	gvr, err := opts.Pluralizer.GVKtoGVR(opts.Resource.GetObjectKind().GroupVersionKind())
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get GVR: %w", err)
	}

	gracefullyPaused := "false"
	if compositionMeta.IsGracefullyPaused(opts.Resource) {
		gracefullyPaused = "true"
	} else {
		gracefullyPaused = "false"
	}
	dat, err = AddOrUpdateFieldInValues(dat, gracefullyPaused, "global", "gracefullyPaused")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to add gracefullyPaused to values: %w", err)
	}

	dat, err = AddOrUpdateFieldInValues(dat, opts.Resource.GetNamespace(), "global", "compositionNamespace")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to add compositionNamespace to values: %w", err)
	}
	dat, err = AddOrUpdateFieldInValues(dat, opts.Resource.GetName(), "global", "compositionName")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to add compositionName to values: %w", err)
	}
	dat, err = AddOrUpdateFieldInValues(dat, opts.KrateoNamespace, "global", "krateoNamespace")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to add krateoNamespace to values: %w", err)
	}
	dat, err = AddOrUpdateFieldInValues(dat, uid, "global", "compositionId")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to add compositionId to values: %w", err)
	}
	// DEPRECATED: Remove in future versions in favor of compositionGroup and compositionInstalledVersion
	dat, err = AddOrUpdateFieldInValues(dat, opts.Resource.GetAPIVersion(), "global", "compositionApiVersion")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to add compositionApiVersion to values: %w", err)
	}
	// END DEPRECATED
	dat, err = AddOrUpdateFieldInValues(dat, gvr.Group, "global", "compositionGroup")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to add compositionGroup to values: %w", err)
	}
	dat, err = AddOrUpdateFieldInValues(dat, gvr.Version, "global", "compositionInstalledVersion")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to add compositionInstalledVersion to values: %w", err)
	}
	dat, err = AddOrUpdateFieldInValues(dat, gvr.Resource, "global", "compositionResource")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to add compositionResource to values: %w", err)
	}
	dat, err = AddOrUpdateFieldInValues(dat, opts.Resource.GetObjectKind().GroupVersionKind().Kind, "global", "compositionKind")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to add compositionKind to values: %w", err)
	}

	claimGen := opts.Resource.GetGeneration()
	chartSpec.ValuesYaml = string(dat)
	helmOpts := &helmclient.GenericHelmOptions{
		PostRenderer: &labelsPostRender{
			UID:                  uid,
			CompositionName:      opts.Resource.GetName(),
			CompositionNamespace: opts.Resource.GetNamespace(),
			CompositionGVR:       gvr,
			CompositionGVK:       opts.Resource.GetObjectKind().GroupVersionKind(),
			KrateoNamespace:      opts.KrateoNamespace,
		},
	}
	rel, err := opts.HelmClient.InstallOrUpgradeChart(ctx, &chartSpec, helmOpts)
	return rel, claimGen, err
}

/*
compositionId: the UID of the composition resource
compositionGroup: the group of the composition resource
compositionInstalledVersion: the version of the composition resource. It changes when the composition version changes. (eg. an update in the chart version)
compositionResource: the resource of the composition resource
compositionName: the name of the composition resource
compositionNamespace: the namespace of the composition resource
compositionKind: the kind of the composition resource
krateoNamespace: the namespace of the Krateo installation
*/
