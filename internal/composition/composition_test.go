//go:build integration
// +build integration

package composition

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	rbacv1 "k8s.io/api/rbac/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/gobuffalo/flect"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/chartinspector"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/rbacgen"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/tools/helmchart/archive"
	"github.com/krateoplatformops/unstructured-runtime/pkg/controller"
	"github.com/krateoplatformops/unstructured-runtime/pkg/eventrecorder"
	"github.com/krateoplatformops/unstructured-runtime/pkg/logging"
	"github.com/krateoplatformops/unstructured-runtime/pkg/meta"
	"github.com/krateoplatformops/unstructured-runtime/pkg/pluralizer"

	"github.com/krateoplatformops/plumbing/e2e"
	xenv "github.com/krateoplatformops/plumbing/env"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	memory "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/e2e-framework/klient/decoder"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"
	"sigs.k8s.io/e2e-framework/pkg/features"
	"sigs.k8s.io/e2e-framework/support/kind"
)

type FakePluralizer struct {
}

var _ pluralizer.PluralizerInterface = &FakePluralizer{}

func (p FakePluralizer) GVKtoGVR(gvk schema.GroupVersionKind) (schema.GroupVersionResource, error) {
	return schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: flect.Pluralize(strings.ToLower(gvk.Kind)),
	}, nil
}

var (
	testenv     env.Environment
	clusterName string
)

const (
	testdataPath      = "../../testdata"
	manifestsPath     = "../../manifests"
	namespace         = "demo-system"
	altNamespace      = "krateo-system"
	chartInspectorUrl = "http://localhost:30007"
)

func TestMain(m *testing.M) {
	xenv.SetTestMode(true)

	clusterName = "kind"
	testenv = env.New()

	testenv.Setup(
		envfuncs.CreateClusterWithConfig(kind.NewProvider(), clusterName, filepath.Join(manifestsPath, "kind.yaml")),
		e2e.CreateNamespace(namespace),
		e2e.CreateNamespace(altNamespace),

		func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			err := decoder.ApplyWithManifestDir(ctx, cfg.Client().Resources(), manifestsPath, "chart-inspector-deployment.yaml", nil)
			if err != nil {
				return ctx, err
			}

			time.Sleep(2 * time.Minute)

			return ctx, nil
		},
	).Finish(
	// envfuncs.DeleteNamespace(namespace),
	// envfuncs.DestroyCluster(clusterName),
	)

	os.Exit(testenv.Run(m))
}

func TestController(t *testing.T) {
	var handler controller.ExternalClient
	// var labelselector labels.Selector
	var c *rest.Config
	f := features.New("Setup").
		Setup(e2e.Logger("test")).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			return ctx
		}).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			c = SetSAToken(ctx, t, cfg)
			r, err := resources.New(cfg.Client().RESTConfig())
			if err != nil {
				t.Error("Creating resource client.", "error", err)
				return ctx
			}

			err = decoder.ApplyWithManifestDir(ctx, r, filepath.Join(testdataPath, "crds", "core"), "*.yaml", nil)
			if err != nil {
				t.Error("Applying crds composition manifests.", "error", err)
				return ctx
			}

			err = decoder.ApplyWithManifestDir(ctx, r, filepath.Join(testdataPath, "crds", "finops"), "*.yaml", nil)
			if err != nil {
				t.Error("Applying crds composition manifests.", "error", err)
				return ctx
			}

			time.Sleep(2 * time.Second)

			err = decoder.ApplyWithManifestDir(ctx, r, filepath.Join(testdataPath, "compositions"), "*.yaml", nil, decoder.MutateNamespace(namespace))
			if err != nil {
				t.Error("Applying composition manifests.", "error", err)
				return ctx
			}

			err = decoder.ApplyWithManifestDir(ctx, r, filepath.Join(testdataPath, "compositiondefinitions"), "*.yaml", nil, decoder.MutateNamespace(namespace))
			if err != nil {
				t.Error("Applying composition definition manifests.", "error", err)
				return ctx
			}

			zl := zap.New(zap.UseDevMode(true))
			log := logging.NewLogrLogger(zl.WithName("composition-controller-test"))

			var pig archive.Getter
			pluralizer := FakePluralizer{}

			pig, err = archive.Dynamic(cfg.Client().RESTConfig(), log, pluralizer)
			if err != nil {
				t.Error("Creating chart url info getter.", "error", err)
				return ctx
			}

			dyn, err := dynamic.NewForConfig(cfg.Client().RESTConfig())
			if err != nil {
				t.Error("Creating dynamic client.", "error", err)
				return ctx
			}

			discovery, err := discovery.NewDiscoveryClientForConfig(cfg.Client().RESTConfig())
			if err != nil {
				t.Error("Creating discovery client.", "error", err)
				return ctx
			}

			cachedDisc := memory.NewMemCacheClient(discovery)

			rec, err := eventrecorder.Create(cfg.Client().RESTConfig())
			if err != nil {
				t.Error("Creating event recorder.", "error", err)
				return ctx
			}

			chartInspector := chartinspector.NewChartInspector(chartInspectorUrl)
			rbacgen := rbacgen.NewRBACGen("test-sa", altNamespace, &chartInspector)
			handler = NewHandler(cfg.Client().RESTConfig(), log, pig, rec, dyn, cachedDisc, pluralizer, rbacgen)

			resli, err := decoder.DecodeAllFiles(ctx, os.DirFS(filepath.Join(testdataPath, "compositiondefinitions")), "*.yaml")
			if err != nil {
				t.Log("Error decoding CRDs: ", err)
				t.Fail()
			}

			for _, res := range resli {
				uns, err := runtime.DefaultUnstructuredConverter.ToUnstructured(res)
				if err != nil {
					t.Log("Error converting CRD: ", err)
					t.Fail()
				}

				apiVersion, ok, err := unstructured.NestedString(uns, "status", "apiVersion")
				if !ok || err != nil {
					t.Log("Error getting apiVersion: ", err)
					t.Fail()
				}
				kind, ok, err := unstructured.NestedString(uns, "status", "kind")
				if !ok || err != nil {
					t.Log("Error getting kind: ", err)
					t.Fail()
				}
				err = r.PatchStatus(ctx, res, k8s.Patch{
					PatchType: types.MergePatchType,
					Data:      []byte(fmt.Sprintf(`{"status": {"apiVersion": "%s", "kind": "%s"}}`, apiVersion, kind)),
				})
				if err != nil {
					t.Log("Error patching Composition: ", err)
					t.Fail()
					return ctx
				}
			}
			return ctx
		}).Assess("Create", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		dynamic := dynamic.NewForConfigOrDie(c)
		var obj unstructured.Unstructured
		err := decoder.DecodeFile(os.DirFS(filepath.Join(testdataPath, "compositions")), "focus.yaml", &obj)
		if err != nil {
			t.Error("Decoding composition manifests.", "error", err)
			return ctx
		}

		version := obj.GetLabels()["krateo.io/composition-version"]
		u, err := dynamic.Resource(schema.GroupVersionResource{
			Group:    "composition.krateo.io",
			Version:  version,
			Resource: flect.Pluralize(strings.ToLower(obj.GetObjectKind().GroupVersionKind().Kind)),
		}).Namespace(obj.GetNamespace()).Get(ctx, obj.GetName(), metav1.GetOptions{})
		if err != nil {
			t.Error("Getting composition.", "error", err)
			return ctx
		}

		observation, err := handler.Observe(ctx, u)
		if err != nil {
			t.Error("Observing composition.", "error", err)
			return ctx
		}

		ctx, err = handleObservation(t, ctx, handler, observation, u)
		if err != nil {
			t.Error("Handling observation.", "error", err)
			return ctx
		}
		return ctx
	}).Assess("Update", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		r, err := resources.New(cfg.Client().RESTConfig())
		if err != nil {
			t.Error("Creating resource client.", "error", err)
			return ctx
		}
		dy := dynamic.NewForConfigOrDie(c)
		var obj unstructured.Unstructured
		err = decoder.DecodeFile(os.DirFS(filepath.Join(testdataPath, "compositions")), "focus.yaml", &obj)
		if err != nil {
			t.Error("Decoding composition manifests.", "error", err)
			return ctx
		}

		version := obj.GetLabels()["krateo.io/composition-version"]
		cli := dy.Resource(schema.GroupVersionResource{
			Group:    "composition.krateo.io",
			Version:  version,
			Resource: flect.Pluralize(strings.ToLower(obj.GetObjectKind().GroupVersionKind().Kind)),
		}).Namespace(obj.GetNamespace())
		u, err := cli.Get(ctx, obj.GetName(), metav1.GetOptions{})
		if err != nil {
			t.Error("Getting composition.", "error", err)
			return ctx
		}

		observation, err := handler.Observe(ctx, u)
		if err != nil {
			t.Error("Observing composition.", "error", err)
			return ctx
		}

		u, err = cli.Get(ctx, obj.GetName(), metav1.GetOptions{})
		if err != nil {
			t.Error("Getting composition.", "error", err)
			return ctx
		}

		ctx, err = handleObservation(t, ctx, handler, observation, u)
		if err != nil {
			t.Error("Handling observation.", "error", err)
			return ctx
		}

		u, err = cli.Get(ctx, obj.GetName(), metav1.GetOptions{})
		if err != nil {
			t.Error("Getting composition.", "error", err)
			return ctx
		}

		observation, err = handler.Observe(ctx, u)
		if err != nil {
			t.Error("Observing composition.", "error", err)
			return ctx
		}

		u, err = cli.Get(ctx, obj.GetName(), metav1.GetOptions{})
		if err != nil {
			t.Error("Getting composition.", "error", err)
			return ctx
		}

		managedResources, _, err := unstructured.NestedSlice(u.Object, "status", "managed")
		if err != nil {
			t.Error("Setting managed resources.", "error", err)
			return ctx
		}

		dyn2 := dynamic.NewForConfigOrDie(cfg.Client().RESTConfig())

		res, err := dyn2.Resource(schema.GroupVersionResource{
			Resource: "datapresentationazures",
			Group:    "finops.krateo.io",
			Version:  "v1alpha1",
		}).Namespace(obj.GetNamespace()).Get(ctx, "focus-1-focus-data-presentation-azure", metav1.GetOptions{})
		if err != nil {
			t.Error("Getting datapresentationazure.", "error", err)
			return ctx
		}

		err = r.PatchStatus(ctx, res, k8s.Patch{
			PatchType: types.MergePatchType,
			Data: []byte(`{
			  "status": {
				"armRegionName": "example-region",
				"armSkuName": "example-sku",
				"currencyCode": "USD",
				"effectiveStartDate": "2025-01-01",
				"isPrimaryMeterRegion": "true",
				"location": "example-location",
				"meterId": "example-meter-id",
				"meterName": "example-meter-name",
				"productId": "example-product-id",
				"productName": "example-product-name",
				"retailPrice": "100.00",
				"serviceFamily": "Networking",
				"serviceId": "example-service-id",
				"serviceName": "example-service-name",
				"skuId": "example-sku-id",
				"skuName": "example-sku-name",
				"tierMinimumUnits": "1",
				"type": "example-type",
				"unitOfMeasure": "example-unit",
				"unitPrice": "10.00"
			  }
			}`),
		})
		if err != nil {
			t.Error("Patching composition.", "error", err)
			return ctx
		}

		u, err = cli.Get(ctx, obj.GetName(), metav1.GetOptions{})
		if err != nil {
			t.Error("Getting composition.", "error", err)
			return ctx
		}

		observation, err = handler.Observe(ctx, u)
		if err != nil {
			t.Error("Observing composition.", "error", err)
			return ctx
		}

		u, err = cli.Get(ctx, obj.GetName(), metav1.GetOptions{})
		if err != nil {
			t.Error("Getting composition.", "error", err)
			return ctx
		}

		ctx, err = handleObservation(t, ctx, handler, observation, u)
		if err != nil {
			t.Error("Handling observation.", "error", err)
			return ctx
		}

		u, err = cli.Get(ctx, obj.GetName(), metav1.GetOptions{})
		if err != nil {
			t.Error("Getting composition.", "error", err)
			return ctx
		}

		tmpRes, _, err := unstructured.NestedSlice(u.Object, "status", "managed")
		if err != nil {
			t.Error("Setting managed resources.", "error", err)
			return ctx
		}

		if len(tmpRes) <= len(managedResources) {
			t.Error("Managed resources not updated.")
		}

		return ctx
	}).Assess("Delete", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		dy := dynamic.NewForConfigOrDie(c)
		var obj unstructured.Unstructured
		err := decoder.DecodeFile(os.DirFS(filepath.Join(testdataPath, "compositions")), "focus.yaml", &obj)
		if err != nil {
			t.Error("Decoding composition manifests.", "error", err)
			return ctx
		}

		version := obj.GetLabels()["krateo.io/composition-version"]
		cli := dy.Resource(schema.GroupVersionResource{
			Group:    "composition.krateo.io",
			Version:  version,
			Resource: flect.Pluralize(strings.ToLower(obj.GetObjectKind().GroupVersionKind().Kind)),
		}).Namespace(obj.GetNamespace())
		u, err := cli.Get(ctx, obj.GetName(), metav1.GetOptions{})
		if err != nil {
			t.Error("Getting composition.", "error", err)
			return ctx
		}

		observation, err := handler.Observe(ctx, u)
		if err != nil {
			t.Error("Observing composition.", "error", err)
			return ctx
		}

		u, err = cli.Get(ctx, obj.GetName(), metav1.GetOptions{})
		if err != nil {
			t.Error("Getting composition.", "error", err)
			return ctx
		}

		ctx, err = handleObservation(t, ctx, handler, observation, u)
		if err != nil {
			t.Error("Handling observation.", "error", err)
			return ctx
		}

		u, err = cli.Get(ctx, obj.GetName(), metav1.GetOptions{})
		if err != nil {
			t.Error("Getting composition.", "error", err)
			return ctx
		}

		u.SetFinalizers([]string{
			"composition.krateo.io/finalizer",
		})

		u, err = cli.Update(ctx, u, metav1.UpdateOptions{})
		if err != nil {
			t.Error("Updating composition.", "error", err)
			return ctx
		}

		err = cli.Delete(ctx, obj.GetName(), metav1.DeleteOptions{})
		if err != nil {
			t.Error("Deleting composition.", "error", err)
			return ctx
		}

		err = handler.Delete(ctx, u)
		if err != nil {
			t.Error("Deleting composition.", "error", err)
			return ctx
		}

		u, err = cli.Get(ctx, obj.GetName(), metav1.GetOptions{})
		if err != nil {
			t.Error("Getting composition.", "error", err)
			return ctx
		}

		u.SetFinalizers([]string{})
		u, err = cli.Update(ctx, u, metav1.UpdateOptions{})
		if err != nil {
			t.Error("Updating composition.", "error", err)
			return ctx
		}

		// Check if the helm release is deleted
		tmp, err := dy.Resource(schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "secrets",
		}).Namespace(obj.GetNamespace()).List(ctx, metav1.ListOptions{
			LabelSelector: "name=" + meta.GetReleaseName(u) + ",owner=helm",
		})
		if tmp != nil && len(tmp.Items) > 0 {
			t.Error("Helm release secret still exists after deletion.")
			return ctx
		}

		_, err = cli.Get(ctx, obj.GetName(), metav1.GetOptions{})
		if err == nil {
			t.Error("Composition still exists after deletion.")
			return ctx
		}
		if !errors.IsNotFound(err) {
			t.Error("Getting composition.", "error", err)
			return ctx
		}

		return ctx
	}).Feature()

	testenv.Test(t, f)
}

func handleObservation(t *testing.T, ctx context.Context, handler controller.ExternalClient, observation controller.ExternalObservation, u *unstructured.Unstructured) (context.Context, error) {
	var err error
	if observation.ResourceExists == true && observation.ResourceUpToDate == true {
		observation, err = handler.Observe(ctx, u)
		if err != nil {
			t.Error("Observing composition.", "error", err)
			return ctx, err
		}
		if observation.ResourceExists == true && observation.ResourceUpToDate == true {
			t.Log("Composition already exists and is ready.")
			return ctx, nil
		}
	} else if observation.ResourceExists == false && observation.ResourceUpToDate == true {
		err = handler.Delete(ctx, u)
		if err != nil {
			t.Error("Deleting composition.", "error", err)
			return ctx, err
		}
	} else if observation.ResourceExists == true && observation.ResourceUpToDate == false {
		err = handler.Update(ctx, u)
		if err != nil {
			t.Error("Updating composition.", "error", err)
			return ctx, err
		}
	} else if observation.ResourceExists == false && observation.ResourceUpToDate == false {
		err = handler.Create(ctx, u)
		if err != nil {
			t.Error("Creating composition.", "error", err)
			return ctx, err
		}
	}
	return ctx, nil
}

func SetSAToken(ctx context.Context, t *testing.T, cfg *envconf.Config) *rest.Config {
	clientset, err := kubernetes.NewForConfig(cfg.Client().RESTConfig())
	if err != nil {
		t.Fatal(err)
	}

	_, err = clientset.CoreV1().ServiceAccounts(namespace).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sa",
			Namespace: namespace,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Create a Role and RoleBinding for the ServiceAccount
	_, err = clientset.RbacV1().Roles(namespace).Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sa-role",
			Namespace: namespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"composition.krateo.io"},
				Resources: []string{"*"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"core.krateo.io"},
				Resources: []string{"compositiondefinitions"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = clientset.RbacV1().RoleBindings(namespace).Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sa-role-binding",
			Namespace: namespace,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "test-sa",
				Namespace: namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			Kind:     "Role",
			Name:     "test-sa-role",
			APIGroup: "rbac.authorization.k8s.io",
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = clientset.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sa-cluster-role",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"rbac.authorization.k8s.io"},
				Resources: []string{"roles", "rolebindings", "clusterroles", "clusterrolebindings"},
				Verbs:     []string{"*"},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = clientset.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-sa-cluster-role-binding",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "test-sa",
				Namespace: namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			Kind:     "ClusterRole",
			Name:     "test-sa-cluster-role",
			APIGroup: "rbac.authorization.k8s.io",
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sa-token",
			Namespace: namespace,
			Annotations: map[string]string{
				"kubernetes.io/service-account.name": "test-sa",
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(2 * time.Second)

	tokenSecret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, "test-sa-token", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}

	saToken := string(tokenSecret.Data["token"])
	cacrt := tokenSecret.Data["ca.crt"]

	if saToken == "" {
		t.Fatal("ServiceAccount token not found")
	}

	// Create a new REST config with the ServiceAccount token
	restConfig := cfg.Client().RESTConfig()
	restConfig.BearerToken = saToken
	restConfig.BearerTokenFile = ""

	config := &rest.Config{
		Host:        restConfig.Host,
		BearerToken: string(saToken),
		TLSClientConfig: rest.TLSClientConfig{
			CertData: cacrt,
			Insecure: true,
		},
	}

	return config
}
