package integration

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	ujcontroller "github.com/crossplane/upjet/v2/pkg/controller"
	"github.com/crossplane/upjet/v2/pkg/controller/handler"
	ujresource "github.com/crossplane/upjet/v2/pkg/resource"
	"github.com/crossplane/upjet/v2/pkg/terraform"
	tfsdk "github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	slackapi "github.com/slack-go/slack"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	conversation "github.com/ars-vivendi/terraform-provider-slack/apis/namespaced/conversation/v1alpha1"
	"github.com/ars-vivendi/terraform-provider-slack/config"
	"github.com/ars-vivendi/terraform-provider-slack/internal/clients"
)

// Match the generated controller's async connector, callbacks, policy support,
// empty initializer chain and operation-tracker finalizer. Reconciliation is
// driven manually through the real HTTP Kubernetes client.
func newAsyncReconciler(t *testing.T, c client.Client, setup terraform.SetupFn, store *ujcontroller.OperationTrackerStore) *managed.Reconciler {
	t.Helper()
	mgr, err := ctrl.NewManager(&rest.Config{Host: "http://unused.invalid"}, ctrl.Options{
		Scheme: c.Scheme(), Metrics: metricsserver.Options{BindAddress: "0"},
		MapperProvider: func(_ *rest.Config, _ *http.Client) (apimeta.RESTMapper, error) { return c.RESTMapper(), nil },
		NewClient:      func(_ *rest.Config, _ client.Options) (client.Client, error) { return c, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := logging.NewNopLogger()
	events := handler.NewEventHandler(handler.WithLogger(logger))
	callbacks := ujcontroller.NewAPICallbacks(mgr, resource.ManagedKind(conversation.Conversation_GroupVersionKind), ujcontroller.WithEventHandler(events), ujcontroller.WithStatusUpdates(false))
	connector := ujcontroller.NewTerraformPluginSDKAsyncConnector(c, store, setup, config.GetProvider().Resources["slack_conversation"],
		ujcontroller.WithTerraformPluginSDKAsyncLogger(logger), ujcontroller.WithTerraformPluginSDKAsyncConnectorEventHandler(events),
		ujcontroller.WithTerraformPluginSDKAsyncCallbackProvider(callbacks), ujcontroller.WithTerraformPluginSDKAsyncManagementPolicies(true))
	return managed.NewReconciler(mgr, resource.ManagedKind(conversation.Conversation_GroupVersionKind), managed.WithExternalConnecter(connector), managed.WithLogger(logger),
		managed.WithInitializers(managed.InitializerChain{}), managed.WithManagementPolicies(),
		managed.WithFinalizer(ujcontroller.NewOperationTrackerFinalizer(store, resource.NewAPIFinalizer(c, managed.FinalizerName))))
}

func waitForAsyncCondition(t *testing.T, c client.Client, store *ujcontroller.OperationTrackerStore, m *conversation.Conversation, status corev1.ConditionStatus, reason xpv2.ConditionReason) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := c.Get(context.Background(), client.ObjectKeyFromObject(m), m); err != nil {
			t.Fatal(err)
		}
		condition := m.GetCondition(ujresource.TypeLastAsyncOperation)
		if condition.Status == status && condition.Reason == reason && !store.Tracker(m).LastOperation.IsRunning() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("async callback did not report %s/%s: conditions=%v operation=%s error=%v", status, reason, m.Status.Conditions, store.Tracker(m).LastOperation.Type, store.Tracker(m).LastOperation.Error())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNativeDeleteDoesNotRecreateAfterTeamIDChange(t *testing.T) {
	for _, change := range []string{"forProvider change", "forProvider removal", "initProvider change", "initProvider removal", "forProvider overrides initProvider"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			s, k, c, setup := newBoundary(t)
			m := newConversation("replacement", "uid-replacement")
			// Keep changes/removals pending rather than late-initializing the old
			// teamId back into forProvider before the delete-only regression.
			m.SetManagementPolicies(xpv2.ManagementPolicies{xpv2.ManagementActionObserve, xpv2.ManagementActionCreate, xpv2.ManagementActionUpdate, xpv2.ManagementActionDelete})
			if strings.HasPrefix(change, "initProvider") || change == "forProvider overrides initProvider" {
				m.Spec.InitProvider.TeamID = ptr.To("Tinitial")
			} else {
				m.Spec.ForProvider.TeamID = ptr.To("Tinitial")
			}
			logger := logging.NewNopLogger()
			store := ujcontroller.NewOperationStore(logger)
			connector := ujcontroller.NewTerraformPluginSDKConnector(c, setup, config.GetProvider().Resources["slack_conversation"], store,
				ujcontroller.WithTerraformPluginSDKLogger(logger), ujcontroller.WithTerraformPluginSDKManagementPolicies(true))
			e, err := connector.Connect(ctx, m)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.Observe(ctx, m); err != nil {
				t.Fatal(err)
			}
			// A real initial team_id diff requires creation and must remain allowed.
			if _, err := e.Create(ctx, m); err != nil {
				t.Fatal(err)
			}
			if meta.GetExternalName(m) != "C123" || m.Status.AtProvider.TeamID == nil || *m.Status.AtProvider.TeamID != "Tinitial" {
				t.Fatalf("creation did not retain real SDK identity/team state: %+v", m.Status.AtProvider)
			}
			m.Finalizers = []string{managed.FinalizerName}
			switch change {
			case "forProvider change", "forProvider overrides initProvider":
				m.Spec.ForProvider.TeamID = ptr.To("Tother")
			case "forProvider removal":
				m.Spec.ForProvider.TeamID = nil
			case "initProvider change":
				m.Spec.InitProvider.TeamID = ptr.To("Tother")
			case "initProvider removal":
				m.Spec.InitProvider.TeamID = nil
			}
			desired := m.Spec.DeepCopy()
			e, err = connector.Connect(ctx, m)
			if err != nil {
				t.Fatal(err)
			}
			o, err := e.Observe(ctx, m)
			if err != nil || !o.ResourceExists {
				t.Fatalf("pending configuration could not observe the original channel: %+v err=%v", o, err)
			}
			if change == "initProvider change" {
				// Upjet ignores changes exclusive to initProvider after creation.
				if !o.ResourceUpToDate {
					t.Fatal("init-only change should be filtered from the native diff")
				}
			} else {
				if o.ResourceUpToDate {
					t.Fatal("pending teamId change/removal did not produce a replacement diff")
				}
				if _, err := e.Update(ctx, m); err == nil || !strings.Contains(err.Error(), "requires replacing it") || !strings.Contains(err.Error(), "team_id") {
					t.Fatalf("native Update did not reject the actual ForceNew diff: %v", err)
				}
			}
			s.mu.Lock()
			creates, archives, archived := s.calls["conversations.create"], s.calls["conversations.archive"], s.channel.IsArchived
			s.mu.Unlock()
			if creates != 1 || archives != 0 || archived || meta.GetExternalName(m) != "C123" {
				t.Fatalf("pending update changed channel identity or lifecycle: create=%d archive=%d archived=%v", creates, archives, archived)
			}
			// Delete on the SAME native external client retains the pending diff.
			// Upjet must pass a fresh destroy-only diff to SDK Apply, not reuse the
			// replacement plan. No restore, reconnect, or parameter mutation here.
			m.DeletionTimestamp = ptr.To(metav1.Now())
			if _, err := e.Delete(ctx, m); err != nil {
				t.Fatal(err)
			}
			if !store.Tracker(m).IsDeleted() || !reflect.DeepEqual(m.Spec, *desired) || meta.GetExternalName(m) != "C123" {
				t.Fatalf("delete changed pending inputs/identity or failed to clear SDK state: %+v", m)
			}
			// The real managed reconciler can finalize the deleting declaration
			// with its changed desired configuration and the original channel ID.
			k.put("/apis/conversation.slack.m.arsvivendi.io/v1alpha1/namespaces/team/conversations/replacement", m)
			r := newAsyncReconciler(t, c, setup, store)
			request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(m)}
			if _, err := r.Reconcile(ctx, request); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, request.NamespacedName, m); err != nil {
				t.Fatal(err)
			}
			if len(m.Finalizers) != 0 || meta.GetExternalName(m) != "C123" || m.Status.AtProvider.ID == nil || *m.Status.AtProvider.ID != "C123" || !reflect.DeepEqual(m.Spec, *desired) {
				t.Fatalf("deletion did not finalize with pending inputs and original identity: %+v", m)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.calls["conversations.create"] != 1 || s.calls["conversations.archive"] != 1 || !s.channel.IsArchived || s.channel.ID != "C123" {
				t.Fatalf("delete-only Apply recreated or lost the channel: calls=%v channel=%+v", s.calls, s.channel)
			}
		})
	}
}

func TestPartialImportManagementPolicies(t *testing.T) {
	for _, action := range []xpv2.ManagementAction{xpv2.ManagementActionLateInitialize, xpv2.ManagementActionDelete} {
		t.Run(string(action), func(t *testing.T) {
			ctx := context.Background()
			s, k, c, setup := newBoundary(t)
			s.channel = &slackapi.Channel{GroupConversation: slackapi.GroupConversation{Conversation: slackapi.Conversation{ID: "C123", Created: 123}, Creator: "Ucreator", Name: "observed-channel"}}
			s.members = map[string]bool{"Ucreator": true}
			m := newConversation("partial-import", "uid-partial-import")
			m.Spec.ForProvider = conversation.ConversationParameters{}
			m.SetManagementPolicies(xpv2.ManagementPolicies{xpv2.ManagementActionObserve, action})
			meta.SetExternalName(m, "C123")
			store := ujcontroller.NewOperationStore(logging.NewNopLogger())
			if action == xpv2.ManagementActionLateInitialize {
				// Persist the original import without write parameters so the real
				// managed reconciler must also perform and persist late initialization.
				k.put("/apis/conversation.slack.m.arsvivendi.io/v1alpha1/namespaces/team/conversations/partial-import", m)
				connector := ujcontroller.NewTerraformPluginSDKConnector(c, setup, config.GetProvider().Resources["slack_conversation"], store,
					ujcontroller.WithTerraformPluginSDKLogger(logging.NewNopLogger()), ujcontroller.WithTerraformPluginSDKManagementPolicies(true))
				e, err := connector.Connect(ctx, m)
				if err != nil {
					t.Fatal(err)
				}
				o, err := e.Observe(ctx, m)
				if err != nil || !o.ResourceExists || !o.ResourceLateInitialized || m.Spec.ForProvider.Name == nil || *m.Spec.ForProvider.Name != "observed-channel" || string(o.ConnectionDetails["channel_id"]) != "C123" {
					t.Fatalf("partial import did not late initialize from Refresh: observation=%+v parameters=%+v err=%v", o, m.Spec.ForProvider, err)
				}
				e, err = connector.Connect(ctx, m)
				if err != nil {
					t.Fatal(err)
				}
				// Native observation still plans SDK defaults absent from imported
				// state. LateInitialize does not authorize applying those defaults.
				// ResourceUpToDate describes the diff, not policy-aware readiness.
				if o, err := e.Observe(ctx, m); err != nil || !o.ResourceExists || o.ResourceLateInitialized || o.ResourceUpToDate {
					t.Fatalf("expected stable late initialization with unapplied SDK defaults: %+v err=%v", o, err)
				}
				parameters, err := m.GetMergedParameters(true)
				if err != nil {
					t.Fatal(err)
				}
				diff, err := config.GetProvider().Resources["slack_conversation"].TerraformResource.Diff(ctx, store.Tracker(m).GetTfState(), tfsdk.NewResourceConfigRaw(parameters), nil)
				if err != nil || diff == nil {
					t.Fatalf("cannot inspect real SDK import diff: %v", err)
				}
				for parameter, want := range map[string]string{"action_on_destroy": "archive", "action_on_update_permanent_members": "kick"} {
					if attribute := diff.Attributes[parameter]; attribute == nil || attribute.New != want {
						t.Fatalf("residual import diff is not the expected upstream default for %s: %s", parameter, diff.GoString())
					}
				}
				r := newAsyncReconciler(t, c, setup, store)
				request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(m)}
				for i := 0; i < 2; i++ {
					if _, err := r.Reconcile(ctx, request); err != nil {
						t.Fatal(err)
					}
				}
				if err := c.Get(ctx, request.NamespacedName, m); err != nil {
					t.Fatal(err)
				}
				if m.Spec.ForProvider.Name == nil || *m.Spec.ForProvider.Name != "observed-channel" || m.Status.AtProvider.ID == nil || *m.Status.AtProvider.ID != "C123" ||
					m.GetCondition(xpv2.TypeReady).Status != corev1.ConditionTrue || m.GetCondition(xpv2.TypeSynced).Status != corev1.ConditionTrue {
					t.Fatalf("late-initialize policy did not persist import and report Ready/Synced: %+v", m)
				}
			} else {
				m.Finalizers = []string{managed.FinalizerName}
				m.DeletionTimestamp = ptr.To(metav1.Now())
				k.put("/apis/conversation.slack.m.arsvivendi.io/v1alpha1/namespaces/team/conversations/partial-import", m)
				r := newAsyncReconciler(t, c, setup, store)
				request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(m)}
				if _, err := r.Reconcile(ctx, request); err != nil {
					t.Fatal(err)
				}
				waitForAsyncCondition(t, c, store, m, corev1.ConditionTrue, ujresource.ReasonSuccess)
				if _, err := r.Reconcile(ctx, request); err != nil {
					t.Fatal(err)
				}
				if err := c.Get(ctx, request.NamespacedName, m); err != nil {
					t.Fatal(err)
				}
				if len(m.Finalizers) != 0 || m.Spec.ForProvider.Name != nil || meta.GetExternalName(m) != "C123" {
					t.Fatalf("observe/delete import did not finalize without changing write inputs: %+v", m)
				}
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			wantArchives := 0
			if action == xpv2.ManagementActionDelete {
				wantArchives = 1
			}
			for method := range s.calls {
				if method != "conversations.info" && !(method == "conversations.archive" && wantArchives == 1) {
					t.Fatalf("partial import performed an unauthorized Slack operation: %v", s.calls)
				}
			}
			if s.calls["conversations.archive"] != wantArchives {
				t.Fatalf("partial import archive count=%d want=%d", s.calls["conversations.archive"], wantArchives)
			}
		})
	}
}

func TestPartialImportStillValidatesSuppliedActions(t *testing.T) {
	for _, action := range []xpv2.ManagementAction{xpv2.ManagementActionLateInitialize, xpv2.ManagementActionDelete} {
		for _, parameter := range []string{"action_on_destroy", "action_on_update_permanent_members"} {
			t.Run(string(action)+"/"+parameter, func(t *testing.T) {
				s, _, c, setup := newBoundary(t)
				s.channel = &slackapi.Channel{GroupConversation: slackapi.GroupConversation{Conversation: slackapi.Conversation{ID: "C123"}, Name: "observed-channel"}}
				m := newConversation("invalid-import", "uid-invalid-import")
				m.Spec.ForProvider = conversation.ConversationParameters{}
				m.SetManagementPolicies(xpv2.ManagementPolicies{xpv2.ManagementActionObserve, action})
				meta.SetExternalName(m, "C123")
				if parameter == "action_on_destroy" {
					m.Spec.ForProvider.ActionOnDestroy = ptr.To("delete")
				} else {
					m.Spec.ForProvider.ActionOnUpdatePermanentMembers = ptr.To("ignore")
				}
				e, err := ujcontroller.NewTerraformPluginSDKConnector(c, setup, config.GetProvider().Resources["slack_conversation"], ujcontroller.NewOperationStore(logging.NewNopLogger()),
					ujcontroller.WithTerraformPluginSDKLogger(logging.NewNopLogger()), ujcontroller.WithTerraformPluginSDKManagementPolicies(true)).Connect(context.Background(), m)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := e.Observe(context.Background(), m); err == nil || !strings.Contains(err.Error(), "validation failed") || !strings.Contains(err.Error(), parameter) {
					t.Fatalf("supplied upstream validation error was masked: %v", err)
				}
				if m.Spec.ForProvider.Name != nil {
					t.Fatal("validation injected an observed name into actual parameters")
				}
				s.mu.Lock()
				defer s.mu.Unlock()
				if len(s.calls) != 1 || s.calls["conversations.info"] != 1 {
					t.Fatalf("invalid partial configuration reached writes: %v", s.calls)
				}
			})
		}
	}
}

func TestWritableMissingNameCannotBorrowExistingState(t *testing.T) {
	for _, tc := range []struct {
		name     string
		enabled  bool
		policies xpv2.ManagementPolicies
		existing bool
	}{
		{"default create", true, xpv2.ManagementPolicies{xpv2.ManagementActionAll}, false},
		{"default update", true, xpv2.ManagementPolicies{xpv2.ManagementActionAll}, true},
		{"create policy", true, xpv2.ManagementPolicies{xpv2.ManagementActionObserve, xpv2.ManagementActionCreate}, false},
		{"update policy", true, xpv2.ManagementPolicies{xpv2.ManagementActionObserve, xpv2.ManagementActionUpdate}, true},
		{"disabled policies", false, xpv2.ManagementPolicies{xpv2.ManagementActionObserve}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, c, _ := newBoundary(t)
			m := newConversation("missing-name", "uid-missing-name")
			m.Spec.ForProvider.Name = nil
			m.SetManagementPolicies(tc.policies)
			if tc.existing {
				meta.SetExternalName(m, "C123")
				m.Status.AtProvider.Name = ptr.To("observed-channel")
			}
			_, err := clients.TerraformSetupBuilder(tc.enabled)(context.Background(), c, m)
			if err == nil || !strings.Contains(err.Error(), "validation failed") || !strings.Contains(err.Error(), "name") {
				t.Fatalf("incomplete writable inputs were accepted: %v", err)
			}
			if len(s.calls) != 0 {
				t.Fatalf("incomplete writable configuration reached Slack: %v", s.calls)
			}
		})
	}
}

func TestSetupValidationMatchesGeneratedParameterMerge(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		edit    func(*conversation.Conversation)
		wantOK  bool
	}{
		{"initProvider name", true, func(m *conversation.Conversation) {
			m.Spec.ForProvider.Name = nil
			m.Spec.InitProvider.Name = ptr.To("initial-name")
		}, true},
		{"initProvider ignored when disabled", false, func(m *conversation.Conversation) {
			m.Spec.ForProvider.Name = nil
			m.Spec.InitProvider.Name = ptr.To("initial-name")
		}, false},
		{"forProvider valid action wins", true, func(m *conversation.Conversation) { m.Spec.InitProvider.ActionOnDestroy = ptr.To("delete") }, true},
		{"forProvider invalid action wins", true, func(m *conversation.Conversation) {
			m.Spec.ForProvider.ActionOnDestroy = ptr.To("delete")
			m.Spec.InitProvider.ActionOnDestroy = ptr.To("archive")
		}, false},
		{"initProvider invalid action used", true, func(m *conversation.Conversation) {
			m.Spec.ForProvider.ActionOnDestroy = nil
			m.Spec.InitProvider.ActionOnDestroy = ptr.To("delete")
		}, false},
		{"empty forProvider uses initProvider", true, func(m *conversation.Conversation) {
			m.Spec.ForProvider.Name = ptr.To("")
			m.Spec.InitProvider.Name = ptr.To("initial-name")
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, c, _ := newBoundary(t)
			m := newConversation("merged", "uid-merged")
			tc.edit(m)
			_, err := clients.TerraformSetupBuilder(tc.enabled)(context.Background(), c, m)
			if tc.wantOK && err != nil {
				t.Fatalf("valid merged configuration rejected: %v", err)
			}
			if !tc.wantOK && (err == nil || !strings.Contains(err.Error(), "validation failed")) {
				t.Fatalf("invalid merged configuration accepted: %v", err)
			}
		})
	}
}

func TestAsyncPermissionFailureIsReported(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		t.Run(operation, func(t *testing.T) {
			ctx := context.Background()
			s, k, c, setup := newBoundary(t)
			m := newConversation("async-denied", "uid-async-denied")
			m.SetWriteConnectionSecretToReference(&xpv2.LocalSecretReference{Name: "denied-output"})
			store := ujcontroller.NewOperationStore(logging.NewNopLogger())
			method, reason := "conversations.create", ujresource.ReasonAsyncCreateFailure
			if operation == "update" {
				e, err := ujcontroller.NewTerraformPluginSDKConnector(c, setup, config.GetProvider().Resources["slack_conversation"], store,
					ujcontroller.WithTerraformPluginSDKLogger(logging.NewNopLogger()), ujcontroller.WithTerraformPluginSDKManagementPolicies(true)).Connect(ctx, m)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := e.Observe(ctx, m); err != nil {
					t.Fatal(err)
				}
				if _, err := e.Create(ctx, m); err != nil {
					t.Fatal(err)
				}
				m.Spec.ForProvider.Topic = ptr.To("denied-update")
				method, reason = "conversations.setTopic", ujresource.ReasonAsyncUpdateFailure
			}
			// This HTTP boundary does not model Kubernetes resourceVersion
			// conflicts. Release the real Slack error after Reconcile returns so
			// its stale status snapshot cannot overwrite the async callback.
			responseGate := make(chan struct{})
			t.Cleanup(func() {
				select {
				case <-responseGate:
				default:
					close(responseGate)
				}
			})
			s.mu.Lock()
			s.apiErrors = map[string]string{method: "missing_scope"}
			s.gatedMethod, s.responseGate = method, responseGate
			s.mu.Unlock()
			k.put("/apis/conversation.slack.m.arsvivendi.io/v1alpha1/namespaces/team/conversations/async-denied", m)
			r := newAsyncReconciler(t, c, setup, store)
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(m)})
			close(responseGate)
			if err != nil {
				t.Fatal(err)
			}
			waitForAsyncCondition(t, c, store, m, corev1.ConditionFalse, reason)
			condition := m.GetCondition(xpv2.TypeSynced)
			if condition.Status != corev1.ConditionFalse || !strings.Contains(condition.Message, "missing_scope") || !strings.Contains(m.GetCondition(ujresource.TypeLastAsyncOperation).Message, "missing_scope") {
				t.Fatalf("production-style callback masked the permission failure: %v", m.Status.Conditions)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if operation == "create" {
				if meta.GetExternalName(m) != "" || m.Status.AtProvider.ID != nil || s.channel != nil || s.calls[method] != 1 || m.GetCondition(xpv2.TypeReady).Status == corev1.ConditionTrue {
					t.Fatalf("failed async creation reported a false identity/success: resource=%+v calls=%v", m, s.calls)
				}
			} else if meta.GetExternalName(m) != "C123" || s.channel.Topic.Value != "initial" || s.calls["conversations.create"] != 1 || s.calls[method] != 2 {
				t.Fatalf("failed async update changed or recreated the channel: calls=%v channel=%+v", s.calls, s.channel)
			}
		})
	}
}
