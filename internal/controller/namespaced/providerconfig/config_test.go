package providerconfig

import (
	"context"
	"reflect"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/ars-vivendi/terraform-provider-slack/apis/namespaced/v1beta1"
)

// Exercise the handler factory used by Setup with both configuration kinds.
// These assertions concern routing requests, not reconciler usage counts.
func TestUsageEventRouting(t *testing.T) {
	usage := func(kind, name, namespace string) *v1beta1.ProviderConfigUsage {
		u := &v1beta1.ProviderConfigUsage{ObjectMeta: metav1.ObjectMeta{Name: "usage", Namespace: namespace}}
		u.SetProviderConfigReference(xpv2.ProviderConfigReference{Kind: kind, Name: name})
		return u
	}
	for _, gvk := range []struct{ kind, namespace string }{{"ProviderConfig", "team-a"}, {"ClusterProviderConfig", ""}} {
		t.Run(gvk.kind, func(t *testing.T) {
			h := usageEventHandler(v1beta1.SchemeGroupVersion.WithKind(gvk.kind))
			for _, tc := range []struct {
				name string
				run  func(resource.RateLimitingInterface)
				want []types.NamespacedName
			}{
				{
					name: "same name different kinds",
					run: func(q resource.RateLimitingInterface) {
						for _, kind := range []string{"ProviderConfig", "ClusterProviderConfig"} {
							h.Create(context.Background(), event.CreateEvent{Object: usage(kind, "shared", "team-a")}, q)
						}
					},
					want: []types.NamespacedName{{Name: "shared", Namespace: gvk.namespace}},
				},
				{
					name: "same name across namespaces",
					run: func(q resource.RateLimitingInterface) {
						for _, ns := range []string{"team-a", "team-b"} {
							h.Create(context.Background(), event.CreateEvent{Object: usage(gvk.kind, "shared", ns)}, q)
						}
					},
					want: func() []types.NamespacedName {
						if gvk.kind == "ProviderConfig" {
							return []types.NamespacedName{{Name: "shared", Namespace: "team-a"}, {Name: "shared", Namespace: "team-b"}}
						}
						return []types.NamespacedName{{Name: "shared"}}
					}(),
				},
				{
					name: "old and new names and namespaces",
					run: func(q resource.RateLimitingInterface) {
						h.Update(context.Background(), event.UpdateEvent{ObjectOld: usage(gvk.kind, "old", "team-a"), ObjectNew: usage(gvk.kind, "new", "team-b")}, q)
					},
					want: func() []types.NamespacedName {
						ns := ""
						if gvk.kind == "ProviderConfig" {
							ns = "team-b"
						}
						return []types.NamespacedName{{Name: "old", Namespace: gvk.namespace}, {Name: "new", Namespace: ns}}
					}(),
				},
				{
					name: "kind transition same name",
					run: func(q resource.RateLimitingInterface) {
						h.Update(context.Background(), event.UpdateEvent{ObjectOld: usage("ProviderConfig", "shared", "team-a"), ObjectNew: usage("ClusterProviderConfig", "shared", "team-a")}, q)
					},
					want: []types.NamespacedName{{Name: "shared", Namespace: gvk.namespace}},
				},
				{
					name: "unrelated kind",
					run: func(q resource.RateLimitingInterface) {
						h.Create(context.Background(), event.CreateEvent{Object: usage("OtherConfig", "shared", "team-a")}, q)
					},
				},
				{
					name: "delete and generic retain scope",
					run: func(q resource.RateLimitingInterface) {
						h.Delete(context.Background(), event.DeleteEvent{Object: usage(gvk.kind, "deleted", "team-a")}, q)
						h.Generic(context.Background(), event.GenericEvent{Object: usage(gvk.kind, "generic", "team-a")}, q)
					},
					want: []types.NamespacedName{{Name: "deleted", Namespace: gvk.namespace}, {Name: "generic", Namespace: gvk.namespace}},
				},
			} {
				t.Run(tc.name, func(t *testing.T) {
					q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
					defer q.ShutDown()
					tc.run(q)
					var got []types.NamespacedName
					for q.Len() > 0 {
						r, _ := q.Get()
						got = append(got, r.NamespacedName)
						q.Done(r)
					}
					if !reflect.DeepEqual(got, tc.want) {
						t.Fatalf("requests = %v, want %v", got, tc.want)
					}
				})
			}
		})
	}
}
