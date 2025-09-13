package composition

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	compositionMeta "github.com/krateoplatformops/composition-dynamic-controller/internal/meta"

	"github.com/krateoplatformops/composition-dynamic-controller/internal/helmclient"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/helmclient/tracer"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/rbacgen"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/tools/helmchart"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/tools/helmchart/archive"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/tools/rbac"
	"github.com/krateoplatformops/plumbing/env"
	"github.com/krateoplatformops/plumbing/kubeutil/event"
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
	helmMaxHistory         = env.Int(helmMaxHistoryEnvvar, 10)
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

func NewHandler(cfg *rest.Config, log logging.Logger, pig archive.Getter, event event.APIRecorder, pluralizer pluralizer.PluralizerInterface, rbacgen rbacgen.RBACGenInterface) controller.ExternalClient {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		log.Error(err, "Creating dynamic client.")
		return nil
	}

	val, ok := os.LookupEnv(helmRegistryConfigPathEnvVar)
	if ok {
		helmRegistryConfigPath = val
	}

	helmRegistryConfigFile = filepath.Join(helmRegistryConfigPath, registry.CredentialsFileBasename)

	return &handler{
		kubeconfig:        cfg,
		rbacgen:           rbacgen,
		pluralizer:        pluralizer,
		logger:            log,
		dynamicClient:     dyn,
		packageInfoGetter: pig,
		eventRecorder:     event,
	}
}

type handler struct {
	kubeconfig        *rest.Config
	rbacgen           rbacgen.RBACGenInterface
	logger            logging.Logger
	pluralizer        pluralizer.PluralizerInterface
	dynamicClient     dynamic.Interface
	packageInfoGetter archive.Getter
	eventRecorder     event.APIRecorder
}

func (h *handler) Observe(ctx context.Context, mg *unstructured.Unstructured) (controller.ExternalObservation, error) {
	log := h.logger.WithValues("op", "Observe").
		WithValues("apiVersion", mg.GetAPIVersion()).
		WithValues("kind", mg.GetKind()).
		WithValues("name", mg.GetName()).
		WithValues("namespace", mg.GetNamespace())

	updateOpts := tools.UpdateOptions{
		Pluralizer:    h.pluralizer,
		DynamicClient: h.dynamicClient,
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
	mg, err := tools.Update(ctx, mg, updateOpts)
	if err != nil {
		log.Error(err, "Updating cr with values")
		return controller.ExternalObservation{}, fmt.Errorf("updating cr with values: %w", err)
	}

	if h.packageInfoGetter == nil {
		return controller.ExternalObservation{}, fmt.Errorf("helm chart package info getter must be specified")
	}
	pkg, err := h.packageInfoGetter.WithLogger(log).Get(mg)
	if err != nil {
		log.Error(err, "Getting package info")
		return controller.ExternalObservation{}, err
	}

	compositionMeta.SetCompositionDefinitionLabels(mg, compositionMeta.CompositionDefinitionInfo{
		Name:      pkg.CompositionDefinitionInfo.Name,
		Namespace: pkg.CompositionDefinitionInfo.Namespace,
		GVR:       pkg.CompositionDefinitionInfo.GVR,
	})
	// This sets the labels for the composition definition and release name
	mg, err = tools.Update(ctx, mg, updateOpts)
	if err != nil {
		log.Error(err, "Updating cr with values")
		return controller.ExternalObservation{}, fmt.Errorf("updating cr with values: %w", err)
	}

	hc, err := h.helmClientForResource(mg, pkg.RegistryAuth)
	if err != nil {
		log.Error(err, "Getting helm client")
		return controller.ExternalObservation{}, err
	}

	rel, err := helmchart.FindRelease(hc, meta.GetReleaseName(mg))
	if err != nil {
		log.Error(err, "Finding helm release")
		return controller.ExternalObservation{}, fmt.Errorf("finding helm release: %w", err)
	}
	if rel == nil {
		log.Debug("Composition not found.")
		return controller.ExternalObservation{
			ResourceExists:   false,
			ResourceUpToDate: false,
		}, nil
	}

	compositionGVR, err := h.pluralizer.GVKtoGVR(mg.GroupVersionKind())
	if err != nil {
		log.Error(err, "Converting GVK to GVR")
		return controller.ExternalObservation{}, fmt.Errorf("converting GVK to GVR: %w", err)
	}
	// Get Resources and generate RBAC
	generated, err := h.rbacgen.
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
		log.Error(err, "Generating RBAC using chart-inspector")
		return controller.ExternalObservation{}, err
	}
	rbInstaller := rbac.NewRBACInstaller(h.dynamicClient)
	err = rbInstaller.ApplyRBAC(generated)
	if err != nil {
		log.Error(err, "Installing RBAC")
		return controller.ExternalObservation{}, err
	}

	tracer := &tracer.Tracer{}
	hc, err = h.helmClientForResourceWithTransportWrapper(mg, pkg.RegistryAuth, func(rt http.RoundTripper) http.RoundTripper {
		return tracer.WithRoundTripper(rt)
	})
	if err != nil {
		log.Error(err, "Getting helm client")
		return controller.ExternalObservation{}, err
	}

	opts := helmchart.UpdateOptions{
		CheckResourceOptions: helmchart.CheckResourceOptions{
			DynamicClient: h.dynamicClient,
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
		log.Error(err, "Performing helm chart update")
		return controller.ExternalObservation{}, err
	}

	modifiedResources := tracer.GetResources()
	if len(modifiedResources) > 0 {
		for _, resource := range modifiedResources {
			if meta.IsVerbose(mg) {
				log.Debug("Composition resource modified", "Name", resource.Name, "Namespace", resource.Namespace, "Group", resource.Group, "Version", resource.Version, "Resource", resource.Resource)
			}
		}
		log.Debug("Composition resources modified", "count", len(modifiedResources))
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

	err = setAvaibleStatus(mg, pkg, "Composition up-to-date", false)
	if err != nil {
		log.Error(err, "Setting available status")
		return controller.ExternalObservation{}, err
	}

	mg, err = tools.UpdateStatus(ctx, mg, updateOpts)
	if err != nil {
		log.Error(err, "Updating cr status with values")
		return controller.ExternalObservation{}, err
	}

	log.Debug("Composition Observed - installed", "package", pkg.URL)

	return controller.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: true,
	}, nil
}

func (h *handler) Create(ctx context.Context, mg *unstructured.Unstructured) error {
	log := h.logger.WithValues("op", "Create").
		WithValues("apiVersion", mg.GetAPIVersion()).
		WithValues("kind", mg.GetKind()).
		WithValues("name", mg.GetName()).
		WithValues("namespace", mg.GetNamespace())

	updateOpts := tools.UpdateOptions{
		Pluralizer:    h.pluralizer,
		DynamicClient: h.dynamicClient,
	}

	if _, p := compositionMeta.GetGracefullyPausedTime(mg); p && compositionMeta.IsGracefullyPaused(mg) {
		log.Debug("Composition is gracefully paused, skipping create.")
		h.eventRecorder.Event(mg, event.Normal(reasonReconciliationGracefullyPaused, "Update", "Reconciliation is paused via the gracefully paused annotation."))
		return nil
	}

	compositionMeta.SetReleaseName(mg, mg.GetName())
	mg, err := tools.Update(ctx, mg, updateOpts)
	if err != nil {
		log.Error(err, "Updating cr with values")
		return fmt.Errorf("updating cr with values: %w", err)
	}

	if h.packageInfoGetter == nil {
		log.Error(err, "helm chart package info getter must be specified")
		return fmt.Errorf("helm chart package info getter must be specified")
	}

	pkg, err := h.packageInfoGetter.WithLogger(log).Get(mg)
	if err != nil {
		log.Error(err, "Getting package info")
		return fmt.Errorf("getting package info: %w", err)
	}
	compositionGVR, err := h.pluralizer.GVKtoGVR(mg.GroupVersionKind())
	if err != nil {
		log.Error(err, "Converting GVK to GVR")
		return fmt.Errorf("converting GVK to GVR: %w", err)
	}
	// Get Resources and generate RBAC
	generated, err := h.rbacgen.
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
		log.Error(err, "Generating RBAC using chart-inspector")
		return err
	}
	rbInstaller := rbac.NewRBACInstaller(h.dynamicClient)
	err = rbInstaller.ApplyRBAC(generated)
	if err != nil {
		log.Error(err, "Installing RBAC")
		return err
	}

	// Install the helm chart
	hc, err := h.helmClientForResource(mg, pkg.RegistryAuth)
	if err != nil {
		log.Error(err, "Getting helm client")
		return err
	}

	opts := helmchart.InstallOptions{
		CheckResourceOptions: helmchart.CheckResourceOptions{
			DynamicClient: h.dynamicClient,
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
		log.Error(err, "Installing helm chart", "package", pkg.URL)
		return fmt.Errorf("installing helm chart: %w", err)
	}
	log.Debug("Installing composition package", "package", pkg.URL)

	all, err := helmchart.GetResourcesRefFromRelease(rel, mg.GetNamespace())
	if err != nil {
		log.Error(err, "Getting resources from release")
		return fmt.Errorf("getting resources from release: %w", err)
	}

	managed, err := populateManagedResources(h.pluralizer, all)
	if err != nil {
		log.Error(err, "Populating managed resources")
		return err
	}
	setManagedResources(mg, managed)
	log.Debug("Composition created.", "package", pkg.URL)

	h.eventRecorder.Event(mg, event.Normal(reasonCreated, "Create", fmt.Sprintf("Composition created: %s", mg.GetName())))
	err = setAvaibleStatus(mg, pkg, "Composition created", true)
	if err != nil {
		log.Error(err, "Setting available status")
		return err
	}
	mg, err = tools.UpdateStatus(ctx, mg, updateOpts)
	if err != nil {
		log.Error(err, "Updating cr status with values")
		return fmt.Errorf("updating cr with values: %w", err)
	}

	meta.RemoveAnnotations(mg, compositionMeta.AnnotationKeyReconciliationGracefullyPausedTime)
	_, err = tools.Update(ctx, mg, updateOpts)
	if err != nil {
		log.Error(err, "Updating cr with values")
		return fmt.Errorf("updating cr with values: %w", err)
	}

	return nil
}

func (h *handler) Update(ctx context.Context, mg *unstructured.Unstructured) error {
	log := h.logger.WithValues("op", "Update").
		WithValues("apiVersion", mg.GetAPIVersion()).
		WithValues("kind", mg.GetKind()).
		WithValues("name", mg.GetName()).
		WithValues("namespace", mg.GetNamespace())

	updateOpts := tools.UpdateOptions{
		Pluralizer:    h.pluralizer,
		DynamicClient: h.dynamicClient,
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
		log.Error(err, "Getting package info")
		return err
	}

	// Update the helm chart
	hc, err := h.helmClientForResource(mg, pkg.RegistryAuth)
	if err != nil {
		log.Error(err, "Getting helm client")
		return err
	}

	rel, err := helmchart.FindRelease(hc, meta.GetReleaseName(mg))
	if err != nil {
		return err
	}

	all, err := helmchart.GetResourcesRefFromRelease(rel, mg.GetNamespace())
	if err != nil {
		log.Error(err, "Getting resources from release")
		return fmt.Errorf("getting resources from release: %w", err)
	}

	managed, err := populateManagedResources(h.pluralizer, all)
	if err != nil {
		log.Error(err, "Populating managed resources")
		return err
	}
	setManagedResources(mg, managed)

	log.Debug("Composition values updated.", "package", pkg.URL)

	h.eventRecorder.Event(mg, event.Normal(reasonUpdated, "Update", fmt.Sprintf("Updated composition: %s", mg.GetName())))

	if compositionMeta.IsGracefullyPaused(mg) {
		err = setGracefullyPausedCondition(mg, pkg)
		if err != nil {
			log.Error(err, "Setting gracefully paused condition")
			return err
		}
		mg, err = tools.UpdateStatus(ctx, mg, tools.UpdateOptions{
			Pluralizer:    h.pluralizer,
			DynamicClient: h.dynamicClient,
		})
		if err != nil {
			log.Error(err, "Updating cr status with values")
			return err
		}

		compositionMeta.SetGracefullyPausedTime(mg, time.Now())
		log.Debug("Composition gracefully paused.")
		h.eventRecorder.Event(mg, event.Normal(reasonReconciliationGracefullyPaused, "Update", "Reconciliation paused via the gracefully paused annotation."))
	} else {
		// Normal behavior, set available status
		err = setAvaibleStatus(mg, pkg, "Composition values updated.", true)
		if err != nil {
			log.Error(err, "Setting available status")
			return err
		}
		mg, err = tools.UpdateStatus(ctx, mg, tools.UpdateOptions{
			Pluralizer:    h.pluralizer,
			DynamicClient: h.dynamicClient,
		})
		if err != nil {
			log.Error(err, "Updating cr status with values")
			return err
		}

		meta.RemoveAnnotations(mg, compositionMeta.AnnotationKeyReconciliationGracefullyPausedTime)
	}

	_, err = tools.Update(ctx, mg, updateOpts)
	if err != nil {
		log.Error(err, "Updating cr with values")
		return fmt.Errorf("updating cr with values: %w", err)
	}
	return nil
}

func (h *handler) Delete(ctx context.Context, mg *unstructured.Unstructured) error {
	log := h.logger.WithValues("op", "Delete").
		WithValues("apiVersion", mg.GetAPIVersion()).
		WithValues("kind", mg.GetKind()).
		WithValues("name", mg.GetName()).
		WithValues("namespace", mg.GetNamespace())

	updateOpts := tools.UpdateOptions{
		Pluralizer:    h.pluralizer,
		DynamicClient: h.dynamicClient,
	}

	if _, p := compositionMeta.GetGracefullyPausedTime(mg); p && compositionMeta.IsGracefullyPaused(mg) {
		log.Debug("Composition is gracefully paused, skipping delete.")
		h.eventRecorder.Event(mg, event.Normal(reasonReconciliationGracefullyPaused, "Delete", "Reconciliation is paused via the gracefully paused annotation."))
		return nil
	}

	if h.packageInfoGetter == nil {
		return fmt.Errorf("helm chart package info getter must be specified")
	}

	hc, err := h.helmClientForResource(mg, nil)
	if err != nil {
		log.Error(err, "Getting helm client")
		return err
	}

	pkg, err := h.packageInfoGetter.WithLogger(log).Get(mg)
	if err != nil {
		log.Error(err, "Getting package info")
		return err
	}

	chartSpec := helmclient.ChartSpec{
		ReleaseName: meta.GetReleaseName(mg),
		Namespace:   mg.GetNamespace(),
		ChartName:   pkg.URL,
		Version:     pkg.Version,
		Wait:        false,
	}

	// Check if the release exists before uninstalling
	rel, err := helmchart.FindAnyRelease(hc, meta.GetReleaseName(mg))
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
		log.Error(err, "Uninstalling helm chart", "package", pkg.URL)
		return fmt.Errorf("uninstalling helm chart: %w", err)
	}

	rel, err = helmchart.FindAnyRelease(hc, meta.GetReleaseName(mg))
	if err != nil {
		log.Error(err, "Finding helm release")
		return fmt.Errorf("finding helm release: %w", err)
	}
	if rel != nil {
		log.Error(err, "Composition not deleted, release still exists", "release", meta.GetReleaseName(mg))
		return fmt.Errorf("composition not deleted, release %s still exists", meta.GetReleaseName(mg))
	}

	log.Debug("Uninstalling RBAC", "package", pkg.URL)

	compositionGVR, err := h.pluralizer.GVKtoGVR(mg.GroupVersionKind())
	if err != nil {
		log.Error(err, "Converting GVK to GVR")
		return fmt.Errorf("converting GVK to GVR: %w", err)
	}
	// Get Resources and generate RBAC
	generated, err := h.rbacgen.
		WithBaseName(meta.GetReleaseName(mg)).
		Generate(rbacgen.Parameters{
			CompositionName:                mg.GetName(),
			CompositionNamespace:           mg.GetNamespace(),
			CompositionGVR:                 compositionGVR,
			CompositionDefinitionName:      pkg.CompositionDefinitionInfo.Name,
			CompositionDefinitionNamespace: pkg.CompositionDefinitionInfo.Namespace,
			CompositionDefintionGVR:        pkg.CompositionDefinitionInfo.GVR,
		})
	rbInstaller := rbac.NewRBACInstaller(h.dynamicClient)
	err = rbInstaller.UninstallRBAC(generated)
	if err != nil {
		log.Error(err, "Uninstalling RBAC")
		return err
	}

	h.eventRecorder.Event(mg, event.Normal(reasonDeleted, "Delete", fmt.Sprintf("Deleted composition: %s", mg.GetName())))
	log.Debug("Composition package removed.", "package", pkg.URL)
	meta.RemoveAnnotations(mg, compositionMeta.AnnotationKeyReconciliationGracefullyPausedTime)

	_, err = tools.Update(ctx, mg, updateOpts)
	if err != nil {
		log.Error(err, "Updating cr with values")
		return fmt.Errorf("updating cr with values: %w", err)
	}

	return nil
}

func (h *handler) helmClientForResource(mg *unstructured.Unstructured, registryAuth *helmclient.RegistryAuth) (helmclient.Client, error) {
	log := h.logger.WithValues("apiVersion", mg.GetAPIVersion()).
		WithValues("kind", mg.GetKind()).
		WithValues("name", mg.GetName()).
		WithValues("namespace", mg.GetNamespace())

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

	return helmclient.NewClientFromRestConf(&helmclient.RestConfClientOptions{
		Options:    opts,
		RestConfig: h.kubeconfig,
	})
}

func (h *handler) helmClientForResourceWithTransportWrapper(mg *unstructured.Unstructured, registryAuth *helmclient.RegistryAuth, transportWrapper func(http.RoundTripper) http.RoundTripper) (helmclient.Client, error) {
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

	h.kubeconfig.WrapTransport = transportWrapper

	return helmclient.NewClientFromRestConf(&helmclient.RestConfClientOptions{
		Options:    opts,
		RestConfig: h.kubeconfig,
	})
}
