package archive

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	compositionMeta "github.com/krateoplatformops/composition-dynamic-controller/internal/meta"

	"github.com/krateoplatformops/composition-dynamic-controller/internal/helmclient"
	"github.com/krateoplatformops/unstructured-runtime/pkg/listwatcher"
	"github.com/krateoplatformops/unstructured-runtime/pkg/logging"
	"github.com/krateoplatformops/unstructured-runtime/pkg/pluralizer"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

type CompositionDefinitionInfo struct {
	Namespace string
	Name      string
	GVR       schema.GroupVersionResource
}

type Info struct {
	// URL of the helm chart package that is being requested.
	URL string `json:"url"`

	// Version of the chart release.
	Version string `json:"version,omitempty"`

	// Repo is the repository name.
	Repo string `json:"repo,omitempty"`

	// RegistryAuth is the credentials to access the registry.
	RegistryAuth *helmclient.RegistryAuth `json:"registryAuth,omitempty"`

	// CompositionDefinitionInfo is the information about the composition definition.
	CompositionDefinitionInfo *CompositionDefinitionInfo `json:"compositionDefinitionInfo,omitempty"`
}

func (i *Info) IsOCI() bool {
	return strings.HasPrefix(i.URL, "oci://")
}

func (i *Info) IsTGZ() bool {
	return strings.HasSuffix(i.URL, ".tgz")
}

func (i *Info) IsHTTP() bool {
	return strings.HasPrefix(i.URL, "http://") || strings.HasPrefix(i.URL, "https://")
}

type Getter interface {
	Get(un *unstructured.Unstructured) (*Info, error)
	WithLogger(logger logging.Logger) Getter
}

func Static(chart string) Getter {
	return staticGetter{chartName: chart}
}

func Dynamic(cfg *rest.Config, pluralizer pluralizer.PluralizerInterface) (Getter, error) {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	return &dynamicGetter{
		dynamicClient: dyn,
		logger:        logging.NewNopLogger(),
		pluralizer:    pluralizer,
	}, nil
}

var _ Getter = (*staticGetter)(nil)

type staticGetter struct {
	chartName string
}

func (pig staticGetter) WithLogger(logger logging.Logger) Getter {
	if logger == nil {
		logger = logging.NewNopLogger()
	}
	return &staticGetter{
		chartName: pig.chartName,
	}
}

func (pig staticGetter) Get(_ *unstructured.Unstructured) (*Info, error) {
	return &Info{
		URL: pig.chartName,
	}, nil
}

var _ Getter = (*dynamicGetter)(nil)

type dynamicGetter struct {
	dynamicClient dynamic.Interface
	logger        logging.Logger
	pluralizer    pluralizer.PluralizerInterface
}

func (g *dynamicGetter) WithLogger(logger logging.Logger) Getter {
	if logger == nil {
		logger = logging.NewNopLogger()
	}
	return &dynamicGetter{
		dynamicClient: g.dynamicClient,
		logger:        logger,
		pluralizer:    g.pluralizer,
	}
}

func (g *dynamicGetter) Get(uns *unstructured.Unstructured) (*Info, error) {
	if uns == nil {
		return nil, fmt.Errorf("unstructured object is nil")
	}
	gvr, err := g.pluralizer.GVKtoGVR(uns.GroupVersionKind())
	if err != nil {
		return nil, fmt.Errorf("error getting GVR for Kind: '%v', Group: '%v, Version: '%v': %w", uns.GroupVersionKind().Kind, uns.GroupVersionKind().Group, uns.GroupVersionKind().Version, err)
	}

	var cdInfo *CompositionDefinitionInfo
	lbl := uns.GetLabels()
	if lbl != nil {
		var gvr schema.GroupVersionResource
		group, ok := lbl[compositionMeta.CompositionDefinitionGroupLabel]
		if ok && group != "" {
			gvr.Group = group
		}
		version, ok := lbl[compositionMeta.CompositionDefinitionVersionLabel]
		if ok && version != "" {
			gvr.Version = version
		}
		resource, ok := lbl[compositionMeta.CompositionDefinitionResourceLabel]
		if ok && resource != "" {
			gvr.Resource = resource
		}

		name, _ := lbl[compositionMeta.CompositionDefinitionNameLabel]

		namespace, _ := lbl[compositionMeta.CompositionDefinitionNamespaceLabel]

		if len(gvr.Group) > 0 && len(gvr.Resource) > 0 && len(gvr.Version) > 0 && len(name) > 0 && len(namespace) > 0 {
			g.logger.Debug("Using labels to get composition definition", "compositionDefinitionName", name, "compositionDefinitionNamespace", namespace, "gvr", gvr.String())
			cdInfo = &CompositionDefinitionInfo{
				Name:      name,
				Namespace: namespace,
				GVR:       gvr,
			}
		}
	}

	var compositionDefinition *unstructured.Unstructured

	if cdInfo != nil {
		g.logger.Debug("Getting composition definition", "compositionDefinitionName", cdInfo.Name, "compositionDefinitionNamespace", cdInfo.Namespace, "gvr", cdInfo.GVR.String())
		compositionDefinition, err = g.dynamicClient.Resource(cdInfo.GVR).
			Namespace(cdInfo.Namespace).
			Get(context.Background(), cdInfo.Name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("error getting composition definition '%s' in namespace '%s' with gvr: %s: %w", cdInfo.Name, cdInfo.Namespace, cdInfo.GVR.String(), err)
		}
	} else {
		// Search for the composition definition in the namespace of the unstructured object
		g.logger.Debug("Searching for composition definition")
		compositionDefinition, err = g.searchCompositionDefinition(gvr, uns)
		if err != nil {
			return nil, fmt.Errorf("error searching for composition definition in namespace '%s': %w", uns.GetNamespace(), err)
		}
	}

	packageUrl, ok, err := unstructured.NestedString(compositionDefinition.UnstructuredContent(), "spec", "chart", "url")
	if err != nil {
		g.logger.Debug("Failed to resolve 'status.packageUrl'", "error", err.Error(), "compositionDefinitionName", compositionDefinition.GetName(), "compositionDefinitionNamespace", compositionDefinition.GetNamespace())
		return nil, err
	}
	if !ok {
		return nil,
			fmt.Errorf("missing 'status.packageUrl' in definition for '%v' in namespace: %s", gvr, uns.GetNamespace())
	}

	g.logger.Debug("PackageUrl for", "compositionDefinitionName", compositionDefinition.GetName(), "compositionDefinitionNamespace", compositionDefinition.GetNamespace(), "url", packageUrl)

	packageVersion, _, err := unstructured.NestedString(compositionDefinition.UnstructuredContent(), "spec", "chart", "version")
	if err != nil {
		g.logger.Debug("Failed to resolve 'spec.chart.version'", "error", err.Error(), "compositionDefinitionName", compositionDefinition.GetName(), "compositionDefinitionNamespace", compositionDefinition.GetNamespace())
		return nil, err
	}
	repo, _, err := unstructured.NestedString(compositionDefinition.UnstructuredContent(), "spec", "chart", "repo")
	if err != nil {
		g.logger.Debug("Failed to resolve 'spec.chart.repo'", "error", err.Error(), "compositionDefinitionName", compositionDefinition.GetName(), "compositionDefinitionNamespace", compositionDefinition.GetNamespace())
		return nil, err
	}

	username, _, err := unstructured.NestedString(compositionDefinition.UnstructuredContent(), "spec", "chart", "credentials", "username")
	if err != nil {
		g.logger.Debug("Failed to resolve 'spec.chart.credentials.username'", "error", err.Error(), "compositionDefinitionName", compositionDefinition.GetName(), "compositionDefinitionNamespace", compositionDefinition.GetNamespace())
		return nil, err
	}

	passwordRef, _, err := unstructured.NestedStringMap(compositionDefinition.UnstructuredContent(), "spec", "chart", "credentials", "passwordRef")
	if err != nil {
		g.logger.Debug("Failed to resolve 'spec.chart.credentials.passwordRef'", "error", err.Error(), "compositionDefinitionName", compositionDefinition.GetName(), "compositionDefinitionNamespace", compositionDefinition.GetNamespace())
		return nil, err
	}

	var password string
	if passwordRef != nil {
		password, err = GetSecret(context.Background(), g.dynamicClient, SecretKeySelector{
			Name:      passwordRef["compositionDefinitionName"],
			Namespace: passwordRef["compositionDefinitionNamespace"],
			Key:       passwordRef["key"],
		})
		if err != nil {
			g.logger.Debug("Failed to resolve secret", "error", err.Error(), "compositionDefinitionName", passwordRef["compositionDefinitionName"], "compositionDefinitionNamespace", passwordRef["compositionDefinitionNamespace"])
			return nil, err
		}
	}
	insecureSkipTLSverify, _, err := unstructured.NestedBool(compositionDefinition.UnstructuredContent(), "spec", "chart", "insecureSkipTLSverify")
	if err != nil {
		g.logger.Debug("Failed to resolve 'spec.chart.insecureSkipTLSverify'", "error", err.Error(), "compositionDefinitionName", compositionDefinition.GetName(), "compositionDefinitionNamespace", compositionDefinition.GetNamespace())
		return nil, err
	}

	compositionDefinitionGVR, err := g.pluralizer.GVKtoGVR(compositionDefinition.GroupVersionKind())
	if err != nil {
		g.logger.Debug("Converting GVK to GVR for composition definition", "error", err.Error(), "compositionDefinitionName", compositionDefinition.GetName(), "compositionDefinitionNamespace", compositionDefinition.GetNamespace())
		return nil, fmt.Errorf("converting GVK to GVR for composition definition: %w", err)
	}

	return &Info{
		URL:     packageUrl,
		Version: packageVersion,
		Repo:    repo,
		RegistryAuth: &helmclient.RegistryAuth{
			Username:              username,
			Password:              password,
			InsecureSkipTLSverify: insecureSkipTLSverify,
		},
		CompositionDefinitionInfo: &CompositionDefinitionInfo{
			Name:      compositionDefinition.GetName(),
			Namespace: compositionDefinition.GetNamespace(),
			GVR:       compositionDefinitionGVR,
		},
	}, nil
}

type SecretKeySelector struct {
	Name      string
	Namespace string
	Key       string
}

func GetSecret(ctx context.Context, client dynamic.Interface, secretKeySelector SecretKeySelector) (string, error) {
	gvr := schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}

	sec, err := client.Resource(gvr).Namespace(secretKeySelector.Namespace).Get(ctx, secretKeySelector.Name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	data, _, err := unstructured.NestedMap(sec.Object, "data")
	if err != nil {
		return "", err
	}
	bsec := data[secretKeySelector.Key].(string)
	bkey, err := base64.StdEncoding.DecodeString(bsec)
	if err != nil {
		return "", fmt.Errorf("failed to decode secret key: %w", err)
	}
	return string(bkey), nil
}

func (g *dynamicGetter) searchCompositionDefinition(gvr schema.GroupVersionResource, mg *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	gvrForDefinitions := schema.GroupVersionResource{
		Group:    "core.krateo.io",
		Version:  "v1alpha1",
		Resource: "compositiondefinitions",
	}
	all, err := g.dynamicClient.Resource(gvrForDefinitions).
		List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	tot := len(all.Items)
	if tot == 0 {
		return nil,
			fmt.Errorf("no definition found for '%v' in namespace: %s", gvr.String(), mg.GetNamespace())
	}

	compositionDefinition := &all.Items[0]
	if tot > 1 {
		found := false
		for _, el := range all.Items {
			apiversion, ok, err := unstructured.NestedString(el.UnstructuredContent(), "status", "apiVersion")
			if err != nil {
				g.logger.Debug("Failed to resolve 'status.apiVersion'", "error", err.Error(), "compositionDefinitionName", el.GetName(), "compositionDefinitionNamespace", el.GetNamespace())
				continue
			}
			if !ok {
				g.logger.Debug("Failed to resolve 'status.apiVersion'", "compositionDefinitionName", el.GetName(), "compositionDefinitionNamespace", el.GetNamespace())
				continue
			}
			versionSplit := strings.Split(apiversion, "/")
			if len(versionSplit) != 2 {
				g.logger.Debug("Invalid format for 'status.apiVersion'", "compositionDefinitionName", el.GetName(), "compositionDefinitionNamespace", el.GetNamespace())
				continue
			}
			kind, ok, err := unstructured.NestedString(el.UnstructuredContent(), "status", "kind")
			if err != nil {
				g.logger.Debug("Failed to resolve 'status.kind'", "error", err.Error(), "compositionDefinitionName", el.GetName(), "compositionDefinitionNamespace", el.GetNamespace())
				continue
			}
			if !ok {
				g.logger.Debug("Failed to resolve 'status.kind'", "compositionDefinitionName", el.GetName(), "compositionDefinitionNamespace", el.GetNamespace())
				continue
			}

			version := versionSplit[1]
			if version == mg.GetLabels()[listwatcher.CompositionVersionLabel] && kind == mg.GetKind() {
				compositionDefinition = &el
				found = true
				break
			}
		}
		if !found {
			return nil,
				fmt.Errorf("too many definitions [%d] found for '%v' in namespace: %s", tot, gvr.String(), mg.GetNamespace())
		}
	}

	return compositionDefinition, nil
}
