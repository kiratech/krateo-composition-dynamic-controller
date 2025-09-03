package composition

import (
	"testing"

	"github.com/krateoplatformops/unstructured-runtime/pkg/controller/objectref"
	"github.com/krateoplatformops/unstructured-runtime/pkg/pluralizer"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type mockPluralizer struct {
	gvrMap map[schema.GroupVersionKind]schema.GroupVersionResource
	errMap map[schema.GroupVersionKind]error
}

func (m *mockPluralizer) GVKtoGVR(gvk schema.GroupVersionKind) (schema.GroupVersionResource, error) {
	if err, exists := m.errMap[gvk]; exists {
		return schema.GroupVersionResource{}, err
	}
	if gvr, exists := m.gvrMap[gvk]; exists {
		return gvr, nil
	}
	return schema.GroupVersionResource{}, nil
}

func TestPopulateManagedResources(t *testing.T) {
	tests := []struct {
		name        string
		pluralizer  pluralizer.PluralizerInterface
		resources   []objectref.ObjectRef
		expected    []interface{}
		expectError bool
	}{
		{
			name: "empty resources",
			pluralizer: &mockPluralizer{
				gvrMap: make(map[schema.GroupVersionKind]schema.GroupVersionResource),
				errMap: make(map[schema.GroupVersionKind]error),
			},
			resources: []objectref.ObjectRef{},
			expected:  []interface{}{},
		},
		{
			name: "namespaced resource with group",
			pluralizer: &mockPluralizer{
				gvrMap: map[schema.GroupVersionKind]schema.GroupVersionResource{
					{Group: "apps", Version: "v1", Kind: "Deployment"}: {Group: "apps", Version: "v1", Resource: "deployments"},
				},
				errMap: make(map[schema.GroupVersionKind]error),
			},
			resources: []objectref.ObjectRef{
				{APIVersion: "apps/v1", Kind: "Deployment", Name: "test-deployment", Namespace: "default"},
			},
			expected: []interface{}{
				ManagedResource{
					APIVersion: "apps/v1",
					Resource:   "deployments",
					Name:       "test-deployment",
					Namespace:  "default",
					Path:       "/apis/apps/v1/namespaces/default/deployments/test-deployment",
				},
			},
		},
		{
			name: "cluster scoped resource with group",
			pluralizer: &mockPluralizer{
				gvrMap: map[schema.GroupVersionKind]schema.GroupVersionResource{
					{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"}: {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"},
				},
				errMap: make(map[schema.GroupVersionKind]error),
			},
			resources: []objectref.ObjectRef{
				{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole", Name: "test-clusterrole", Namespace: ""},
			},
			expected: []interface{}{
				ManagedResource{
					APIVersion: "rbac.authorization.k8s.io/v1",
					Resource:   "clusterroles",
					Name:       "test-clusterrole",
					Namespace:  "",
					Path:       "/apis/rbac.authorization.k8s.io/v1/clusterroles/test-clusterrole",
				},
			},
		},
		{
			name: "core group namespaced resource",
			pluralizer: &mockPluralizer{
				gvrMap: map[schema.GroupVersionKind]schema.GroupVersionResource{
					{Group: "", Version: "v1", Kind: "Pod"}: {Group: "", Version: "v1", Resource: "pods"},
				},
				errMap: make(map[schema.GroupVersionKind]error),
			},
			resources: []objectref.ObjectRef{
				{APIVersion: "v1", Kind: "Pod", Name: "test-pod", Namespace: "default"},
			},
			expected: []interface{}{
				ManagedResource{
					APIVersion: "v1",
					Resource:   "pods",
					Name:       "test-pod",
					Namespace:  "default",
					Path:       "/api/v1/namespaces/default/pods/test-pod",
				},
			},
		},
		{
			name: "core group cluster scoped resource",
			pluralizer: &mockPluralizer{
				gvrMap: map[schema.GroupVersionKind]schema.GroupVersionResource{
					{Group: "", Version: "v1", Kind: "Node"}: {Group: "", Version: "v1", Resource: "nodes"},
				},
				errMap: make(map[schema.GroupVersionKind]error),
			},
			resources: []objectref.ObjectRef{
				{APIVersion: "v1", Kind: "Node", Name: "test-node", Namespace: ""},
			},
			expected: []interface{}{
				ManagedResource{
					APIVersion: "v1",
					Resource:   "nodes",
					Name:       "test-node",
					Namespace:  "",
					Path:       "/api/v1/nodes/test-node",
				},
			},
		},
		{
			name: "multiple resources",
			pluralizer: &mockPluralizer{
				gvrMap: map[schema.GroupVersionKind]schema.GroupVersionResource{
					{Group: "apps", Version: "v1", Kind: "Deployment"}: {Group: "apps", Version: "v1", Resource: "deployments"},
					{Group: "", Version: "v1", Kind: "Service"}:        {Group: "", Version: "v1", Resource: "services"},
				},
				errMap: make(map[schema.GroupVersionKind]error),
			},
			resources: []objectref.ObjectRef{
				{APIVersion: "apps/v1", Kind: "Deployment", Name: "test-deployment", Namespace: "default"},
				{APIVersion: "v1", Kind: "Service", Name: "test-service", Namespace: "default"},
			},
			expected: []interface{}{
				ManagedResource{
					APIVersion: "apps/v1",
					Resource:   "deployments",
					Name:       "test-deployment",
					Namespace:  "default",
					Path:       "/apis/apps/v1/namespaces/default/deployments/test-deployment",
				},
				ManagedResource{
					APIVersion: "v1",
					Resource:   "services",
					Name:       "test-service",
					Namespace:  "default",
					Path:       "/api/v1/namespaces/default/services/test-service",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := populateManagedResources(tt.pluralizer, tt.resources)

			if tt.expectError && err == nil {
				t.Fatal("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d managed resources, got %d", len(tt.expected), len(result))
			}

			for i, expected := range tt.expected {
				expectedRes := expected.(ManagedResource)
				actualRes := result[i].(ManagedResource)

				if expectedRes.APIVersion != actualRes.APIVersion {
					t.Errorf("APIVersion mismatch at index %d: expected %s, got %s", i, expectedRes.APIVersion, actualRes.APIVersion)
				}
				if expectedRes.Resource != actualRes.Resource {
					t.Errorf("Resource mismatch at index %d: expected %s, got %s", i, expectedRes.Resource, actualRes.Resource)
				}
				if expectedRes.Name != actualRes.Name {
					t.Errorf("Name mismatch at index %d: expected %s, got %s", i, expectedRes.Name, actualRes.Name)
				}
				if expectedRes.Namespace != actualRes.Namespace {
					t.Errorf("Namespace mismatch at index %d: expected %s, got %s", i, expectedRes.Namespace, actualRes.Namespace)
				}
				if expectedRes.Path != actualRes.Path {
					t.Errorf("Path mismatch at index %d: expected %s, got %s", i, expectedRes.Path, actualRes.Path)
				}
			}
		})
	}
}
