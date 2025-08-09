package meta

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestGetReleaseName(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		expected string
	}{
		{
			name:     "release name exists",
			labels:   map[string]string{ReleaseNameLabel: "test-release"},
			expected: "test-release",
		},
		{
			name:     "release name does not exist",
			labels:   map[string]string{"other-label": "value"},
			expected: "",
		},
		{
			name:     "nil labels",
			labels:   nil,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// obj := &unstructured.U{labels: tt.labels}
			obj := unstructured.Unstructured{}
			obj.SetLabels(tt.labels)
			result := GetReleaseName(&obj)
			if result != tt.expected {
				t.Errorf("GetReleaseName() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestSetReleaseName(t *testing.T) {
	tests := []struct {
		name           string
		initialLabels  map[string]string
		releaseName    string
		expectedLabels map[string]string
	}{
		{
			name:          "set release name on nil labels",
			initialLabels: nil,
			releaseName:   "new-release",
			expectedLabels: map[string]string{
				ReleaseNameLabel: "new-release",
			},
		},
		{
			name:          "set release name on empty labels",
			initialLabels: map[string]string{},
			releaseName:   "new-release",
			expectedLabels: map[string]string{
				ReleaseNameLabel: "new-release",
			},
		},
		{
			name: "set release name with existing labels",
			initialLabels: map[string]string{
				"existing-label": "value",
			},
			releaseName: "new-release",
			expectedLabels: map[string]string{
				"existing-label": "value",
				ReleaseNameLabel: "new-release",
			},
		},
		{
			name: "do not overwrite existing release name",
			initialLabels: map[string]string{
				ReleaseNameLabel: "existing-release",
				"other-label":    "value",
			},
			releaseName: "new-release",
			expectedLabels: map[string]string{
				ReleaseNameLabel: "existing-release",
				"other-label":    "value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := unstructured.Unstructured{}
			obj.SetLabels(tt.initialLabels)
			SetReleaseName(&obj, tt.releaseName)

			labels := obj.GetLabels()
			if len(labels) != len(tt.expectedLabels) {
				t.Errorf("Expected %d labels, got %d", len(tt.expectedLabels), len(labels))
			}

			for key, expectedValue := range tt.expectedLabels {
				if actualValue, exists := labels[key]; !exists || actualValue != expectedValue {
					t.Errorf("Expected label %s=%s, got %s=%s", key, expectedValue, key, actualValue)
				}
			}
		})
	}
}

func TestSetCompositionDefinitionLabels(t *testing.T) {
	tests := []struct {
		name           string
		initialLabels  map[string]string
		cdInfo         CompositionDefinitionInfo
		expectedLabels map[string]string
	}{
		{
			name:          "set labels on nil labels",
			initialLabels: nil,
			cdInfo: CompositionDefinitionInfo{
				Namespace: "test-namespace",
				Name:      "test-name",
				GVR: schema.GroupVersionResource{
					Group:    "test.io",
					Version:  "v1",
					Resource: "testresources",
				},
			},
			expectedLabels: map[string]string{
				CompositionDefinitionNameLabel:      "test-name",
				CompositionDefinitionNamespaceLabel: "test-namespace",
				CompositionDefinitionGroupLabel:     "test.io",
				CompositionDefinitionVersionLabel:   "v1",
				CompositionDefinitionResourceLabel:  "testresources",
			},
		},
		{
			name: "set labels with existing labels",
			initialLabels: map[string]string{
				"existing-label": "value",
			},
			cdInfo: CompositionDefinitionInfo{
				Namespace: "test-namespace",
				Name:      "test-name",
				GVR: schema.GroupVersionResource{
					Group:    "test.io",
					Version:  "v1",
					Resource: "testresources",
				},
			},
			expectedLabels: map[string]string{
				"existing-label":                    "value",
				CompositionDefinitionNameLabel:      "test-name",
				CompositionDefinitionNamespaceLabel: "test-namespace",
				CompositionDefinitionGroupLabel:     "test.io",
				CompositionDefinitionVersionLabel:   "v1",
				CompositionDefinitionResourceLabel:  "testresources",
			},
		},
		{
			name: "overwrite existing composition definition labels",
			initialLabels: map[string]string{
				CompositionDefinitionNameLabel: "old-name",
				"other-label":                  "value",
			},
			cdInfo: CompositionDefinitionInfo{
				Namespace: "new-namespace",
				Name:      "new-name",
				GVR: schema.GroupVersionResource{
					Group:    "new.io",
					Version:  "v2",
					Resource: "newresources",
				},
			},
			expectedLabels: map[string]string{
				"other-label":                       "value",
				CompositionDefinitionNameLabel:      "new-name",
				CompositionDefinitionNamespaceLabel: "new-namespace",
				CompositionDefinitionGroupLabel:     "new.io",
				CompositionDefinitionVersionLabel:   "v2",
				CompositionDefinitionResourceLabel:  "newresources",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := unstructured.Unstructured{}
			obj.SetLabels(tt.initialLabels)
			SetCompositionDefinitionLabels(&obj, tt.cdInfo)

			labels := obj.GetLabels()
			if len(labels) != len(tt.expectedLabels) {
				t.Errorf("Expected %d labels, got %d", len(tt.expectedLabels), len(labels))
			}

			for key, expectedValue := range tt.expectedLabels {
				if actualValue, exists := labels[key]; !exists || actualValue != expectedValue {
					t.Errorf("Expected label %s=%s, got %s=%s", key, expectedValue, key, actualValue)
				}
			}
		})
	}
}
