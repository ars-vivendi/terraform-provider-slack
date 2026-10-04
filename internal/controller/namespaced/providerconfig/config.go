package providerconfig

import (
	"github.com/ars-vivendi/terraform-provider-slack/apis/namespaced/v1beta1"
	"github.com/crossplane/crossplane-runtime/v2/pkg/event"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/providerconfig"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/crossplane/upjet/v2/pkg/controller"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SetupWebhookWithManager registers the standard provider configuration webhook.
func SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &v1beta1.ProviderConfig{}).Complete()
}

// Setup registers Crossplane's standard provider configuration usage controllers.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	for _, obj := range []client.Object{&v1beta1.ProviderConfig{}, &v1beta1.ClusterProviderConfig{}} {
		gvk, err := mgr.GetClient().GroupVersionKindFor(obj)
		if err != nil {
			return err
		}
		name := providerconfig.ControllerName(schema.GroupKind{Group: gvk.Group, Kind: gvk.Kind}.String())
		kinds := resource.ProviderConfigKinds{Config: gvk, Usage: v1beta1.ProviderConfigUsageGroupVersionKind, UsageList: v1beta1.ProviderConfigUsageListGroupVersionKind}
		if err := ctrl.NewControllerManagedBy(mgr).Named(name).WithOptions(o.ForControllerRuntime()).For(obj).
			Watches(&v1beta1.ProviderConfigUsage{}, &resource.EnqueueRequestForProviderConfig{}).
			Complete(providerconfig.NewReconciler(mgr, kinds, providerconfig.WithLogger(o.Logger.WithValues("controller", name)), providerconfig.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))))); err != nil {
			return err
		}
	}
	return nil
}

// SetupGated starts configuration controllers once their CRDs are established.
func SetupGated(mgr ctrl.Manager, o controller.Options) error {
	o.Gate.Register(func() {
		if err := Setup(mgr, o); err != nil {
			mgr.GetLogger().Error(err, "setup Slack provider configuration controllers")
		}
	}, v1beta1.ProviderConfigGroupVersionKind, v1beta1.ClusterProviderConfigGroupVersionKind, v1beta1.ProviderConfigUsageGroupVersionKind)
	return nil
}
