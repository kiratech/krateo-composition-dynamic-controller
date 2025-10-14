package composition

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/krateoplatformops/composition-dynamic-controller/internal/chartinspector"
	compositionMeta "github.com/krateoplatformops/composition-dynamic-controller/internal/meta"

	"github.com/krateoplatformops/composition-dynamic-controller/internal/helmclient"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/helmclient/tracer"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/rbacgen"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/tools/helmchart"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/tools/helmchart/archive"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/tools/rbac"
	"github.com/krateoplatformops/plumbing/env"
	"github.com/krateoplatformops/plumbing/kubeutil/event"
	"github.com/krateoplatformops/plumbing/maps"
	"github.com/krateoplatformops/unstructured-runtime/pkg/controller"
	"github.com/krateoplatformops/unstructured-runtime/pkg/logging"
	"github.com/krateoplatformops/unstructured-runtime/pkg/meta"
	"github.com/krateoplatformops/unstructured-runtime/pkg/pluralizer"
	"github.com/krateoplatformops/unstructured-runtime/pkg/tools"
	"helm.sh/helm/v3/pkg/registry"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

var (
	helmRegistryConfigPath = env.String(helmRegistryConfigPathEnvVar, helmclient.DefaultRegistryConfigPath)
	krateoNamespace        = env.String(krateoNamespaceEnvVar, krateoNamespaceDefault)
	helmRegistryConfigFile = filepath.Join(helmRegistryConfigPath, registry.CredentialsFileBasename)
	helmMaxHistory         = env.Int(helmMaxHistoryEnvvar, 3)
)

const (
	reasonReconciliationGracefullyPaused event.Reason = "ReconciliationGracefullyPaused"

	// Event reasons
	reasonCreated   = "CompositionCreated"
	reasonDeleted   = "CompositionDeleted"
	reasonReady     = "CompositionReady"
	reasonNotReady  = "CompositionNotReady"
	reasonUpdated   = "CompositionUpdated"
	reasonInstalled = "CompositionInstalled"

	// Environment variables
	helmRegistryConfigPathEnvVar = "HELM_REGISTRY_CONFIG_PATH"
	helmMaxHistoryEnvvar         = "HELM_MAX_HISTORY"
	krateoNamespaceEnvVar        = "KRATEO_NAMESPACE"

	// Default namespace for Krateo Installation
	krateoNamespaceDefault = "krateo-system"
)

var _ controller.ExternalClient = (*handler)(nil)

func NewHandler(cfg *rest.Config, log logging.Logger, pig archive.Getter, event event.APIRecorder, pluralizer pluralizer.PluralizerInterface, chartInspectorUrl string, saName string, saNamespace string) controller.ExternalClient {
	val, ok := os.LookupEnv(helmRegistryConfigPathEnvVar)
	if ok {
		helmRegistryConfigPath = val
	}

	helmRegistryConfigFile = filepath.Join(helmRegistryConfigPath, registry.CredentialsFileBasename)

	return &handler{
		kubeconfig:        cfg,
		pluralizer:        pluralizer,
		logger:            log,
		packageInfoGetter: pig,
		eventRecorder:     event,

		chartInspectorUrl: chartInspectorUrl,
		saName:            saName,
		saNamespace:       saNamespace,
	}
}

type handler struct {
	kubeconfig        *rest.Config
	logger            logging.Logger
	pluralizer        pluralizer.PluralizerInterface
	packageInfoGetter archive.Getter
	eventRecorder     event.APIRecorder

	chartInspectorUrl string
	saName            string
	saNamespace       string
}

func (h *handler) Observe(ctx context.Context, mg *unstructured.Unstructured) (controller.ExternalObservation, error) {
	mg = mg.DeepCopy()
	log := h.logger.WithValues("op", "Observe").
		WithValues("apiVersion", mg.GetAPIVersion()).
		WithValues("kind", mg.GetKind()).
		WithValues("name", mg.GetName()).
		WithValues("namespace", mg.GetNamespace())

	dyn, err := dynamic.NewForConfig(h.kubeconfig)
	if err != nil {
		return controller.ExternalObservation{}, fmt.Errorf("creating dynamic client: %w", err)
	}

	updateOpts := tools.UpdateOptions{
		Pluralizer:    h.pluralizer,
		DynamicClient: dyn,
	}

	compositionMeta.SetReleaseName(mg, mg.GetName())
	if _, p := compositionMeta.GetGracefullyPausedTime(mg); p && compositionMeta.IsGracefullyPaused(mg) {
		log.Debug("Composition is gracefully paused, skipping observe.")
		h.eventRecorder.Event(mg, event.Normal(reasonReconciliationGracefullyPaused, "Observe", "Reconciliation is paused via the gracefully paused annotation."))
		return controller.ExternalObservation{
			ResourceExists:   true,
			ResourceUpToDate: true,
		}, nil
	}
	// Immediately remove the gracefully paused time annotation if the composition is not gracefully paused.
	meta.RemoveAnnotations(mg, compositionMeta.AnnotationKeyReconciliationGracefullyPausedTime)
	mg, err = tools.Update(ctx, mg, updateOpts)
	if err != nil {
		return controller.ExternalObservation{}, fmt.Errorf("updating cr with values: %w", err)
	}

	if h.packageInfoGetter == nil {
		return controller.ExternalObservation{}, fmt.Errorf("helm chart package info getter must be specified")
	}
	pkg, err := h.packageInfoGetter.WithLogger(log).Get(mg)
	if err != nil {
		return controller.ExternalObservation{}, fmt.Errorf("getting package info: %w", err)
	}

	compositionMeta.SetCompositionDefinitionLabels(mg, compositionMeta.CompositionDefinitionInfo{
		Name:      pkg.CompositionDefinitionInfo.Name,
		Namespace: pkg.CompositionDefinitionInfo.Namespace,
		GVR:       pkg.CompositionDefinitionInfo.GVR,
	})
	// This sets the labels for the composition definition and release name
	mg, err = tools.Update(ctx, mg, updateOpts)
	if err != nil {
		return controller.ExternalObservation{}, fmt.Errorf("updating cr with values: %w", err)
	}

	hc, _, err := h.helmClientForResource(mg, pkg.RegistryAuth)
	if err != nil {
		return controller.ExternalObservation{}, fmt.Errorf("getting helm client: %w", err)
	}

	rel, err := helmchart.FindRelease(hc, meta.GetReleaseName(mg))
	if err != nil {
		return controller.ExternalObservation{}, fmt.Errorf("finding helm release: %w", err)
	}
	if rel == nil {
		log.Debug("Composition not found.")
		return controller.ExternalObservation{
			ResourceExists:   false,
			ResourceUpToDate: false,
		}, nil
	}

	if rel.Info.Status.IsPending() {
		log.Debug("Composition stuck install or upgrade in progress. Rolling back to previous release before re-attempting.")
		// Rollback to previous release
		err = hc.RollbackRelease(&helmclient.ChartSpec{
			ReleaseName: meta.GetReleaseName(mg),
			Namespace:   mg.GetNamespace(),
			Repo:        pkg.Repo,
			ChartName:   pkg.URL,
			Version:     pkg.Version,
			MaxHistory:  helmMaxHistory,
		})
		if err != nil {
			return controller.ExternalObservation{}, fmt.Errorf("rolling back release: %w", err)
		}
	}

	compositionGVR, err := h.pluralizer.GVKtoGVR(mg.GroupVersionKind())
	if err != nil {
		return controller.ExternalObservation{}, fmt.Errorf("converting GVK to GVR: %w", err)
	}

	chartInspector := chartinspector.NewChartInspector(h.chartInspectorUrl)
	rbgen := rbacgen.NewRBACGen(h.saName, h.saNamespace, &chartInspector)
	// Get Resources and generate RBAC
	generated, err := rbgen.
		WithBaseName(meta.GetReleaseName(mg)).
		Generate(rbacgen.Parameters{
			CompositionName:                mg.GetName(),
			CompositionNamespace:           mg.GetNamespace(),
			CompositionGVR:                 compositionGVR,
			CompositionDefinitionName:      pkg.CompositionDefinitionInfo.Name,
			CompositionDefinitionNamespace: pkg.CompositionDefinitionInfo.Namespace,
			CompositionDefintionGVR:        pkg.CompositionDefinitionInfo.GVR,
		})
	if err != nil {
		return controller.ExternalObservation{}, fmt.Errorf("generating RBAC using chart-inspector: %w", err)
	}
	rbInstaller := rbac.NewRBACInstaller(dyn)
	err = rbInstaller.ApplyRBAC(generated)
	if err != nil {
		return controller.ExternalObservation{}, fmt.Errorf("installing rbac: %w", err)
	}

	tracer := &tracer.Tracer{}
	hc, clientset, err := h.helmClientForResourceWithTransportWrapper(mg, pkg.RegistryAuth, func(rt http.RoundTripper) http.RoundTripper {
		return tracer.WithRoundTripper(rt)
	})
	if err != nil {
		return controller.ExternalObservation{}, fmt.Errorf("getting helm client: %w", err)
	}

	opts := helmchart.UpdateOptions{
		CheckResourceOptions: helmchart.CheckResourceOptions{
			DynamicClient: dyn,
			Pluralizer:    h.pluralizer,
		},
		HelmClient:      hc,
		ChartName:       pkg.URL,
		Resource:        mg,
		Repo:            pkg.Repo,
		Version:         pkg.Version,
		KrateoNamespace: krateoNamespace,
		MaxHistory:      helmMaxHistory,
	}
	if pkg.RegistryAuth != nil {
		opts.Credentials = &helmchart.Credentials{
			Username: pkg.RegistryAuth.Username,
			Password: pkg.RegistryAuth.Password,
		}
	}

	upgradedRel, err := helmchart.Update(ctx, opts)
	if err != nil {
		return controller.ExternalObservation{}, err
	}

	_, digest, err := helmchart.GetResourcesRefFromRelease(upgradedRel, mg.GetNamespace(), clientset)
	if err != nil {
		return controller.ExternalObservation{}, fmt.Errorf("getting resources from release: %w", err)
	}
	previousDigest, err := maps.NestedString(mg.Object, "status", "digest")
	if err != nil {
		return controller.ExternalObservation{}, fmt.Errorf("getting previous digest from status: %w", err)
	}
	if previousDigest == "" {
		// Calculate the digest from the previous release if not present in status
		log.Debug("Previous digest not found in status, calculating from previous release")
		_, previousDigest, err = helmchart.GetResourcesRefFromRelease(rel, mg.GetNamespace(), clientset)
		if err != nil {
			return controller.ExternalObservation{}, fmt.Errorf("getting resources from previous release: %w", err)
		}
	}
	if digest != previousDigest {
		log.Debug("Composition out-of-date.", "package", pkg.URL, "current", digest, "expected", previousDigest)
		return controller.ExternalObservation{
			ResourceExists:   true,
			ResourceUpToDate: false,
		}, nil
	}

	if rel.Chart.Metadata.Version != upgradedRel.Chart.Metadata.Version {
		log.Debug("Composition package version mismatch.", "package", pkg.URL, "installed", rel.Chart.Metadata.Version, "expected", pkg.Version)
		return controller.ExternalObservation{
			ResourceExists:   true,
			ResourceUpToDate: false,
		}, nil
	}

	err = setStatus(mg, &statusManagerOpts{
		pluralizer:     h.pluralizer,
		force:          false,
		resources:      nil, // we don't need to set resources here as they are already set when a resource is created/updated
		previousDigest: previousDigest,
		digest:         digest,
		message:        "Composition is up-to-date",
		chartURL:       pkg.URL,
		chartVersion:   pkg.Version,
		conditionType:  ConditionTypeAvailable,
	})
	if err != nil {
		return controller.ExternalObservation{}, err
	}

	_, err = tools.UpdateStatus(ctx, mg, updateOpts)
	if err != nil {
		return controller.ExternalObservation{}, err
	}

	log.Debug("Composition Observed - installed", "package", pkg.URL)

	return controller.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: true,
	}, nil
}

func (h *handler) Create(ctx context.Context, mg *unstructured.Unstructured) error {
	mg = mg.DeepCopy()
	log := h.logger.WithValues("op", "Create").
		WithValues("apiVersion", mg.GetAPIVersion()).
		WithValues("kind", mg.GetKind()).
		WithValues("name", mg.GetName()).
		WithValues("namespace", mg.GetNamespace())

	dyn, err := dynamic.NewForConfig(h.kubeconfig)
	if err != nil {
		return fmt.Errorf("creating dynamic client: %w", err)
	}

	updateOpts := tools.UpdateOptions{
		Pluralizer:    h.pluralizer,
		DynamicClient: dyn,
	}

	if _, p := compositionMeta.GetGracefullyPausedTime(mg); p && compositionMeta.IsGracefullyPaused(mg) {
		log.Debug("Composition is gracefully paused, skipping create.")
		h.eventRecorder.Event(mg, event.Normal(reasonReconciliationGracefullyPaused, "Update", "Reconciliation is paused via the gracefully paused annotation."))
		return nil
	}

	compositionMeta.SetReleaseName(mg, mg.GetName())
	mg, err = tools.Update(ctx, mg, updateOpts)
	if err != nil {
		return fmt.Errorf("updating cr with values: %w", err)
	}

	if h.packageInfoGetter == nil {
		return fmt.Errorf("helm chart package info getter must be specified")
	}

	pkg, err := h.packageInfoGetter.WithLogger(log).Get(mg)
	if err != nil {
		return fmt.Errorf("getting package info: %w", err)
	}
	compositionGVR, err := h.pluralizer.GVKtoGVR(mg.GroupVersionKind())
	if err != nil {
		return fmt.Errorf("converting GVK to GVR: %w", err)
	}

	chartInspector := chartinspector.NewChartInspector(h.chartInspectorUrl)
	rbgen := rbacgen.NewRBACGen(h.saName, h.saNamespace, &chartInspector)
	// Get Resources and generate RBAC
	generated, err := rbgen.
		WithBaseName(meta.GetReleaseName(mg)).
		Generate(rbacgen.Parameters{
			CompositionName:                mg.GetName(),
			CompositionNamespace:           mg.GetNamespace(),
			CompositionGVR:                 compositionGVR,
			CompositionDefinitionName:      pkg.CompositionDefinitionInfo.Name,
			CompositionDefinitionNamespace: pkg.CompositionDefinitionInfo.Namespace,
			CompositionDefintionGVR:        pkg.CompositionDefinitionInfo.GVR,
		})
	if err != nil {
		return fmt.Errorf("generating RBAC using chart-inspector: %w", err)
	}
	rbInstaller := rbac.NewRBACInstaller(dyn)
	err = rbInstaller.ApplyRBAC(generated)
	if err != nil {
		return fmt.Errorf("installing rbac: %w", err)
	}

	// Install the helm chart
	hc, clientset, err := h.helmClientForResource(mg, pkg.RegistryAuth)
	if err != nil {
		return fmt.Errorf("getting helm client: %w", err)
	}

	opts := helmchart.InstallOptions{
		CheckResourceOptions: helmchart.CheckResourceOptions{
			DynamicClient: dyn,
			Pluralizer:    h.pluralizer,
		},
		HelmClient:      hc,
		ChartName:       pkg.URL,
		Resource:        mg,
		Repo:            pkg.Repo,
		Version:         pkg.Version,
		KrateoNamespace: krateoNamespace,
		MaxHistory:      helmMaxHistory,
	}
	if pkg.RegistryAuth != nil {
		opts.Credentials = &helmchart.Credentials{
			Username: pkg.RegistryAuth.Username,
			Password: pkg.RegistryAuth.Password,
		}
	}

	rel, _, err := helmchart.Install(ctx, opts)
	if err != nil {
		return fmt.Errorf("installing helm chart: %w", err)
	}
	log.Debug("Installing composition package", "package", pkg.URL)

	all, digest, err := helmchart.GetResourcesRefFromRelease(rel, mg.GetNamespace(), clientset)
	if err != nil {
		return fmt.Errorf("getting resources from release: %w", err)
	}

	err = setStatus(mg, &statusManagerOpts{
		pluralizer:     h.pluralizer,
		force:          true,
		resources:      all,
		previousDigest: "",
		digest:         digest,
		message:        "Composition created",
		chartURL:       pkg.URL,
		chartVersion:   pkg.Version,
		conditionType:  ConditionTypeAvailable,
	})
	if err != nil {
		return fmt.Errorf("setting status: %w", err)
	}

	log.Debug("Composition created.", "package", pkg.URL)

	h.eventRecorder.Event(mg, event.Normal(reasonCreated, "Create", fmt.Sprintf("Composition created: %s", mg.GetName())))
	mg, err = tools.UpdateStatus(ctx, mg, updateOpts)
	if err != nil {
		return fmt.Errorf("updating cr with values: %w", err)
	}

	meta.RemoveAnnotations(mg, compositionMeta.AnnotationKeyReconciliationGracefullyPausedTime)
	_, err = tools.Update(ctx, mg, updateOpts)
	if err != nil {
		return fmt.Errorf("updating cr with values: %w", err)
	}

	return nil
}

func (h *handler) Update(ctx context.Context, mg *unstructured.Unstructured) error {
	mg = mg.DeepCopy()

	log := h.logger.WithValues("op", "Update").
		WithValues("apiVersion", mg.GetAPIVersion()).
		WithValues("kind", mg.GetKind()).
		WithValues("name", mg.GetName()).
		WithValues("namespace", mg.GetNamespace())

	dyn, err := dynamic.NewForConfig(h.kubeconfig)
	if err != nil {
		return fmt.Errorf("creating dynamic client: %w", err)
	}

	updateOpts := tools.UpdateOptions{
		Pluralizer:    h.pluralizer,
		DynamicClient: dyn,
	}

	if _, p := compositionMeta.GetGracefullyPausedTime(mg); p && compositionMeta.IsGracefullyPaused(mg) {
		log.Debug("Composition is gracefully paused, skipping update.")
		h.eventRecorder.Event(mg, event.Normal(reasonReconciliationGracefullyPaused, "Update", "Reconciliation is paused via the gracefully paused annotation."))
		return nil
	}

	log.Debug("Handling composition values update.")

	if h.packageInfoGetter == nil {
		return fmt.Errorf("helm chart package info getter must be specified")
	}

	pkg, err := h.packageInfoGetter.WithLogger(log).Get(mg)
	if err != nil {
		return fmt.Errorf("getting package info: %w", err)
	}

	// Update the helm chart
	hc, clientset, err := h.helmClientForResource(mg, pkg.RegistryAuth)
	if err != nil {
		return fmt.Errorf("getting helm client: %w", err)
	}

	rel, err := helmchart.FindRelease(hc, meta.GetReleaseName(mg))
	if err != nil {
		return fmt.Errorf("finding helm release: %w", err)
	}

	previousDigest, err := maps.NestedString(mg.Object, "status", "digest")
	if err != nil {
		return fmt.Errorf("getting previous digest from status: %w", err)
	}

	all, digest, err := helmchart.GetResourcesRefFromRelease(rel, mg.GetNamespace(), clientset)
	if err != nil {
		return fmt.Errorf("getting resources from release: %w", err)
	}

	managed, err := populateManagedResources(h.pluralizer, all)
	if err != nil {
		return fmt.Errorf("populating managed resources: %w", err)
	}
	setManagedResources(mg, managed)

	log.Debug("Composition values updated.", "package", pkg.URL)

	h.eventRecorder.Event(mg, event.Normal(reasonUpdated, "Update", fmt.Sprintf("Updated composition: %s", mg.GetName())))

	statusOpts := &statusManagerOpts{
		pluralizer:     h.pluralizer,
		force:          false,
		resources:      all,
		digest:         digest,
		previousDigest: previousDigest,
		message:        "Composition values updated",
		chartURL:       pkg.URL,
		chartVersion:   pkg.Version,
	}

	if compositionMeta.IsGracefullyPaused(mg) {
		statusOpts.conditionType = ConditionTypeReconcileGracefullyPaused
		compositionMeta.SetGracefullyPausedTime(mg, time.Now())
		log.Debug("Composition gracefully paused.")
		h.eventRecorder.Event(mg, event.Normal(reasonReconciliationGracefullyPaused, "Update", "Reconciliation paused via the gracefully paused annotation."))
	} else {
		statusOpts.conditionType = ConditionTypeAvailable
		meta.RemoveAnnotations(mg, compositionMeta.AnnotationKeyReconciliationGracefullyPausedTime)
	}

	err = setStatus(mg, statusOpts)
	if err != nil {
		return fmt.Errorf("setting status: %w", err)
	}

	mg, err = tools.UpdateStatus(ctx, mg, tools.UpdateOptions{
		Pluralizer:    h.pluralizer,
		DynamicClient: dyn,
	})
	if err != nil {
		return fmt.Errorf("updating cr status with values: %w", err)
	}
	_, err = tools.Update(ctx, mg, updateOpts)
	if err != nil {
		return fmt.Errorf("updating cr with values: %w", err)
	}
	return nil
}

func (h *handler) Delete(ctx context.Context, mg *unstructured.Unstructured) error {
	mg = mg.DeepCopy()

	log := h.logger.WithValues("op", "Delete").
		WithValues("apiVersion", mg.GetAPIVersion()).
		WithValues("kind", mg.GetKind()).
		WithValues("name", mg.GetName()).
		WithValues("namespace", mg.GetNamespace())

	dyn, err := dynamic.NewForConfig(h.kubeconfig)
	if err != nil {
		return fmt.Errorf("creating dynamic client: %w", err)
	}

	updateOpts := tools.UpdateOptions{
		Pluralizer:    h.pluralizer,
		DynamicClient: dyn,
	}

	if _, p := compositionMeta.GetGracefullyPausedTime(mg); p && compositionMeta.IsGracefullyPaused(mg) {
		log.Debug("Composition is gracefully paused, skipping delete.")
		h.eventRecorder.Event(mg, event.Normal(reasonReconciliationGracefullyPaused, "Delete", "Reconciliation is paused via the gracefully paused annotation."))
		return nil
	}

	if h.packageInfoGetter == nil {
		return fmt.Errorf("helm chart package info getter must be specified")
	}

	hc, _, err := h.helmClientForResource(mg, nil)
	if err != nil {
		return fmt.Errorf("getting helm client: %w", err)
	}

	pkg, err := h.packageInfoGetter.WithLogger(log).Get(mg)
	if err != nil {
		return fmt.Errorf("getting package info: %w", err)
	}

	chartSpec := helmclient.ChartSpec{
		ReleaseName: meta.GetReleaseName(mg),
		Namespace:   mg.GetNamespace(),
		ChartName:   pkg.URL,
		Version:     pkg.Version,
		Wait:        false,
	}

	// Check if the release exists before uninstalling
	rel, err := helmchart.FindRelease(hc, meta.GetReleaseName(mg))
	if err != nil {
		return fmt.Errorf("finding helm release: %w", err)
	}
	if rel == nil {
		log.Debug("Composition not found, nothing to uninstall.", "package", pkg.URL)
		h.eventRecorder.Event(mg, event.Normal(reasonDeleted, "Delete", fmt.Sprintf("Composition not found, nothing to uninstall: %s", mg.GetName())))
		return nil
	}

	err = hc.UninstallRelease(&chartSpec)
	if err != nil {
		return fmt.Errorf("uninstalling helm chart: %w", err)
	}

	rel, err = helmchart.FindRelease(hc, meta.GetReleaseName(mg))
	if err != nil {
		return fmt.Errorf("finding helm release: %w", err)
	}
	if rel != nil {
		return fmt.Errorf("composition not deleted, release %s still exists", meta.GetReleaseName(mg))
	}

	log.Debug("Uninstalling RBAC", "package", pkg.URL)

	compositionGVR, err := h.pluralizer.GVKtoGVR(mg.GroupVersionKind())
	if err != nil {
		return fmt.Errorf("converting GVK to GVR: %w", err)
	}
	chartInspector := chartinspector.NewChartInspector(h.chartInspectorUrl)
	rbgen := rbacgen.NewRBACGen(h.saName, h.saNamespace, &chartInspector)
	// Get Resources and generate RBAC
	generated, err := rbgen.
		WithBaseName(meta.GetReleaseName(mg)).
		Generate(rbacgen.Parameters{
			CompositionName:                mg.GetName(),
			CompositionNamespace:           mg.GetNamespace(),
			CompositionGVR:                 compositionGVR,
			CompositionDefinitionName:      pkg.CompositionDefinitionInfo.Name,
			CompositionDefinitionNamespace: pkg.CompositionDefinitionInfo.Namespace,
			CompositionDefintionGVR:        pkg.CompositionDefinitionInfo.GVR,
		})
	if err != nil {
		return fmt.Errorf("generating RBAC using chart-inspector: %w", err)
	}
	rbInstaller := rbac.NewRBACInstaller(dyn)
	err = rbInstaller.UninstallRBAC(generated)
	if err != nil {
		return fmt.Errorf("uninstalling rbac: %w", err)
	}

	h.eventRecorder.Event(mg, event.Normal(reasonDeleted, "Delete", fmt.Sprintf("Deleted composition: %s", mg.GetName())))
	log.Debug("Composition package removed.", "package", pkg.URL)
	meta.RemoveAnnotations(mg, compositionMeta.AnnotationKeyReconciliationGracefullyPausedTime)

	_, err = tools.Update(ctx, mg, updateOpts)
	if err != nil {
		return fmt.Errorf("updating cr with values: %w", err)
	}

	return nil
}

func (h *handler) helmClientForResource(mg *unstructured.Unstructured, registryAuth *helmclient.RegistryAuth) (helmclient.Client, helmclient.CachedClientsInterface, error) {
	log := h.logger.WithValues("apiVersion", mg.GetAPIVersion()).
		WithValues("kind", mg.GetKind()).
		WithValues("name", mg.GetName()).
		WithValues("namespace", mg.GetNamespace())

	clientSet, err := helmclient.NewCachedClients(h.kubeconfig)
	if err != nil {
		return nil, nil, fmt.Errorf("creating cached helm client set: %w", err)
	}

	opts := &helmclient.Options{
		Namespace:        mg.GetNamespace(),
		RepositoryCache:  "/tmp/.helmcache",
		RepositoryConfig: "/tmp/.helmrepo",
		RegistryConfig:   helmRegistryConfigFile,
		Debug:            true,
		Linting:          false,
		DebugLog: func(format string, v ...interface{}) {
			if !meta.IsVerbose(mg) {
				return
			}

			if len(v) > 0 {
				log.Info(fmt.Sprintf(format, v))
			} else {
				log.Info(format)
			}
		},
		RegistryAuth: (registryAuth),
	}

	hc, err := helmclient.NewCachedClientFromRestConf(
		&helmclient.RestConfClientOptions{
			Options:    opts,
			RestConfig: h.kubeconfig,
		},
		clientSet,
	)
	return hc, clientSet, err
}

func (h *handler) helmClientForResourceWithTransportWrapper(mg *unstructured.Unstructured, registryAuth *helmclient.RegistryAuth, transportWrapper func(http.RoundTripper) http.RoundTripper) (helmclient.Client, helmclient.CachedClientsInterface, error) {
	opts := &helmclient.Options{
		Namespace:        mg.GetNamespace(),
		RepositoryCache:  "/tmp/.helmcache",
		RepositoryConfig: "/tmp/.helmrepo",
		RegistryConfig:   helmRegistryConfigFile,
		Debug:            true,
		Linting:          false,
		DebugLog:         func(format string, v ...interface{}) {},
		RegistryAuth:     registryAuth,
	}

	clientSet, err := helmclient.NewCachedClients(h.kubeconfig)
	if err != nil {
		return nil, nil, err
	}

	cfg := rest.CopyConfig(h.kubeconfig)
	cfg.WrapTransport = transportWrapper

	hc, err := helmclient.NewCachedClientFromRestConf(
		&helmclient.RestConfClientOptions{
			Options:    opts,
			RestConfig: cfg,
		},
		clientSet,
	)
	return hc, clientSet, err
}
