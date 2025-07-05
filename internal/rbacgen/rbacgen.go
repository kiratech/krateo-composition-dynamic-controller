package rbacgen

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/utils/ptr"

	"github.com/krateoplatformops/composition-dynamic-controller/internal/chartinspector"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/tools/rbac"
)

type RBACGenInterface interface {
	Generate(compositionDefinitionUID, compositionDefinitionNamespace, compositionUID, compositionNamespace string) (*rbac.RBAC, error)
	WithBaseName(baseName string) RBACGenInterface
}

type RBACGen struct {
	chartInspector chartinspector.ChartInspectorInterface
	baseName       string
	saName         string
	saNamespace    string
}

var _ RBACGenInterface = &RBACGen{}

func NewRBACGen(saName string, saNamespace string, chartInspector chartinspector.ChartInspectorInterface) *RBACGen {
	return &RBACGen{
		chartInspector: chartInspector,
		saName:         saName,
		saNamespace:    saNamespace,
	}
}

func (r *RBACGen) WithBaseName(baseName string) RBACGenInterface {
	r.baseName = baseName
	return r
}

func (r *RBACGen) Generate(compositionDefinitionUID, compositionDefinitionNamespace, compositionUID, compositionNamespace string) (*rbac.RBAC, error) {
	resources, err := r.chartInspector.Resources(compositionDefinitionUID, compositionDefinitionNamespace, compositionUID, compositionNamespace)
	if err != nil {
		return nil, fmt.Errorf("getting resources: %w", err)
	}
	policy := rbac.RBAC{
		ClusterRole:        rbac.InitClusterRole(r.baseName),
		ClusterRoleBinding: rbac.InitClusterRoleBinding(r.baseName, r.baseName, r.saName, r.saNamespace),
		Namespaced:         map[string]rbac.Namespaced{},
		Namespaces:         []*corev1.Namespace{},
	}

	for _, resource := range resources {
		if resource.Namespace == "" {
			if resource.Group == "" && resource.Resource == "namespaces" && resource.Version == "v1" {
				// If the resource is a namespace, we need to create a namespace object
				policy.Namespaces = append(policy.Namespaces, rbac.CreateNamespace(resource.Name, r.baseName, compositionNamespace))
			}

			policy.ClusterRole.Rules = append(policy.ClusterRole.Rules, rbacv1.PolicyRule{
				APIGroups: []string{resource.Group},
				Resources: []string{resource.Resource},
				Verbs:     []string{"*"},
				// ResourceNames: []string{resource.Name},
			})
		} else {
			if _, ok := policy.Namespaced[resource.Namespace]; !ok {
				policy.Namespaced[resource.Namespace] = rbac.Namespaced{
					Role:        rbac.InitRole(r.baseName, resource.Namespace),
					RoleBinding: rbac.InitRoleBinding(r.baseName, r.baseName, resource.Namespace, r.saName, r.saNamespace),
				}
			}

			policy.Namespaced[resource.Namespace].Role.Rules = append(policy.Namespaced[resource.Namespace].Role.Rules, rbacv1.PolicyRule{
				APIGroups: []string{resource.Group},
				Resources: []string{resource.Resource},
				Verbs:     []string{"*"},
				// ResourceNames: []string{resource.Name},
			})
		}
	}
	return ptr.To(policy), nil
}
