// Command provider runs the generated native Upjet Slack controllers.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	xpcontroller "github.com/crossplane/crossplane-runtime/v2/pkg/controller"
	"github.com/crossplane/crossplane-runtime/v2/pkg/feature"
	"github.com/crossplane/crossplane-runtime/v2/pkg/gate"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/customresourcesgate"
	ujcontroller "github.com/crossplane/upjet/v2/pkg/controller"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	apis "github.com/ars-vivendi/terraform-provider-slack/apis/namespaced"
	"github.com/ars-vivendi/terraform-provider-slack/config"
	"github.com/ars-vivendi/terraform-provider-slack/internal/clients"
	controllers "github.com/ars-vivendi/terraform-provider-slack/internal/controller/namespaced"
)

// Version can be set by the packaging pipeline with -ldflags.
var Version = "development"

func main() {
	debug := flag.Bool("debug", false, "Enable debug logging")
	leader := flag.Bool("leader-election", false, "Enable leader election")
	poll := flag.Duration("poll", 10*time.Minute, "Drift observation interval")
	metrics := flag.String("metrics-bind-address", ":8080", "Metrics listen address")
	health := flag.String("health-probe-bind-address", ":8081", "Health listen address")
	rate := flag.Int("max-reconcile-rate", 10, "Maximum concurrent reconciliations and global rate")
	version := flag.Bool("version", false, "Print version")
	managementPolicies := flag.Bool("enable-management-policies", true, "Enable standard Crossplane management policies")
	flag.Parse()
	if *version {
		fmt.Println(Version)
		return
	}
	if *rate < 1 || *poll <= 0 {
		fail(fmt.Errorf("max-reconcile-rate and poll must be positive"))
	}
	zl := zap.New(zap.UseDevMode(*debug))
	ctrl.SetLogger(zl)
	log := logging.NewLogrLogger(zl.WithName("provider-slack"))
	scheme := runtime.NewScheme()
	fail(clientgoscheme.AddToScheme(scheme))
	fail(apis.AddToScheme(scheme))
	fail(apiextensionsv1.AddToScheme(scheme))
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:         scheme,
		LeaderElection: *leader, LeaderElectionID: "provider-slack.slack.m.arsvivendi.io",
		Metrics: metricsserver.Options{BindAddress: *metrics}, HealthProbeBindAddress: *health,
		Client: client.Options{Cache: &client.CacheOptions{DisableFor: []client.Object{&corev1.Secret{}}}},
	})
	fail(err)
	crdGate := new(gate.Gate[schema.GroupVersionKind])
	o := ujcontroller.Options{
		Options:  xpcontroller.Options{Logger: log, GlobalRateLimiter: ratelimiter.NewGlobal(*rate), PollInterval: *poll, MaxConcurrentReconciles: *rate, Features: &feature.Flags{}, Gate: crdGate},
		Provider: config.GetProvider(), SetupFn: clients.TerraformSetupBuilder(*managementPolicies), OperationTrackerStore: ujcontroller.NewOperationStore(log),
	}
	if *managementPolicies {
		o.Features.Enable(feature.EnableBetaManagementPolicies)
	}
	fail(customresourcesgate.Setup(mgr, xpcontroller.Options{Logger: log, Gate: crdGate, MaxConcurrentReconciles: 1}))
	fail(controllers.SetupGated(mgr, o))
	fail(mgr.AddHealthzCheck("healthz", healthz.Ping))
	fail(mgr.AddReadyzCheck("readyz", healthz.Ping))
	fail(mgr.Start(ctrl.SetupSignalHandler()))
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
