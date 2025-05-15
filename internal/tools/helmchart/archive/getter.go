package archive

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/krateoplatformops/composition-dynamic-controller/internal/helmclient"
	"github.com/krateoplatformops/unstructured-runtime/pkg/listwatcher"
	"github.com/krateoplatformops/unstructured-runtime/pkg/logging"
	"github.com/krateoplatformops/unstructured-runtime/pkg/pluralizer"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

type CompositionDefinitionInfo struct {
	UID       types.UID
	Namespace string
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
}

func Static(chart string) Getter {
	return staticGetter{chartName: chart}
}

func Dynamic(cfg *rest.Config, log logging.Logger, pluralizer pluralizer.PluralizerInterface) (Getter, error) {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	return &dynamicGetter{
		dynamicClient: dyn,
		logger:        log,
		pluralizer:    pluralizer,
	}, nil
}

var _ Getter = (*staticGetter)(nil)

type staticGetter struct {
	chartName string
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

func (g *dynamicGetter) Get(uns *unstructured.Unstructured) (*Info, error) {
	gvr, err := g.pluralizer.GVKtoGVR(uns.GroupVersionKind())
	if err != nil {
		return nil, fmt.Errorf("error getting GVR for Kind: '%v', Group: '%v, Version: '%v': %w", uns.GroupVersionKind().Kind, uns.GroupVersionKind().Group, uns.GroupVersionKind().Version, err)
	}

	gvrForDefinitions := schema.GroupVersionResource{
		Group:    "core.krateo.io",
		Version:  "v1alpha1",
		Resource: "compositiondefinitions",
	}

	all, err := g.dynamicClient.Resource(gvrForDefinitions).
		Namespace(uns.GetNamespace()).
		List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	tot := len(all.Items)
	if tot == 0 {
		return nil,
			fmt.Errorf("no definition found for '%v' in namespace: %s", gvr.String(), uns.GetNamespace())
	}

	compositionDefinition := all.Items[0]
	if tot > 1 {
		found := false
		for _, el := range all.Items {
			apiversion, ok, err := unstructured.NestedString(el.UnstructuredContent(), "status", "apiVersion")
			if err != nil {
				g.logger.Debug("Failed to resolve 'status.apiVersion'", "error", err.Error(), "name", el.GetName(), "namespace", el.GetNamespace())
				continue
			}
			if !ok {
				g.logger.Debug("Failed to resolve 'status.apiVersion'", "name", el.GetName(), "namespace", el.GetNamespace())
				continue
			}
			versionSplit := strings.Split(apiversion, "/")
			if len(versionSplit) != 2 {
				g.logger.Debug("Invalid format for 'status.apiVersion'", "name", el.GetName(), "namespace", el.GetNamespace())
				continue
			}
			kind, ok, err := unstructured.NestedString(el.UnstructuredContent(), "status", "kind")
			if err != nil {
				g.logger.Debug("Failed to resolve 'status.kind'", "error", err.Error(), "name", el.GetName(), "namespace", el.GetNamespace())
				continue
			}
			if !ok {
				g.logger.Debug("Failed to resolve 'status.kind'", "name", el.GetName(), "namespace", el.GetNamespace())
				continue
			}

			version := versionSplit[1]
			if version == uns.GetLabels()[listwatcher.CompositionVersionLabel] && kind == uns.GetKind() {
				compositionDefinition = el
				found = true
				break
			}
		}
		if !found {
			return nil,
				fmt.Errorf("too many definitions [%d] found for '%v' in namespace: %s", tot, gvr.String(), uns.GetNamespace())
		}
	}

	packageUrl, ok, err := unstructured.NestedString(compositionDefinition.UnstructuredContent(), "spec", "chart", "url")
	if err != nil {
		g.logger.Debug("Failed to resolve 'status.packageUrl'", "error", err.Error(), "name", compositionDefinition.GetName(), "namespace", compositionDefinition.GetNamespace())
		return nil, err
	}
	if !ok {
		return nil,
			fmt.Errorf("missing 'status.packageUrl' in definition for '%v' in namespace: %s", gvr, uns.GetNamespace())
	}
	g.logger.Debug("PackageUrl for", "name", compositionDefinition.GetName(), "namespace", compositionDefinition.GetNamespace(), "url", packageUrl)

	packageVersion, _, err := unstructured.NestedString(compositionDefinition.UnstructuredContent(), "spec", "chart", "version")
	if err != nil {
		g.logger.Debug("Failed to resolve 'spec.chart.version'", "error", err.Error(), "name", compositionDefinition.GetName(), "namespace", compositionDefinition.GetNamespace())
		return nil, err
	}
	repo, _, err := unstructured.NestedString(compositionDefinition.UnstructuredContent(), "spec", "chart", "repo")
	if err != nil {
		g.logger.Debug("Failed to resolve 'spec.chart.repo'", "error", err.Error(), "name", compositionDefinition.GetName(), "namespace", compositionDefinition.GetNamespace())
		return nil, err
	}

	username, _, err := unstructured.NestedString(compositionDefinition.UnstructuredContent(), "spec", "chart", "credentials", "username")
	if err != nil {
		g.logger.Debug("Failed to resolve 'spec.chart.credentials.username'", "error", err.Error(), "name", compositionDefinition.GetName(), "namespace", compositionDefinition.GetNamespace())
		return nil, err
	}

	passwordRef, _, err := unstructured.NestedStringMap(compositionDefinition.UnstructuredContent(), "spec", "chart", "credentials", "passwordRef")
	if err != nil {
		g.logger.Debug("Failed to resolve 'spec.chart.credentials.passwordRef'", "error", err.Error(), "name", compositionDefinition.GetName(), "namespace", compositionDefinition.GetNamespace())
		return nil, err
	}

	var password string
	if passwordRef != nil {
		password, err = GetSecret(context.Background(), g.dynamicClient, SecretKeySelector{
			Name:      passwordRef["name"],
			Namespace: passwordRef["namespace"],
			Key:       passwordRef["key"],
		})
		if err != nil {
			g.logger.Debug("Failed to resolve secret", "error", err.Error(), "name", passwordRef["name"], "namespace", passwordRef["namespace"])
			return nil, err
		}
	}
	insecureSkipTLSverify, _, err := unstructured.NestedBool(compositionDefinition.UnstructuredContent(), "spec", "chart", "insecureSkipTLSverify")
	if err != nil {
		g.logger.Debug("Failed to resolve 'spec.chart.insecureSkipTLSverify'", "error", err.Error(), "name", compositionDefinition.GetName(), "namespace", compositionDefinition.GetNamespace())
		return nil, err
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
			UID:       compositionDefinition.GetUID(),
			Namespace: compositionDefinition.GetNamespace(),
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
