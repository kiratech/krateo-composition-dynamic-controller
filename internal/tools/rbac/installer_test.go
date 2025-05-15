package rbac

import (
	"context"
	"os"
	"testing"

	"github.com/krateoplatformops/plumbing/e2e"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"
	"sigs.k8s.io/e2e-framework/pkg/features"
	"sigs.k8s.io/e2e-framework/support/kind"

	xenv "github.com/krateoplatformops/plumbing/env"
)

var (
	testenv     env.Environment
	clusterName string
	namespace   string
)

func TestMain(m *testing.M) {
	xenv.SetTestMode(true)

	namespace = "demo-system"
	altNamespace := "krateo-system"
	clusterName = "krateo"
	testenv = env.New()

	testenv.Setup(
		envfuncs.CreateCluster(kind.NewProvider(), clusterName),
		e2e.CreateNamespace(namespace),
		e2e.CreateNamespace(altNamespace),

		func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			r, err := resources.New(cfg.Client().RESTConfig())
			if err != nil {
				return ctx, err
			}

			r.WithNamespace(namespace)

			return ctx, nil
		},
	).Finish(
		envfuncs.DeleteNamespace(namespace),
		envfuncs.DestroyCluster(clusterName),
	)

	os.Exit(testenv.Run(m))
}

func TestApplyRole(t *testing.T) {
	f := features.New("Setup").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("Install", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		dyn := dynamic.NewForConfigOrDie(cfg.Client().RESTConfig())

		installer := &RBACInstaller{DynamicClient: dyn}

		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-role",
				Namespace: "default",
			},
		}

		_, err := installer.ApplyRole(context.Background(), role)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		return ctx
	}).Feature()

	testenv.Test(t, f)
}

func TestApplyRole_OverwriteExisting(t *testing.T) {
	f := features.New("Setup").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("Install", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		dyn := dynamic.NewForConfigOrDie(cfg.Client().RESTConfig())

		installer := &RBACInstaller{DynamicClient: dyn}
		// Create initial role
		initialRole := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-role",
				Namespace: "default",
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"pods"},
				},
			},
		}

		_, err := installer.ApplyRole(context.Background(), initialRole)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Apply new role with the same name but different rules
		newRole := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-role",
				Namespace: "default",
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"list"},
					APIGroups: []string{""},
					Resources: []string{"pods"},
				},
			},
		}

		_, err = installer.ApplyRole(context.Background(), newRole)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Retrieve the role from the cluster and check if it matches the new role
		retrievedRole, err := installer.ApplyRole(context.Background(), newRole)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if len(retrievedRole.Rules) != len(newRole.Rules) || retrievedRole.Rules[0].Verbs[0] != newRole.Rules[0].Verbs[0] {
			t.Errorf("expected role rules %v, got %v", newRole.Rules, retrievedRole.Rules)
		}
		return ctx
	}).Feature()

	testenv.Test(t, f)

}
func TestApplyClusterRole(t *testing.T) {
	f := features.New("Setup").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("Install", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		dyn := dynamic.NewForConfigOrDie(cfg.Client().RESTConfig())

		installer := &RBACInstaller{DynamicClient: dyn}

		clusterRole := &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-clusterrole",
			},
		}

		_, err := installer.ApplyClusterRole(context.Background(), clusterRole)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		return ctx
	}).Feature()

	testenv.Test(t, f)
}

func TestApplyClusterRoleBinding(t *testing.T) {
	f := features.New("Setup").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("Install", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		dyn := dynamic.NewForConfigOrDie(cfg.Client().RESTConfig())

		installer := &RBACInstaller{DynamicClient: dyn}

		clusterRoleBinding := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-clusterrolebinding",
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     "test-clusterrole",
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "User",
					Name:      "test-user",
					Namespace: "default",
				},
			},
		}

		_, err := installer.ApplyClusterRoleBinding(context.Background(), clusterRoleBinding)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		return ctx
	}).Feature()

	testenv.Test(t, f)
}

func TestApplyRoleBinding(t *testing.T) {
	f := features.New("Setup").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("Install", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		dyn := dynamic.NewForConfigOrDie(cfg.Client().RESTConfig())

		installer := &RBACInstaller{DynamicClient: dyn}

		roleBinding := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-rolebinding",
				Namespace: "default",
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     "test-role",
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "User",
					Name:      "test-user",
					Namespace: "default",
				},
			},
		}

		_, err := installer.ApplyRoleBinding(context.Background(), roleBinding)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		return ctx
	}).Feature()

	testenv.Test(t, f)
}

func TestApplyRole_NamespaceNotFound(t *testing.T) {
	f := features.New("Setup").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("Install", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		dyn := dynamic.NewForConfigOrDie(cfg.Client().RESTConfig())

		installer := &RBACInstaller{DynamicClient: dyn}

		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-role",
				Namespace: "non-existent-namespace",
			},
		}

		_, err := installer.ApplyRole(context.Background(), role)
		if err == nil {
			t.Fatalf("expected error, got none")
		}
		return ctx
	}).Feature()

	testenv.Test(t, f)
}

func TestApplyClusterRole_OverwriteExisting(t *testing.T) {
	f := features.New("Setup").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("Install", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		dyn := dynamic.NewForConfigOrDie(cfg.Client().RESTConfig())

		installer := &RBACInstaller{DynamicClient: dyn}
		// Create initial cluster role
		initialClusterRole := &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-clusterrole",
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"pods"},
				},
			},
		}

		_, err := installer.ApplyClusterRole(context.Background(), initialClusterRole)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Apply new cluster role with the same name but different rules
		newClusterRole := &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-clusterrole",
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"list"},
					APIGroups: []string{""},
					Resources: []string{"pods"},
				},
			},
		}

		_, err = installer.ApplyClusterRole(context.Background(), newClusterRole)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Retrieve the cluster role from the cluster and check if it matches the new cluster role
		retrievedClusterRole, err := installer.ApplyClusterRole(context.Background(), newClusterRole)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if len(retrievedClusterRole.Rules) != len(newClusterRole.Rules) || retrievedClusterRole.Rules[0].Verbs[0] != newClusterRole.Rules[0].Verbs[0] {
			t.Errorf("expected cluster role rules %v, got %v", newClusterRole.Rules, retrievedClusterRole.Rules)
		}
		return ctx
	}).Feature()

	testenv.Test(t, f)
}

func TestApplyRoleBinding_NamespaceNotFound(t *testing.T) {
	f := features.New("Setup").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("Install", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		dyn := dynamic.NewForConfigOrDie(cfg.Client().RESTConfig())

		installer := &RBACInstaller{DynamicClient: dyn}

		roleBinding := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-rolebinding",
				Namespace: "non-existent-namespace",
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     "test-role",
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "User",
					Name:      "test-user",
					Namespace: "default",
				},
			},
		}

		_, err := installer.ApplyRoleBinding(context.Background(), roleBinding)
		if err == nil {
			t.Fatalf("expected error, got none")
		}
		return ctx
	}).Feature()

	testenv.Test(t, f)
}
func TestApplyRBAC(t *testing.T) {
	f := features.New("Setup").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("Install", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		dyn := dynamic.NewForConfigOrDie(cfg.Client().RESTConfig())

		installer := &RBACInstaller{DynamicClient: dyn}

		rbac := &RBAC{
			ClusterRole: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clusterrole",
				},
			},
			ClusterRoleBinding: &rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clusterrolebinding",
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "test-clusterrole",
				},
				Subjects: []rbacv1.Subject{
					{
						Kind: "User",
						Name: "test-user",
					},
				},
			},
			Namespaced: map[string]Namespaced{
				"default": {
					Role: &rbacv1.Role{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-role",
							Namespace: "default",
						},
					},
					RoleBinding: &rbacv1.RoleBinding{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-rolebinding",
							Namespace: "default",
						},
						RoleRef: rbacv1.RoleRef{
							APIGroup: "rbac.authorization.k8s.io",
							Kind:     "Role",
							Name:     "test-role",
						},
						Subjects: []rbacv1.Subject{
							{
								Kind:      "User",
								Name:      "test-user",
								Namespace: "default",
							},
						},
					},
				},
			},
		}

		err := installer.ApplyRBAC(rbac)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		return ctx
	}).Feature()

	testenv.Test(t, f)
}

func TestApplyRBAC_OverwriteExisting(t *testing.T) {
	f := features.New("Setup").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("Install", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		dyn := dynamic.NewForConfigOrDie(cfg.Client().RESTConfig())

		installer := &RBACInstaller{DynamicClient: dyn}

		// Create initial RBAC
		initialRBAC := &RBAC{
			ClusterRole: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clusterrole",
				},
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"get"},
						APIGroups: []string{""},
						Resources: []string{"pods"},
					},
				},
			},
			ClusterRoleBinding: &rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clusterrolebinding",
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "test-clusterrole",
				},
				Subjects: []rbacv1.Subject{
					{
						Kind: "User",
						Name: "test-user",
					},
				},
			},
			Namespaced: map[string]Namespaced{
				"default": {
					Role: &rbacv1.Role{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-role",
							Namespace: "default",
						},
						Rules: []rbacv1.PolicyRule{
							{
								Verbs:     []string{"get"},
								APIGroups: []string{""},
								Resources: []string{"pods"},
							},
						},
					},
					RoleBinding: &rbacv1.RoleBinding{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-rolebinding",
							Namespace: "default",
						},
						RoleRef: rbacv1.RoleRef{
							APIGroup: "rbac.authorization.k8s.io",
							Kind:     "Role",
							Name:     "test-role",
						},
						Subjects: []rbacv1.Subject{
							{
								Kind:      "User",
								Name:      "test-user",
								Namespace: "default",
							},
						},
					},
				},
			},
		}

		err := installer.ApplyRBAC(initialRBAC)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Apply new RBAC with the same names but different rules
		newRBAC := &RBAC{
			ClusterRole: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clusterrole",
				},
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"list"},
						APIGroups: []string{""},
						Resources: []string{"pods"},
					},
				},
			},
			ClusterRoleBinding: &rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clusterrolebinding",
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "test-clusterrole",
				},
				Subjects: []rbacv1.Subject{
					{
						Kind: "User",
						Name: "test-user",
					},
				},
			},
			Namespaced: map[string]Namespaced{
				"default": {
					Role: &rbacv1.Role{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-role",
							Namespace: "default",
						},
						Rules: []rbacv1.PolicyRule{
							{
								Verbs:     []string{"list"},
								APIGroups: []string{""},
								Resources: []string{"pods"},
							},
						},
					},
					RoleBinding: &rbacv1.RoleBinding{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-rolebinding",
							Namespace: "default",
						},
						RoleRef: rbacv1.RoleRef{
							APIGroup: "rbac.authorization.k8s.io",
							Kind:     "Role",
							Name:     "test-role",
						},
						Subjects: []rbacv1.Subject{
							{
								Kind:      "User",
								Name:      "test-user",
								Namespace: "default",
							},
						},
					},
				},
			},
		}

		err = installer.ApplyRBAC(newRBAC)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Retrieve the RBAC from the cluster and check if it matches the new RBAC
		retrievedClusterRole, err := installer.ApplyClusterRole(context.Background(), newRBAC.ClusterRole)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if len(retrievedClusterRole.Rules) != len(newRBAC.ClusterRole.Rules) || retrievedClusterRole.Rules[0].Verbs[0] != newRBAC.ClusterRole.Rules[0].Verbs[0] {
			t.Errorf("expected cluster role rules %v, got %v", newRBAC.ClusterRole.Rules, retrievedClusterRole.Rules)
		}

		retrievedRole, err := installer.ApplyRole(context.Background(), newRBAC.Namespaced["default"].Role)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if len(retrievedRole.Rules) != len(newRBAC.Namespaced["default"].Role.Rules) || retrievedRole.Rules[0].Verbs[0] != newRBAC.Namespaced["default"].Role.Rules[0].Verbs[0] {
			t.Errorf("expected role rules %v, got %v", newRBAC.Namespaced["default"].Role.Rules, retrievedRole.Rules)
		}

		return ctx
	}).Feature()

	testenv.Test(t, f)
}

func TestUninstallRole(t *testing.T) {
	f := features.New("Uninstall").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("Uninstall", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		dyn := dynamic.NewForConfigOrDie(cfg.Client().RESTConfig())

		installer := &RBACInstaller{DynamicClient: dyn}

		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-role",
				Namespace: "default",
			},
		}

		// Apply the role first
		_, err := installer.ApplyRole(context.Background(), role)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Uninstall the role
		err = installer.DeleteRole(context.Background(), role.Namespace, role.Name)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Verify the role is deleted
		_, err = dyn.Resource(
			schema.GroupVersionResource{
				Group:    "rbac.authorization.k8s.io",
				Version:  "v1",
				Resource: "roles",
			},
		).Namespace(role.Namespace).Get(ctx, role.Name, metav1.GetOptions{})
		if !errors.IsNotFound(err) {
			t.Fatalf("expected not found error, got none")
		}
		return ctx
	}).Feature()

	testenv.Test(t, f)
}

func TestUninstallClusterRole(t *testing.T) {
	f := features.New("Uninstall").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("Uninstall", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		dyn := dynamic.NewForConfigOrDie(cfg.Client().RESTConfig())

		installer := &RBACInstaller{DynamicClient: dyn}

		clusterRole := &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-clusterrole",
			},
		}

		// Apply the cluster role first
		_, err := installer.ApplyClusterRole(context.Background(), clusterRole)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Uninstall the cluster role
		err = installer.DeleteClusterRole(context.Background(), clusterRole.Name)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Verify the cluster role is deleted
		_, err = dyn.Resource(
			schema.GroupVersionResource{
				Group:    "rbac.authorization.k8s.io",
				Version:  "v1",
				Resource: "clusterroles",
			},
		).Namespace(clusterRole.Namespace).Get(ctx, clusterRole.Name, metav1.GetOptions{})
		if !errors.IsNotFound(err) {
			t.Fatalf("expected not found error, got none")
		}
		return ctx
	}).Feature()

	testenv.Test(t, f)
}

func TestUninstallClusterRoleBinding(t *testing.T) {
	f := features.New("Uninstall").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("Uninstall", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		dyn := dynamic.NewForConfigOrDie(cfg.Client().RESTConfig())

		installer := &RBACInstaller{DynamicClient: dyn}

		clusterRoleBinding := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-clusterrolebinding",
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     "test-clusterrole",
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "User",
					Name:      "test-user",
					Namespace: "default",
				},
			},
		}

		// Apply the cluster role binding first
		_, err := installer.ApplyClusterRoleBinding(context.Background(), clusterRoleBinding)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Uninstall the cluster role binding
		err = installer.DeleteClusterRoleBinding(context.Background(), clusterRoleBinding.Name)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Verify the cluster role binding is deleted
		_, err = dyn.Resource(
			schema.GroupVersionResource{
				Group:    "rbac.authorization.k8s.io",
				Version:  "v1",
				Resource: "clusterrolebindings",
			},
		).Namespace(clusterRoleBinding.Namespace).Get(ctx, clusterRoleBinding.Name, metav1.GetOptions{})
		if !errors.IsNotFound(err) {
			t.Fatalf("expected not found error, got none")
		}
		return ctx
	}).Feature()

	testenv.Test(t, f)
}

func TestUninstallRoleBinding(t *testing.T) {
	f := features.New("Uninstall").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("Uninstall", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		dyn := dynamic.NewForConfigOrDie(cfg.Client().RESTConfig())

		installer := &RBACInstaller{DynamicClient: dyn}

		roleBinding := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-rolebinding",
				Namespace: "default",
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     "test-role",
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "User",
					Name:      "test-user",
					Namespace: "default",
				},
			},
		}

		// Apply the role binding first
		_, err := installer.ApplyRoleBinding(context.Background(), roleBinding)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Uninstall the role binding
		err = installer.DeleteRoleBinding(context.Background(), roleBinding.Namespace, roleBinding.Name)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Verify the role binding is deleted
		_, err = dyn.Resource(
			schema.GroupVersionResource{
				Group:    "rbac.authorization.k8s.io",
				Version:  "v1",
				Resource: "rolebindings",
			},
		).Namespace(roleBinding.Namespace).Get(ctx, roleBinding.Name, metav1.GetOptions{})
		if !errors.IsNotFound(err) {
			t.Fatalf("expected not found error, got none")
		}
		return ctx
	}).Feature()

	testenv.Test(t, f)
}

func TestUninstallRBAC(t *testing.T) {
	f := features.New("Uninstall").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).Assess("Uninstall", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		dyn := dynamic.NewForConfigOrDie(cfg.Client().RESTConfig())

		installer := &RBACInstaller{DynamicClient: dyn}

		rbac := &RBAC{
			ClusterRole: &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clusterrole",
				},
			},
			ClusterRoleBinding: &rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clusterrolebinding",
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "test-clusterrole",
				},
				Subjects: []rbacv1.Subject{
					{
						Kind: "User",
						Name: "test-user",
					},
				},
			},
			Namespaced: map[string]Namespaced{
				"default": {
					Role: &rbacv1.Role{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-role",
							Namespace: "default",
						},
					},
					RoleBinding: &rbacv1.RoleBinding{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-rolebinding",
							Namespace: "default",
						},
						RoleRef: rbacv1.RoleRef{
							APIGroup: "rbac.authorization.k8s.io",
							Kind:     "Role",
							Name:     "test-role",
						},
						Subjects: []rbacv1.Subject{
							{
								Kind:      "User",
								Name:      "test-user",
								Namespace: "default",
							},
						},
					},
				},
			},
		}

		// Apply the RBAC first
		err := installer.ApplyRBAC(rbac)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Uninstall the RBAC
		err = installer.UninstallRBAC(rbac)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		return ctx
	}).Feature()

	testenv.Test(t, f)
}
