package rbacgen

import (
	"errors"
	"testing"

	"github.com/krateoplatformops/composition-dynamic-controller/internal/chartinspector"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/tools/rbac"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

type MockChartInspector struct {
	mock.Mock
}

func (m *MockChartInspector) Resources(compositionDefinitionUID, compositionDefinitionNamespace, compositionUID, compositionNamespace string) ([]chartinspector.Resource, error) {
	args := m.Called(compositionDefinitionUID, compositionDefinitionNamespace, compositionUID, compositionNamespace)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]chartinspector.Resource), args.Error(1)
}

var _ chartinspector.ChartInspectorInterface = &MockChartInspector{}

func TestRBACGen_Generate(t *testing.T) {

	t.Run("successfully generates RBAC policies", func(t *testing.T) {
		mockInspector := new(MockChartInspector)
		rbacGen := NewRBACGen("test-sa", "test-namespace", mockInspector).WithBaseName("test-base")
		mockResources := []chartinspector.Resource{
			{Group: "group1", Resource: "resource1", Name: "name1"},
			{Group: "group2", Resource: "resource2", Name: "name2", Namespace: "namespace1"},
			{Group: "", Resource: "resource3", Name: "name3"},
			{Group: "", Resource: "resource4", Name: "name4", Namespace: "namespace1"},
			{Group: "", Resource: "namespaces", Version: "v1", Name: "namespace1"},
		}
		mockInspector.On("Resources", "compDefUID", "compDefNS", "compUID", "compNS").Return(mockResources, nil)

		expectedPolicy := &rbac.RBAC{
			ClusterRole:        rbac.InitClusterRole("test-base"),
			ClusterRoleBinding: rbac.InitClusterRoleBinding("test-base", "test-base", "test-sa", "test-namespace"),
			Namespaced: map[string]rbac.Namespaced{
				"namespace1": {
					Role:        rbac.InitRole("test-base", "namespace1"),
					RoleBinding: rbac.InitRoleBinding("test-base", "test-base", "namespace1", "test-sa", "test-namespace"),
				},
			},
			Namespaces: []*corev1.Namespace{},
		}

		expectedPolicy.Namespaces = append(expectedPolicy.Namespaces, rbac.CreateNamespace("namespace1", "test-base", "compNS"))

		expectedPolicy.ClusterRole.Rules = append(expectedPolicy.ClusterRole.Rules, rbacv1.PolicyRule{
			APIGroups: []string{"group1"},
			Resources: []string{"resource1"},
			Verbs:     []string{"*"},
			// ResourceNames: []string{"name1"},
		})
		expectedPolicy.Namespaced["namespace1"].Role.Rules = append(expectedPolicy.Namespaced["namespace1"].Role.Rules, rbacv1.PolicyRule{
			APIGroups: []string{"group2"},
			Resources: []string{"resource2"},
			Verbs:     []string{"*"},
			// ResourceNames: []string{"name2"},
		})
		expectedPolicy.ClusterRole.Rules = append(expectedPolicy.ClusterRole.Rules, rbacv1.PolicyRule{
			APIGroups: []string{""},
			Resources: []string{"resource3"},
			Verbs:     []string{"*"},
			// ResourceNames: []string{"name3"},
		})

		expectedPolicy.ClusterRole.Rules = append(expectedPolicy.ClusterRole.Rules, rbacv1.PolicyRule{
			APIGroups: []string{""},
			Resources: []string{"namespaces"},
			Verbs:     []string{"*"},
			// ResourceNames: []string{"namespace1"},
		})

		expectedPolicy.Namespaced["namespace1"].Role.Rules = append(expectedPolicy.Namespaced["namespace1"].Role.Rules, rbacv1.PolicyRule{
			APIGroups: []string{""},
			Resources: []string{"resource4"},
			Verbs:     []string{"*"},
			// ResourceNames: []string{"name4"},
		})

		policy, err := rbacGen.Generate("compDefUID", "compDefNS", "compUID", "compNS")
		assert.NoError(t, err)
		assert.Equal(t, expectedPolicy, policy)
		mockInspector.AssertExpectations(t)
	})

	t.Run("returns error when chartInspector fails", func(t *testing.T) {
		mockInspector := new(MockChartInspector)
		rbacGen := NewRBACGen("test-sa", "test-namespace", mockInspector).WithBaseName("test-base")
		mockInspector.On("Resources", "compDefUID", "compDefNS", "compUID", "compNS").Return(nil, errors.New("some error"))

		policy, err := rbacGen.Generate("compDefUID", "compDefNS", "compUID", "compNS")

		assert.Error(t, err)
		assert.Nil(t, policy)
		assert.EqualError(t, err, "getting resources: some error")
		mockInspector.AssertExpectations(t)
	})
}
