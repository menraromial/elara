package main

import (
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that `exec-entrypoint` and `run` can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	// *** NEW IMPORT REQUIRED FOR THE FIX ***
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	// IMPORTANT: Ensure this path matches your module name in go.mod
	greenopsv1 "elara/api/v1"
	"elara/controllers"
	//+kubebuilder:scaffold:imports
)

var (
	// scheme is used by the framework to map Go types to Kubernetes GroupVersionKinds (GVKs).
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	// `init()` is a special Go function that runs before main().
	// We use it here to register all the necessary types with the scheme.

	// Registers the basic built-in Kubernetes types (Pods, Deployments, etc.).
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	// Registers our own API's types (ElaraPolicy).
	utilruntime.Must(greenopsv1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	// --- Step 1: Configure command-line flags ---
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string

	// Flag for the metrics endpoint address (for Prometheus).
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	// Flag for the health probe endpoint address (for liveness/readiness probes).
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	// Flag to enable leader election. This is critical in production to ensure that only one instance of the controller is active at a time.
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")

	// Configure the `zap` logger options.
	opts := zap.Options{
		Development: true, // `true` for more human-readable logs during development.
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	// Initialize the global logger for the controller-runtime.
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// --- Step 2: Create the Manager ---
	// The Manager is the central component that runs all the controllers.
	// `ctrl.GetConfigOrDie()` retrieves the cluster configuration (either from `~/.kube/config` or from the in-cluster service account).
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		// *** THIS IS THE CORRECTED STRUCTURE ***
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "leader.greenops.elara.dev", // A unique ID for the leader election lock.
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// --- Step 3: Initialize and register the controller ---
	// This is where we instantiate our reconciler and attach it to the manager.
	if err = (&controllers.ElaraPolicyReconciler{
		Client: mgr.GetClient(), // The client for interacting with the Kubernetes API.
		Scheme: mgr.GetScheme(), // The scheme of known types.
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ElaraPolicy")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	// --- Step 4: Add health probes ---
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	// --- Step 5: Start the Manager ---
	setupLog.Info("starting manager")
	// `mgr.Start` is a blocking operation. It starts all registered controllers
	// and will only stop when the process receives a termination signal (e.g., Ctrl+C or a signal from Kubernetes).
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}