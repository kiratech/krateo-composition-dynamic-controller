package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/krateoplatformops/unstructured-runtime/pkg/controller"
	"github.com/krateoplatformops/unstructured-runtime/pkg/controller/builder"
	ctrlevent "github.com/krateoplatformops/unstructured-runtime/pkg/controller/event"
	"github.com/krateoplatformops/unstructured-runtime/pkg/metrics/server"

	"github.com/go-logr/logr"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/composition"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/meta"
	"github.com/krateoplatformops/composition-dynamic-controller/internal/tools/helmchart/archive"
	"github.com/krateoplatformops/plumbing/env"
	"github.com/krateoplatformops/plumbing/kubeutil/event"
	"github.com/krateoplatformops/plumbing/kubeutil/eventrecorder"
	"github.com/krateoplatformops/plumbing/ptr"
	prettylog "github.com/krateoplatformops/plumbing/slogs/pretty"

	"github.com/krateoplatformops/unstructured-runtime/pkg/logging"
	"github.com/krateoplatformops/unstructured-runtime/pkg/pluralizer"
	"github.com/krateoplatformops/unstructured-runtime/pkg/workqueue"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	serviceName = "composition-dynamic-controller"
)

func main() {
	// Flags
	kubeconfig := flag.String("kubeconfig", env.String("KUBECONFIG", ""),
		"absolute path to the kubeconfig file")
	debug := flag.Bool("debug",
		env.Bool("COMPOSITION_CONTROLLER_DEBUG", false), "dump verbose output")
	workers := flag.Int("workers", env.Int("COMPOSITION_CONTROLLER_WORKERS", 1), "number of workers")
	resyncInterval := flag.Duration("resync-interval",
		env.Duration("COMPOSITION_CONTROLLER_RESYNC_INTERVAL", time.Minute*3), "resync interval")
	resourceGroup := flag.String("group",
		env.String("COMPOSITION_CONTROLLER_GROUP", ""), "resource api group")
	resourceVersion := flag.String("version",
		env.String("COMPOSITION_CONTROLLER_VERSION", ""), "resource api version")
	resourceName := flag.String("resource",
		env.String("COMPOSITION_CONTROLLER_RESOURCE", ""), "resource plural name")
	namespace := flag.String("namespace",
		env.String("COMPOSITION_CONTROLLER_NAMESPACE", ""), "namespace to watch, empty for all namespaces")
	chart := flag.String("chart",
		env.String("COMPOSITION_CONTROLLER_CHART", ""), "chart")
	urlChartInspector := flag.String("urlChartInspector",
		env.String("URL_CHART_INSPECTOR", "http://chart-inspector.krateo-system.svc.cluster.local:8081/"), "url chart inspector")
	saName := flag.String("saName",
		env.String("COMPOSITION_CONTROLLER_SA_NAME", ""), "service account name")
	saNamespace := flag.String("saNamespace",
		env.String("COMPOSITION_CONTROLLER_SA_NAMESPACE", ""), "service account namespace")
	maxErrorRetryInterval := flag.Duration("max-error-retry-interval",
		env.Duration("COMPOSITION_CONTROLLER_MAX_ERROR_RETRY_INTERVAL", 60*time.Second), "The maximum interval between retries when an error occurs. This should be less than the half of the poll interval.")
	minErrorRetryInterval := flag.Duration("min-error-retry-interval",
		env.Duration("COMPOSITION_CONTROLLER_MIN_ERROR_RETRY_INTERVAL", 1*time.Second), "The minimum interval between retries when an error occurs. This should be less than max-error-retry-interval.")
	maxErrorRetry := flag.Int("max-error-retries",
		env.Int("COMPOSITION_CONTROLLER_MAX_ERROR_RETRIES", 5), "How many times to retry the processing of a resource when an error occurs before giving up and dropping the resource.")
	metricsServerPort := flag.Int("metrics-server-port",
		env.Int("COMPOSITION_CONTROLLER_METRICS_SERVER_PORT", 0), "The address to bind the metrics server to. If empty, metrics server is disabled.")

	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "Flags:")
		flag.PrintDefaults()
	}

	flag.Parse()

	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()

	logLevel := slog.LevelInfo
	if *debug {
		logLevel = slog.LevelDebug
	}

	lh := prettylog.New(&slog.HandlerOptions{
		Level:     logLevel,
		AddSource: false,
	},
		prettylog.WithDestinationWriter(os.Stderr),
		prettylog.WithColor(),
		prettylog.WithOutputEmptyAttrs(),
	)

	log := logging.NewLogrLogger(logr.FromSlogHandler(slog.New(lh).Handler()))

	// Kubernetes configuration
	var cfg *rest.Config
	var err error
	if len(*kubeconfig) > 0 {
		cfg, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
	} else {
		cfg, err = builder.GetConfig()
	}
	if err != nil {
		log.Error(err, "Building kubeconfig.")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), []os.Signal{
		os.Interrupt,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGKILL,
		syscall.SIGHUP,
		syscall.SIGQUIT,
	}...)
	defer cancel()

	rec, err := eventrecorder.Create(ctx, cfg, "composition-dynamic-controller", nil)
	if err != nil {
		log.Error(err, "Creating event recorder.")
		os.Exit(1)
	}
	pluralizer := pluralizer.New()

	var pig archive.Getter
	if len(*chart) > 0 {
		pig = archive.Static(*chart)
	} else {
		pig, err = archive.Dynamic(cfg, pluralizer)
		if err != nil {
			log.Error(err, "Creating chart url info getter.")
			os.Exit(1)
		}
	}

	log.WithValues("debug", *debug).
		WithValues("resyncInterval", *resyncInterval).
		WithValues("group", *resourceGroup).
		WithValues("version", *resourceVersion).
		WithValues("resource", *resourceName).
		WithValues("namespace", *namespace).
		WithValues("workers", *workers).
		WithValues("minErrorRetryInterval", *minErrorRetryInterval).
		WithValues("maxErrorRetryInterval", *maxErrorRetryInterval).
		WithValues("maxErrorRetry", *maxErrorRetry).
		WithValues("metricsServerPort", *metricsServerPort).
		Info("Starting composition dynamic controller.")

	// Create a label requirement for the composition version
	labelreq, err := labels.NewRequirement(meta.CompositionVersionLabel, selection.Equals, []string{*resourceVersion})
	if err != nil {
		log.Error(err, "Creating label requirement.")
		os.Exit(1)
	}
	labelselector := labels.NewSelector().Add(*labelreq)

	handler := composition.NewHandler(cfg, pig, *event.NewAPIRecorder(rec), *pluralizer, *urlChartInspector, *saName, *saNamespace)

	opts := []builder.FuncOption{
		builder.WithLogger(log),
		builder.WithNamespace(*namespace),
		builder.WithResyncInterval(*resyncInterval),
		builder.WithGlobalRateLimiter(workqueue.NewExponentialTimedFailureRateLimiter[any](*minErrorRetryInterval, *maxErrorRetryInterval)),
		builder.WithMaxRetries(*maxErrorRetry),
		builder.WithListWatcher(controller.ListWatcherConfiguration{
			LabelSelector: ptr.To(labelselector.String()),
		}),
		builder.WithActionEvent(ctrlevent.CRUpdated, ctrlevent.Observe),
	}

	metricsServerBindAddress := ""
	if ptr.Deref(metricsServerPort, 0) != 0 {
		log.Info("Metrics server enabled", "bindAddress", fmt.Sprintf(":%d", *metricsServerPort))
		metricsServerBindAddress = fmt.Sprintf(":%d", *metricsServerPort)
		opts = append(opts, builder.WithMetrics(server.Options{
			BindAddress: metricsServerBindAddress,
		}))
	} else {
		log.Info("Metrics server disabled")
	}

	controller, err := builder.Build(ctx, builder.Configuration{
		Config: cfg,
		GVR: schema.GroupVersionResource{
			Group:    *resourceGroup,
			Version:  *resourceVersion,
			Resource: *resourceName,
		},
		ProviderName: serviceName,
	}, opts...)
	if err != nil {
		log.Error(err, "Creating controller.")
		os.Exit(1)
	}

	controller.SetExternalClient(handler)

	err = controller.Run(ctx, *workers)
	if err != nil {
		log.Error(err, "Running controller.")
		os.Exit(1)
	}
}
