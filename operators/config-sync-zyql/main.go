package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	configsyncv1 "config-sync-operator/api/v1"
	"config-sync-operator/controllers"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configsyncv1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var probeAddr string
	var enableDeployment bool
	var enableWebhook bool
	var webhookPort int
	var certDir string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableWebhook, "enable-webhook", true, "Enable webhook server for resource validation.")
	flag.IntVar(&webhookPort, "webhook-port", 9443, "The port the webhook server binds to.")
	flag.StringVar(&certDir, "cert-dir", "/tmp/k8s-webhook-server/serving-certs", "The directory containing webhook TLS certificates.")
	flag.BoolVar(&enableDeployment, "enable-deployment", false, "Enable deployment of the operator")

	// Set development mode for zap logger based on deployment flag, true for dev and the log have colored output, false for production.
	opts := zap.Options{
		Development: enableDeployment,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgrOptions := ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         false, // Leader election disabled - operator is idempotent
	}

	// Only configure webhook server if enabled
	if enableWebhook {
		mgrOptions.WebhookServer = webhook.NewServer(webhook.Options{
			Port:    webhookPort,
			CertDir: certDir,
		})
		setupLog.Info("webhook server enabled", "port", webhookPort, "certDir", certDir)
	} else {
		setupLog.Info("webhook server disabled - resources will not be protected from manual modifications")
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), mgrOptions)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controllers.ConfigSyncReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ConfigSync")
		os.Exit(1)
	}

	if err = (&controllers.SecretReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Secret")
		os.Exit(1)
	}

	if err = (&controllers.ConfigMapReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ConfigMap")
		os.Exit(1)
	}

	if err = (&controllers.NamespaceReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Namespace")
		os.Exit(1)
	}

	// Setup webhooks (only if enabled)
	if enableWebhook {
		if err = controllers.SetupSecretWebhook(mgr); err != nil {
			setupLog.Error(err, "unable to setup webhook", "webhook", "Secret")
			os.Exit(1)
		}

		if err = controllers.SetupConfigMapWebhook(mgr); err != nil {
			setupLog.Error(err, "unable to setup webhook", "webhook", "ConfigMap")
			os.Exit(1)
		}
		setupLog.Info("webhooks registered successfully")
	}

	setupLog.Info("manager configuration",
		"scheme", mgr.GetScheme(),
		"cache", mgr.GetCache() != nil,
		"client", mgr.GetClient() != nil,
	)

	// Setup health checks
	// Liveness probe - simple ping check
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	// Readiness probe - check if informer cache is synced
	if err := mgr.AddReadyzCheck("readyz", func(req *http.Request) error {
		if mgr.GetCache().WaitForCacheSync(req.Context()) {
			return nil
		}
		return fmt.Errorf("cache not synced")
	}); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	// mgr.Add(runnable)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
