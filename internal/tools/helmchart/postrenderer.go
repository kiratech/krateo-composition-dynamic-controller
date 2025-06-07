package helmchart

import (
	"bytes"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/kustomize/kyaml/kio"
)

type labelsPostRender struct {
	UID                  types.UID
	CompositionGVR       schema.GroupVersionResource
	CompositionName      string
	CompositionNamespace string
	CompositionGVK       schema.GroupVersionKind
	KrateoNamespace      string
}

func (r *labelsPostRender) Run(renderedManifests *bytes.Buffer) (modifiedManifests *bytes.Buffer, err error) {
	nodes, err := kio.FromBytes(renderedManifests.Bytes())
	if err != nil {
		return renderedManifests, errors.Wrap(err, "parse rendered manifests failed")
	}
	for _, v := range nodes {
		labels := v.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		// your labels
		labels["krateo.io/composition-id"] = string(r.UID)
		labels["krateo.io/composition-group"] = r.CompositionGVR.Group
		labels["krateo.io/composition-installed-version"] = r.CompositionGVR.Version
		labels["krateo.io/composition-resource"] = r.CompositionGVR.Resource
		labels["krateo.io/composition-name"] = r.CompositionName
		labels["krateo.io/composition-namespace"] = r.CompositionNamespace
		labels["krateo.io/composition-kind"] = r.CompositionGVK.Kind
		labels["krateo.io/krateo-namespace"] = r.KrateoNamespace
		v.SetLabels(labels)
	}

	str, err := kio.StringAll(nodes)
	if err != nil {
		return renderedManifests, errors.Wrap(err, "string all nodes failed")
	}

	return bytes.NewBufferString(str), nil
}
