package rbac

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type RBACInstaller struct {
	DynamicClient dynamic.Interface
}

func NewRBACInstaller(cli dynamic.Interface) *RBACInstaller {
	return &RBACInstaller{DynamicClient: cli}
}

func (i *RBACInstaller) ApplyRBAC(rbac *RBAC) error {
	if rbac == nil {
		return fmt.Errorf("RBAC cannot be nil")
	}

	if rbac.Namespaces != nil {
		for _, ns := range rbac.Namespaces {
			if ns != nil {
				_, err := i.ApplyNamespace(context.Background(), ns)
				if err != nil {
					return fmt.Errorf("failed to Apply Namespace %s: %w", ns.Name, err)
				}
			}
		}
	}

	if rbac.ClusterRole != nil {
		_, err := i.ApplyClusterRole(context.Background(), rbac.ClusterRole)
		if err != nil {
			return fmt.Errorf("failed to Apply ClusterRole: %w", err)
		}
	}
	if rbac.ClusterRoleBinding != nil {
		_, err := i.ApplyClusterRoleBinding(context.Background(), rbac.ClusterRoleBinding)
		if err != nil {
			return fmt.Errorf("failed to Apply ClusterRoleBinding: %w", err)
		}
	}
	for _, ns := range rbac.Namespaced {
		if ns.Role != nil {
			_, err := i.ApplyRole(context.Background(), ns.Role)
			if err != nil {
				return fmt.Errorf("failed to Apply Role: %w", err)
			}
		}
		if ns.RoleBinding != nil {
			_, err := i.ApplyRoleBinding(context.Background(), ns.RoleBinding)
			if err != nil {
				return fmt.Errorf("failed to Apply RoleBinding: %w", err)
			}
		}
	}
	return nil

}

func (r *RBACInstaller) ApplyNamespace(ctx context.Context, namespace *corev1.Namespace) (*corev1.Namespace, error) {
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to convert Namespace to unstructured: %w", err)
	}
	u := &unstructured.Unstructured{Object: m}
	cli := r.DynamicClient.Resource(
		schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "namespaces",
		},
	)

	if _, err := cli.Get(ctx, namespace.Name, metav1.GetOptions{}); errors.IsNotFound(err) {
		res, err := cli.Create(ctx, u, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to Create Namespace: %w", err)
		}
		namespace = &corev1.Namespace{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(res.Object, namespace)
		if err != nil {
			return nil, fmt.Errorf("failed to convert unstructured to Namespace: %w", err)
		}
		return namespace, nil
	}

	res, err := cli.Update(ctx, u, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to Update Namespace: %w", err)
	}
	namespace = &corev1.Namespace{}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(res.Object, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to convert unstructured to Namespace: %w", err)
	}
	return namespace, nil
}

func (i *RBACInstaller) ApplyRole(ctx context.Context, role *rbacv1.Role) (*rbacv1.Role, error) {
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(role)
	if err != nil {
		return nil, fmt.Errorf("failed to convert Role to unstructured: %w", err)
	}
	u := &unstructured.Unstructured{Object: m}

	cli := i.DynamicClient.Resource(
		schema.GroupVersionResource{
			Group:    "rbac.authorization.k8s.io",
			Version:  "v1",
			Resource: "roles",
		},
	).Namespace(role.Namespace)

	if _, err = cli.Get(ctx, role.Name, metav1.GetOptions{}); errors.IsNotFound(err) {
		res, err := cli.Create(ctx, u, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to Create Role: %w", err)
		}

		role = &rbacv1.Role{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(res.Object, role)
		if err != nil {
			return nil, fmt.Errorf("failed to convert unstructured to Role: %w", err)
		}

		return role, nil
	}

	res, err := cli.Update(ctx, u, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to Update Role: %w", err)
	}

	role = &rbacv1.Role{}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(res.Object, role)
	if err != nil {
		return nil, fmt.Errorf("failed to convert unstructured to Role: %w", err)
	}

	return role, nil
}

func (i *RBACInstaller) ApplyRoleBinding(ctx context.Context, roleBinding *rbacv1.RoleBinding) (*rbacv1.RoleBinding, error) {
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(roleBinding)
	if err != nil {
		return nil, fmt.Errorf("failed to convert RoleBinding to unstructured: %w", err)
	}
	u := &unstructured.Unstructured{Object: m}

	cli := i.DynamicClient.Resource(
		schema.GroupVersionResource{
			Group:    "rbac.authorization.k8s.io",
			Version:  "v1",
			Resource: "rolebindings",
		},
	).Namespace(roleBinding.Namespace)

	if _, err := cli.Get(ctx, roleBinding.Name, metav1.GetOptions{}); errors.IsNotFound(err) {
		res, err := cli.Create(ctx, u, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to Create RoleBinding: %w", err)
		}

		roleBinding = &rbacv1.RoleBinding{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(res.Object, roleBinding)
		if err != nil {
			return nil, fmt.Errorf("failed to convert unstructured to RoleBinding: %w", err)
		}

		return roleBinding, nil
	}

	res, err := cli.Update(ctx, u, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to Update RoleBinding: %w", err)
	}

	roleBinding = &rbacv1.RoleBinding{}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(res.Object, roleBinding)
	if err != nil {
		return nil, fmt.Errorf("failed to convert unstructured to RoleBinding: %w", err)
	}

	return roleBinding, nil
}

func (i *RBACInstaller) ApplyClusterRole(ctx context.Context, clusterRole *rbacv1.ClusterRole) (*rbacv1.ClusterRole, error) {
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(clusterRole)
	if err != nil {
		return nil, fmt.Errorf("failed to convert ClusterRole to unstructured: %w", err)
	}
	u := &unstructured.Unstructured{Object: m}

	cli := i.DynamicClient.Resource(
		schema.GroupVersionResource{
			Group:    "rbac.authorization.k8s.io",
			Version:  "v1",
			Resource: "clusterroles",
		},
	)

	if _, err := cli.Get(ctx, clusterRole.Name, metav1.GetOptions{}); errors.IsNotFound(err) {
		res, err := cli.Create(ctx, u, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to Create ClusterRole: %w", err)
		}

		clusterRole = &rbacv1.ClusterRole{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(res.Object, clusterRole)
		if err != nil {
			return nil, fmt.Errorf("failed to convert unstructured to ClusterRole: %w", err)
		}

		return clusterRole, nil
	}

	res, err := cli.Update(ctx, u, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to Update ClusterRole: %w", err)
	}

	clusterRole = &rbacv1.ClusterRole{}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(res.Object, clusterRole)
	if err != nil {
		return nil, fmt.Errorf("failed to convert unstructured to ClusterRole: %w", err)
	}
	return clusterRole, nil
}

func (i *RBACInstaller) ApplyClusterRoleBinding(ctx context.Context, clusterRoleBinding *rbacv1.ClusterRoleBinding) (*rbacv1.ClusterRoleBinding, error) {
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(clusterRoleBinding)
	if err != nil {
		return nil, fmt.Errorf("failed to convert ClusterRoleBinding to unstructured: %w", err)
	}
	u := &unstructured.Unstructured{Object: m}

	cli := i.DynamicClient.Resource(
		schema.GroupVersionResource{
			Group:    "rbac.authorization.k8s.io",
			Version:  "v1",
			Resource: "clusterrolebindings",
		},
	)

	if _, err := cli.Get(ctx, clusterRoleBinding.Name, metav1.GetOptions{}); errors.IsNotFound(err) {
		res, err := cli.Create(ctx, u, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to Create ClusterRoleBinding: %w", err)
		}

		clusterRoleBinding = &rbacv1.ClusterRoleBinding{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(res.Object, clusterRoleBinding)
		if err != nil {
			return nil, fmt.Errorf("failed to convert unstructured to ClusterRoleBinding: %w", err)
		}

		return clusterRoleBinding, nil
	}

	res, err := cli.Update(ctx, u, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to Update ClusterRoleBinding: %w", err)
	}

	clusterRoleBinding = &rbacv1.ClusterRoleBinding{}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(res.Object, clusterRoleBinding)
	if err != nil {
		return nil, fmt.Errorf("failed to convert unstructured to ClusterRoleBinding: %w", err)
	}

	return clusterRoleBinding, nil
}

func (i *RBACInstaller) UninstallRBAC(rbac *RBAC) error {
	if rbac == nil {
		return fmt.Errorf("RBAC cannot be nil")
	}
	// in this case it will not delete the namespaces, only the roles and bindings because Helm should delete the namespaces itself

	if rbac.ClusterRole != nil {
		err := i.DeleteClusterRole(context.Background(), rbac.ClusterRole.Name)
		if err != nil {
			return fmt.Errorf("failed to Delete ClusterRole: %w", err)
		}
	}
	if rbac.ClusterRoleBinding != nil {
		err := i.DeleteClusterRoleBinding(context.Background(), rbac.ClusterRoleBinding.Name)
		if err != nil {
			return fmt.Errorf("failed to Delete ClusterRoleBinding: %w", err)
		}
	}
	for _, ns := range rbac.Namespaced {
		if ns.Role != nil {
			err := i.DeleteRole(context.Background(), ns.Role.Namespace, ns.Role.Name)
			if err != nil {
				return fmt.Errorf("failed to Delete Role: %w", err)
			}
		}
		if ns.RoleBinding != nil {
			err := i.DeleteRoleBinding(context.Background(), ns.RoleBinding.Namespace, ns.RoleBinding.Name)
			if err != nil {
				return fmt.Errorf("failed to Delete RoleBinding: %w", err)
			}
		}
	}
	return nil
}

func (i *RBACInstaller) DeleteRole(ctx context.Context, namespace, name string) error {
	cli := i.DynamicClient.Resource(
		schema.GroupVersionResource{
			Group:    "rbac.authorization.k8s.io",
			Version:  "v1",
			Resource: "roles",
		},
	).Namespace(namespace)

	err := cli.Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to Delete Role: %w", err)
	}
	return nil
}

func (i *RBACInstaller) DeleteRoleBinding(ctx context.Context, namespace, name string) error {
	cli := i.DynamicClient.Resource(
		schema.GroupVersionResource{
			Group:    "rbac.authorization.k8s.io",
			Version:  "v1",
			Resource: "rolebindings",
		},
	).Namespace(namespace)

	err := cli.Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to Delete RoleBinding: %w", err)
	}
	return nil
}

func (i *RBACInstaller) DeleteClusterRole(ctx context.Context, name string) error {
	cli := i.DynamicClient.Resource(
		schema.GroupVersionResource{
			Group:    "rbac.authorization.k8s.io",
			Version:  "v1",
			Resource: "clusterroles",
		},
	)

	err := cli.Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to Delete ClusterRole: %w", err)
	}
	return nil
}

func (i *RBACInstaller) DeleteClusterRoleBinding(ctx context.Context, name string) error {
	cli := i.DynamicClient.Resource(
		schema.GroupVersionResource{
			Group:    "rbac.authorization.k8s.io",
			Version:  "v1",
			Resource: "clusterrolebindings",
		},
	)

	err := cli.Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to Delete ClusterRoleBinding: %w", err)
	}
	return nil
}
