package helmchart

import (
	"context"
	"fmt"

	"github.com/krateoplatformops/composition-dynamic-controller/internal/helmclient"
	compositionMeta "github.com/krateoplatformops/composition-dynamic-controller/internal/meta"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type UpdateOptions struct {
	CheckResourceOptions
	HelmClient      helmclient.Client
	ChartName       string
	Version         string
	Resource        *unstructured.Unstructured
	Repo            string
	Credentials     *Credentials
	KrateoNamespace string
	MaxHistory      int
}

func Update(ctx context.Context, opts UpdateOptions) (*release.Release, error) {
	chartSpec := helmclient.ChartSpec{
		ReleaseName:     compositionMeta.GetReleaseName(opts.Resource),
		Namespace:       opts.Resource.GetNamespace(),
		Repo:            opts.Repo,
		ChartName:       opts.ChartName,
		Version:         opts.Version,
		CreateNamespace: true,
		UpgradeCRDs:     true,
		Replace:         true,
		CleanupOnFail:   true,
		Install:         true,
		MaxHistory:      opts.MaxHistory,
	}
	if opts.Credentials != nil {
		chartSpec.Username = opts.Credentials.Username
		chartSpec.Password = opts.Credentials.Password
	}

	dat, err := ExtractValuesFromSpec(opts.Resource)
	if err != nil {
		return nil, err
	}
	if len(dat) > 0 {
		chartSpec.ResetValues = true
		chartSpec.ValuesYaml = string(dat)
	}

	uid := opts.Resource.GetUID()

	gvr, err := opts.Pluralizer.GVKtoGVR(opts.Resource.GetObjectKind().GroupVersionKind())
	if err != nil {
		return nil, fmt.Errorf("failed to get GVR: %w", err)
	}

	gracefullyPaused := "false"
	if compositionMeta.IsGracefullyPaused(opts.Resource) {
		gracefullyPaused = "true"
	} else {
		gracefullyPaused = "false"
	}
	dat, err = AddOrUpdateFieldInValues(dat, gracefullyPaused, "global", "gracefullyPaused")
	if err != nil {
		return nil, fmt.Errorf("failed to add gracefullyPaused to values: %w", err)
	}

	dat, err = AddOrUpdateFieldInValues(dat, opts.Resource.GetNamespace(), "global", "compositionNamespace")
	if err != nil {
		return nil, fmt.Errorf("failed to add compositionNamespace to values: %w", err)
	}

	dat, err = AddOrUpdateFieldInValues(dat, opts.Resource.GetName(), "global", "compositionName")
	if err != nil {
		return nil, fmt.Errorf("failed to add compositionName to values: %w", err)
	}

	dat, err = AddOrUpdateFieldInValues(dat, opts.KrateoNamespace, "global", "krateoNamespace")
	if err != nil {
		return nil, fmt.Errorf("failed to add krateoNamespace to values: %w", err)
	}

	dat, err = AddOrUpdateFieldInValues(dat, uid, "global", "compositionId")
	if err != nil {
		return nil, fmt.Errorf("failed to add compositionId to values: %w", err)
	}
	// DEPRECATED: Remove in future versions in favor of compositionGroup and compositionInstalledVersion
	dat, err = AddOrUpdateFieldInValues(dat, opts.Resource.GetAPIVersion(), "global", "compositionApiVersion")
	if err != nil {
		return nil, fmt.Errorf("failed to add compositionApiVersion to values: %w", err)
	}
	// END DEPRECATED
	dat, err = AddOrUpdateFieldInValues(dat, gvr.Group, "global", "compositionGroup")
	if err != nil {
		return nil, fmt.Errorf("failed to add compositionGroup to values: %w", err)
	}
	dat, err = AddOrUpdateFieldInValues(dat, gvr.Version, "global", "compositionInstalledVersion")
	if err != nil {
		return nil, fmt.Errorf("failed to add compositionInstalledVersion to values: %w", err)
	}
	dat, err = AddOrUpdateFieldInValues(dat, gvr.Resource, "global", "compositionResource")
	if err != nil {
		return nil, fmt.Errorf("failed to add compositionResource to values: %w", err)
	}
	dat, err = AddOrUpdateFieldInValues(dat, opts.Resource.GetObjectKind().GroupVersionKind().Kind, "global", "compositionKind")
	if err != nil {
		return nil, fmt.Errorf("failed to add compositionKind to values: %w", err)
	}

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

	rel, err := opts.HelmClient.UpgradeChart(ctx, &chartSpec, helmOpts)
	return rel, err
}
