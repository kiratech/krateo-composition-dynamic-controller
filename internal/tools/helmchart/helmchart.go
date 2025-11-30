package helmchart

import (
	"context"
	"fmt"
	"io"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"

	"github.com/krateoplatformops/unstructured-runtime/pkg/pluralizer"
	unstructuredtools "github.com/krateoplatformops/unstructured-runtime/pkg/tools/unstructured"

	"github.com/krateoplatformops/composition-dynamic-controller/internal/helmclient"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/tools/hasher"
	"github.com/krateoplatformops/unstructured-runtime/pkg/controller/objectref"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigsyaml "sigs.k8s.io/yaml"

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

// MinimalMetadata holds only the necessary fields for reference extraction.
// Decoding into this struct is significantly cheaper than map[string]any.
type minimalMetadata struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		// Use the map to capture annotations, including the helm hook
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
}

func GetResourcesRefFromRelease(rel *release.Release, defaultNamespace string, clientset helmclient.CachedClientsInterface) ([]objectref.ObjectRef, string, error) {

	// The hasher must implement io.Writer to allow incremental hashing.
	var hasher = hasher.NewFNVObjectHash()
	// build an io.Reader that streams manifest + hooks without concatenating into a single []byte
	var readers []io.Reader
	if rel != nil {
		if strings.TrimSpace(rel.Manifest) != "" {
			readers = append(readers, strings.NewReader(rel.Manifest))
		}
		for _, h := range rel.Hooks {
			// include hook marker to preserve document boundary
			hdr := fmt.Sprintf("\n---\n# Source: %s\n", h.Path)
			readers = append(readers, strings.NewReader(hdr+h.Manifest))
		}
	}

	all := []objectref.ObjectRef{}
	if len(readers) == 0 {
		return all, "", nil
	}

	combined := io.MultiReader(readers...)

	// 1. Set up io.Pipe for concurrent stream processing.
	// pr is the Reader for the decoder, pw is the Writer fed by the goroutine.
	pr, pw := io.Pipe()

	// 2. Use io.MultiWriter to pipe the stream to both:
	//    a) The PipeWriter (pw), which feeds the decoder in the main routine.
	//    b) The Hasher, which calculates the hash incrementally.
	mw := io.MultiWriter(pw, hasher)

	// 3. Start a goroutine to copy the combined manifest into the MultiWriter concurrently.
	go func() {
		// io.Copy reads from 'combined' (source) and writes to 'mw' (destinations).
		// It's crucial to close the PipeWriter (pw) when finished, or the decoder will hang.
		_, err := io.Copy(mw, combined)
		pw.CloseWithError(err)
	}()

	// The decoder now reads from the PipeReader (pr).
	decoder := yamlutil.NewYAMLOrJSONDecoder(pr, 4096)
	for {
		var doc minimalMetadata
		if err := decoder.Decode(&doc); err != nil {
			if err == io.EOF {
				break
			}
			// skip invalid doc but continue processing others
			continue
		}

		apiVersion := doc.APIVersion
		kind := doc.Kind
		name := doc.Metadata.Name
		namespace := doc.Metadata.Namespace
		hook := doc.Metadata.Annotations["helm.sh/hook"]

		if namespace == "" {
			namespace = defaultNamespace

			// Check if the resource is cluster-scoped
			gvk := schema.FromAPIVersionAndKind(apiVersion, kind)
			mapping, err := clientset.RESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version)
			if err != nil {
				// Close the reader pipe on error to ensure the goroutine doesn't hang
				pr.Close()
				return nil, "", fmt.Errorf("failed to get REST mapping for %s: %w", gvk.String(), err)
			}
			if mapping.Scope.Name() == meta.RESTScopeNameRoot {
				namespace = ""
			}
		}
		// skip helm hooks if present
		if hook != "" {
			continue
		}
		// skip empty documents
		if apiVersion == "" && kind == "" && name == "" {
			continue
		}

		all = append(all, objectref.ObjectRef{
			APIVersion: apiVersion,
			Kind:       kind,
			Name:       name,
			Namespace:  namespace,
		})
	}

	// Ensure the PipeReader is closed after the loop to clean up the pipe resources.
	pr.Close()

	// The hasher already holds the final hash digest, calculated incrementally.
	// We use GetHash() to retrieve the final hash string.
	return all, hasher.GetHash(), nil
}

type CheckResourceOptions struct {
	DynamicClient dynamic.Interface
	Pluralizer    pluralizer.PluralizerInterface
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
	rel, err := hc.GetRelease(name)
	if err != nil {
		if strings.Contains(err.Error(), "release: not found") {
			return nil, nil
		}
		return nil, err
	}
	return rel, nil
}

func FindAllReleases(hc helmclient.Client) ([]*release.Release, error) {
	all, err := hc.ListReleasesByStateMask(action.ListAll)
	if err != nil {
		return nil, err
	}

	res := make([]*release.Release, 0, len(all))
	res = append(res, all...)

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
