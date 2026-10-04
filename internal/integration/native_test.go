// These tests exercise real Upjet SDK Connect/Observe/Create/Update/Delete and
// real upstream callbacks against HTTP Slack and Kubernetes API simulators.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/meta"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	ujcontroller "github.com/crossplane/upjet/v2/pkg/controller"
	"github.com/crossplane/upjet/v2/pkg/terraform"
	jsonpatch "github.com/evanphx/json-patch/v5"
	upstreamslack "github.com/pablovarela/terraform-provider-slack/slack"
	slackapi "github.com/slack-go/slack"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	apis "github.com/ars-vivendi/terraform-provider-slack/apis/namespaced"
	conversation "github.com/ars-vivendi/terraform-provider-slack/apis/namespaced/conversation/v1alpha1"
	"github.com/ars-vivendi/terraform-provider-slack/apis/namespaced/v1beta1"
	"github.com/ars-vivendi/terraform-provider-slack/config"
	"github.com/ars-vivendi/terraform-provider-slack/internal/clients"
)

type slackSimulator struct {
	mu                        sync.Mutex
	channel                   *slackapi.Channel
	members                   map[string]bool
	calls                     map[string]int
	tokens                    []string
	memberPages, channelPages int
	apiErrors                 map[string]string
	// An optional response barrier models async HTTP completion after the
	// initiating reconcile's status write, without sleeps or callback changes.
	gatedMethod  string
	responseGate <-chan struct{}
}

func (s *slackSimulator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	method := strings.TrimPrefix(r.URL.Path, "/api/")
	s.calls[method]++
	s.tokens = append(s.tokens, r.Form.Get("token"))
	response := map[string]any{"ok": true}
	apiError := s.apiErrors[method]
	if s.channel != nil && s.channel.IsArchived {
		switch method {
		case "conversations.setTopic", "conversations.setPurpose", "conversations.rename", "conversations.join", "conversations.invite", "conversations.kick":
			apiError = "is_archived"
		}
	}
	if apiError != "" {
		if method == s.gatedMethod && s.responseGate != nil {
			<-s.responseGate
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": apiError}); err != nil {
			panic(err)
		}
		return
	}
	switch method {
	case "conversations.create":
		if s.channel != nil {
			response = map[string]any{"ok": false, "error": "name_taken"}
		} else {
			s.channel = &slackapi.Channel{GroupConversation: slackapi.GroupConversation{Conversation: slackapi.Conversation{ID: "C123", Created: 123}, Creator: "Ucreator", Name: r.Form.Get("name")}}
			s.members = map[string]bool{"Ucreator": true, "Uapi": true}
			response["channel"] = s.channel
		}
	case "conversations.info":
		if s.channel == nil || r.Form.Get("channel") != s.channel.ID {
			response = map[string]any{"ok": false, "error": "channel_not_found"}
		} else {
			response["channel"] = s.channel
		}
	case "conversations.list":
		s.channelPages++
		if r.Form.Get("cursor") == "" {
			response["channels"] = []any{}
			response["response_metadata"] = map[string]any{"next_cursor": "channel-page-2"}
		} else {
			response["channels"] = []*slackapi.Channel{s.channel}
			response["response_metadata"] = map[string]any{"next_cursor": ""}
		}
	case "conversations.members":
		s.memberPages++
		members := make([]string, 0, len(s.members))
		for m := range s.members {
			members = append(members, m)
		}
		sort.Strings(members)
		if r.Form.Get("cursor") == "" && len(members) > 1 {
			response["members"] = members[:1]
			response["response_metadata"] = map[string]any{"next_cursor": "members-page-2"}
		} else {
			if r.Form.Get("cursor") != "" {
				members = members[1:]
			}
			response["members"] = members
			response["response_metadata"] = map[string]any{"next_cursor": ""}
		}
	case "auth.test":
		response["user_id"] = "Uapi"
		response["team_id"] = "T123"
	case "conversations.join":
		s.members["Uapi"] = true
		response["channel"] = s.channel
	case "conversations.invite":
		for _, m := range strings.Split(r.Form.Get("users"), ",") {
			if m != "" {
				s.members[m] = true
			}
		}
		response["channel"] = s.channel
	case "conversations.kick":
		delete(s.members, r.Form.Get("user"))
	case "conversations.setTopic":
		s.channel.Topic.Value = r.Form.Get("topic")
		response["channel"] = s.channel
	case "conversations.setPurpose":
		s.channel.Purpose.Value = r.Form.Get("purpose")
		response["channel"] = s.channel
	case "conversations.rename":
		s.channel.Name = r.Form.Get("name")
		response["channel"] = s.channel
	case "conversations.archive":
		s.channel.IsArchived = true
	case "conversations.unarchive":
		s.channel.IsArchived = false
	default:
		http.Error(w, "unsupported Slack method: "+method, 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		panic(err)
	}
}

type kubeSimulator struct {
	mu      sync.Mutex
	objects map[string]json.RawMessage
	codecs  serializer.CodecFactory
}

func (k *kubeSimulator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	k.mu.Lock()
	defer k.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimSuffix(r.URL.Path, "/status")
	switch r.Method {
	case http.MethodGet:
		b, ok := k.objects[path]
		if !ok {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: "Failure", Message: "object not found: " + path, Reason: metav1.StatusReasonNotFound, Code: 404})
			return
		}
		k.respond(w, r, b)
	case http.MethodPost, http.MethodPut:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if strings.Contains(r.Header.Get("Content-Type"), runtime.ContentTypeProtobuf) {
			obj, _, err := k.codecs.UniversalDeserializer().Decode(body, nil, nil)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			body, err = json.Marshal(obj)
			if err != nil {
				panic(err)
			}
		}
		var obj map[string]any
		if err := json.Unmarshal(body, &obj); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if r.Method == http.MethodPost {
			path += "/" + obj["metadata"].(map[string]any)["name"].(string)
		}
		if strings.HasSuffix(r.URL.Path, "/status") {
			// Like a real API server, status writes must not replace spec or
			// annotations read before a concurrent asynchronous operation.
			var current map[string]any
			if err := json.Unmarshal(k.objects[path], &current); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			current["status"] = obj["status"]
			obj = current
		}
		b, err := json.Marshal(obj)
		if err != nil {
			panic(err)
		}
		k.objects[path] = b
		k.respond(w, r, b)
	case http.MethodPatch:
		patch, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		b, err := jsonpatch.MergePatch(k.objects[path], patch)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		k.objects[path] = b
		k.respond(w, r, b)
	default:
		http.Error(w, "unsupported Kubernetes method: "+r.Method, 500)
	}
}

func (k *kubeSimulator) respond(w http.ResponseWriter, r *http.Request, b []byte) {
	if strings.Contains(r.URL.Path, "/secrets") && strings.Contains(r.Header.Get("Accept"), runtime.ContentTypeProtobuf) {
		obj := &corev1.Secret{}
		if err := json.Unmarshal(b, obj); err != nil {
			panic(err)
		}
		info, ok := runtime.SerializerInfoForMediaType(k.codecs.SupportedMediaTypes(), runtime.ContentTypeProtobuf)
		if !ok {
			panic("missing protobuf serializer")
		}
		w.Header().Set("Content-Type", runtime.ContentTypeProtobuf)
		if err := info.Serializer.Encode(obj, w); err != nil {
			panic(err)
		}
		return
	}
	_, _ = w.Write(b)
}

func TestManagedReconcilerPublishesChannelID(t *testing.T) {
	ctx := context.Background()
	_, k, c, setup := newBoundary(t)
	m := newConversation("reconciled", "uid-reconciled")
	m.SetWriteConnectionSecretToReference(&xpv2.LocalSecretReference{Name: "channel-output"})
	k.put("/apis/conversation.slack.m.arsvivendi.io/v1alpha1/namespaces/team/conversations/reconciled", m)
	// Use a real controller-runtime manager and HTTP client. The manager is not
	// started because reconciliation is driven synchronously by this test.
	mgr, err := ctrl.NewManager(&rest.Config{Host: "http://unused.invalid"}, ctrl.Options{
		Scheme: c.Scheme(), Metrics: metricsserver.Options{BindAddress: "0"},
		MapperProvider: func(_ *rest.Config, _ *http.Client) (apimeta.RESTMapper, error) { return c.RESTMapper(), nil },
		NewClient:      func(_ *rest.Config, _ client.Options) (client.Client, error) { return c, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := logging.NewNopLogger()
	connector := ujcontroller.NewTerraformPluginSDKConnector(c, setup, config.GetProvider().Resources["slack_conversation"], ujcontroller.NewOperationStore(logger), ujcontroller.WithTerraformPluginSDKLogger(logger))
	// Match the generated controller's initializer chain: Slack generates IDs,
	// so Kubernetes metadata.name must never become the external identity.
	r := managed.NewReconciler(mgr, resource.ManagedKind(conversation.Conversation_GroupVersionKind), managed.WithExternalConnecter(connector), managed.WithLogger(logger), managed.WithInitializers(managed.InitializerChain{}))
	for i := 0; i < 6; i++ {
		result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: m.Name, Namespace: m.Namespace}})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(m), m); err != nil {
			t.Fatal(err)
		}
		t.Logf("reconcile %d: result=%v conditions=%v annotations=%v", i, result, m.Status.Conditions, m.Annotations)
	}
	secret := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Name: "channel-output", Namespace: "team"}, secret); err != nil {
		t.Fatal(err)
	}
	if string(secret.Data["channel_id"]) != "C123" {
		t.Fatalf("incorrect published Secret: %v", secret.Data)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(m), m); err != nil {
		t.Fatal(err)
	}
	if meta.GetExternalName(m) != "C123" || m.GetCondition(xpv2.TypeReady).Status != corev1.ConditionTrue || m.GetCondition(xpv2.TypeSynced).Status != corev1.ConditionTrue {
		t.Fatalf("managed reconciliation not ready/synced: annotations=%v conditions=%v", m.Annotations, m.Status.Conditions)
	}
}

func TestAsyncNativeCallbacksAndSecretOutput(t *testing.T) {
	ctx := context.Background()
	_, k, c, setup := newBoundary(t)
	m := newConversation("async", "uid-async")
	m.SetWriteConnectionSecretToReference(&xpv2.LocalSecretReference{Name: "async-output"})
	k.put("/apis/conversation.slack.m.arsvivendi.io/v1alpha1/namespaces/team/conversations/async", m)
	mgr, err := ctrl.NewManager(&rest.Config{Host: "http://unused.invalid"}, ctrl.Options{
		Scheme: c.Scheme(), Metrics: metricsserver.Options{BindAddress: "0"},
		MapperProvider: func(_ *rest.Config, _ *http.Client) (apimeta.RESTMapper, error) { return c.RESTMapper(), nil },
		NewClient:      func(_ *rest.Config, _ client.Options) (client.Client, error) { return c, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := logging.NewNopLogger()
	store := ujcontroller.NewOperationStore(logger)
	callbacks := ujcontroller.NewAPICallbacks(mgr, resource.ManagedKind(conversation.Conversation_GroupVersionKind), ujcontroller.WithStatusUpdates(false))
	connector := ujcontroller.NewTerraformPluginSDKAsyncConnector(c, store, setup, config.GetProvider().Resources["slack_conversation"],
		ujcontroller.WithTerraformPluginSDKAsyncLogger(logger), ujcontroller.WithTerraformPluginSDKAsyncCallbackProvider(callbacks), ujcontroller.WithTerraformPluginSDKAsyncManagementPolicies(true))
	r := managed.NewReconciler(mgr, resource.ManagedKind(conversation.Conversation_GroupVersionKind), managed.WithExternalConnecter(connector), managed.WithLogger(logger),
		managed.WithInitializers(managed.InitializerChain{}), managed.WithManagementPolicies(),
		managed.WithFinalizer(ujcontroller.NewOperationTrackerFinalizer(store, resource.NewAPIFinalizer(c, managed.FinalizerName))))
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(m)}
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}
	// Drive the real asynchronous callback to completion before the next manual
	// reconciliation. Watch/event delivery and real API concurrency remain
	// disposable-cluster acceptance checks, not claims of this boundary test.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := c.Get(ctx, request.NamespacedName, m); err != nil {
			t.Fatal(err)
		}
		finished := false
		for _, condition := range m.Status.Conditions {
			if condition.Type == "LastAsyncOperation" && condition.Status == corev1.ConditionTrue {
				finished = true
			}
		}
		if finished && !store.Tracker(m).LastOperation.IsRunning() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("async operation/callback did not complete: %v", m.Status.Conditions)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, request.NamespacedName, m); err != nil {
		t.Fatal(err)
	}
	secret := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: m.Namespace, Name: "async-output"}, secret); err != nil {
		t.Fatal(err)
	}
	if meta.GetExternalName(m) != "C123" || string(secret.Data["channel_id"]) != "C123" || m.GetCondition(xpv2.TypeReady).Status != corev1.ConditionTrue {
		t.Fatalf("async create did not converge with output: conditions=%v annotations=%v data=%v", m.Status.Conditions, m.Annotations, secret.Data)
	}
}

func TestArchivedExternalIDRestoreRetainsUpstreamOrderingError(t *testing.T) {
	s, _, c, setup := newBoundary(t)
	s.mu.Lock()
	s.channel = &slackapi.Channel{GroupConversation: slackapi.GroupConversation{Conversation: slackapi.Conversation{ID: "C123"}, Creator: "Ucreator", Name: "managed-channel", IsArchived: true}}
	s.members = map[string]bool{"Ucreator": true}
	s.mu.Unlock()
	m := newConversation("archived-import", "uid-archived-import")
	meta.SetExternalName(m, "C123")
	logger := logging.NewNopLogger()
	e, err := ujcontroller.NewTerraformPluginSDKConnector(c, setup, config.GetProvider().Resources["slack_conversation"], ujcontroller.NewOperationStore(logger), ujcontroller.WithTerraformPluginSDKLogger(logger)).Connect(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Observe(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Update(context.Background(), m); err == nil || !strings.Contains(err.Error(), "is_archived") {
		t.Fatalf("expected upstream setTopic-before-unarchive error, got %v", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.channel.IsArchived || s.calls["conversations.unarchive"] != 0 {
		t.Fatal("upstream lifecycle ordering was bypassed")
	}
}

func TestSlackPermissionErrorIsNotReportedAsSuccess(t *testing.T) {
	s, _, c, setup := newBoundary(t)
	s.mu.Lock()
	s.apiErrors = map[string]string{"conversations.create": "missing_scope"}
	s.mu.Unlock()
	m := newConversation("denied", "uid-denied")
	logger := logging.NewNopLogger()
	e, err := ujcontroller.NewTerraformPluginSDKConnector(c, setup, config.GetProvider().Resources["slack_conversation"], ujcontroller.NewOperationStore(logger), ujcontroller.WithTerraformPluginSDKLogger(logger)).Connect(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Observe(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Create(context.Background(), m); err == nil || !strings.Contains(err.Error(), "missing_scope") {
		t.Fatalf("Slack permission failure masked: %v", err)
	}
	if meta.GetExternalName(m) != "" || m.Status.AtProvider.ID != nil {
		t.Fatal("failed create produced a false channel ID")
	}
}

func TestUpstreamDefaultsAndDeleteArchive(t *testing.T) {
	ctx := context.Background()
	s, _, c, setup := newBoundary(t)
	m := newConversation("defaults", "uid-defaults")
	m.Spec.ForProvider.ActionOnDestroy = nil
	m.Spec.ForProvider.ActionOnUpdatePermanentMembers = nil
	m.Spec.ForProvider.AdoptExistingChannel = nil
	m.Spec.ForProvider.IsArchived = nil
	m.Spec.ForProvider.IsPrivate = nil
	logger := logging.NewNopLogger()
	store := ujcontroller.NewOperationStore(logger)
	connector := ujcontroller.NewTerraformPluginSDKConnector(c, setup, config.GetProvider().Resources["slack_conversation"], store, ujcontroller.WithTerraformPluginSDKLogger(logger))
	e, err := connector.Connect(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Observe(ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	e, err = connector.Connect(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Observe(ctx, m); err != nil {
		t.Fatal(err)
	}
	if m.Spec.ForProvider.ActionOnDestroy == nil || *m.Spec.ForProvider.ActionOnDestroy != "archive" || m.Spec.ForProvider.ActionOnUpdatePermanentMembers == nil || *m.Spec.ForProvider.ActionOnUpdatePermanentMembers != "kick" {
		t.Fatalf("upstream defaults not late initialized: %+v", m.Spec.ForProvider)
	}
	if _, err := e.Delete(ctx, m); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.channel.IsArchived {
		t.Fatal("default delete did not archive")
	}
}

func TestPartialUpstreamCreateFailureRetainsRecoveryRequirement(t *testing.T) {
	ctx := context.Background()
	s, _, c, setup := newBoundary(t)
	s.mu.Lock()
	s.apiErrors = map[string]string{"conversations.setTopic": "missing_scope"}
	s.mu.Unlock()
	m := newConversation("partial", "uid-partial")
	logger := logging.NewNopLogger()
	store := ujcontroller.NewOperationStore(logger)
	connector := ujcontroller.NewTerraformPluginSDKConnector(c, setup, config.GetProvider().Resources["slack_conversation"], store, ujcontroller.WithTerraformPluginSDKLogger(logger))
	e, err := connector.Connect(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Observe(ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Create(ctx, m); err == nil || !strings.Contains(err.Error(), "missing_scope") {
		t.Fatalf("partial failure masked: %v", err)
	}
	s.mu.Lock()
	if s.channel == nil || s.channel.ID != "C123" {
		t.Fatal("expected Slack create to precede topic failure")
	}
	delete(s.apiErrors, "conversations.setTopic")
	s.mu.Unlock()
	// Pinned upstream sets d.SetId only after membership/topic/purpose writes.
	// A failed post-create write therefore loses identity even though Slack
	// created the channel. Keep that failure visible rather than invent an ID.
	if meta.GetExternalName(m) != "" {
		t.Fatal("partial create invented an external identity")
	}
	// After permission correction the explicit adoption policy recovers by name.
	e, err = connector.Connect(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Observe(ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	if meta.GetExternalName(m) != "C123" {
		t.Fatal("partial creation recovery did not retain ID")
	}
}

func (k *kubeSimulator) put(path string, obj any) {
	k.mu.Lock()
	defer k.mu.Unlock()
	b, err := json.Marshal(obj)
	if err != nil {
		panic(err)
	}
	k.objects[path] = b
}

func newBoundary(t *testing.T) (*slackSimulator, *kubeSimulator, client.Client, terraform.SetupFn) {
	t.Helper()
	s := &slackSimulator{calls: map[string]int{}}
	ss := httptest.NewServer(s)
	t.Cleanup(ss.Close)
	k := &kubeSimulator{objects: map[string]json.RawMessage{}}
	ks := httptest.NewServer(k)
	t.Cleanup(ks.Close)
	sch := runtime.NewScheme()
	if err := apis.AddToScheme(sch); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(sch); err != nil {
		t.Fatal(err)
	}
	k.codecs = serializer.NewCodecFactory(sch)
	mapper := apimeta.NewDefaultRESTMapper([]schema.GroupVersion{corev1.SchemeGroupVersion, v1beta1.SchemeGroupVersion, conversation.CRDGroupVersion})
	mapper.Add(corev1.SchemeGroupVersion.WithKind("Secret"), apimeta.RESTScopeNamespace)
	mapper.Add(v1beta1.ProviderConfigGroupVersionKind, apimeta.RESTScopeNamespace)
	mapper.Add(v1beta1.ClusterProviderConfigGroupVersionKind, apimeta.RESTScopeRoot)
	mapper.Add(v1beta1.ProviderConfigUsageGroupVersionKind, apimeta.RESTScopeNamespace)
	mapper.Add(conversation.Conversation_GroupVersionKind, apimeta.RESTScopeNamespace)
	c, err := client.New(&rest.Config{Host: ks.URL}, client.Options{Scheme: sch, Mapper: mapper})
	if err != nil {
		t.Fatal(err)
	}
	pc := &v1beta1.ProviderConfig{TypeMeta: metav1.TypeMeta{APIVersion: v1beta1.SchemeGroupVersion.String(), Kind: "ProviderConfig"}, ObjectMeta: metav1.ObjectMeta{Name: "workspace", Namespace: "team"}, Spec: v1beta1.ProviderConfigSpec{Credentials: v1beta1.ProviderCredentials{Source: xpv2.CredentialsSourceSecret, SecretRef: &v1beta1.CredentialSecretReference{Name: "slack", Namespace: "must-not-use", Key: "credentials"}}}}
	k.put("/apis/slack.m.arsvivendi.io/v1beta1/namespaces/team/providerconfigs/workspace", pc)
	rotate := func(token string) {
		k.put("/api/v1/namespaces/team/secrets/slack", &corev1.Secret{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"}, ObjectMeta: metav1.ObjectMeta{Name: "slack", Namespace: "team"}, Data: map[string][]byte{"credentials": []byte(fmt.Sprintf(`{"token":%q}`, token))}})
	}
	rotate("xoxp-initial")
	setup := func(ctx context.Context, c client.Client, mg resource.Managed) (terraform.Setup, error) {
		ts, err := clients.TerraformSetupBuilder(true)(ctx, c, mg)
		if err != nil {
			return ts, err
		}
		// Configure the real Slack client's supported HTTP options for the simulator.
		// Upstream Meta, provider validation and resource callbacks remain real.
		ts.Meta.(*upstreamslack.Meta).Client = slackapi.New(ts.Configuration["token"].(string), slackapi.OptionAPIURL(ss.URL+"/api/"), slackapi.OptionHTTPClient(ss.Client()))
		return ts, nil
	}
	return s, k, c, setup
}

func newConversation(name, uid string) *conversation.Conversation {
	m := &conversation.Conversation{TypeMeta: metav1.TypeMeta{APIVersion: conversation.CRDGroupVersion.String(), Kind: "Conversation"}, ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "team", UID: types.UID(uid)}, Spec: conversation.ConversationSpec{ForProvider: conversation.ConversationParameters{Name: ptr.To("managed-channel"), Topic: ptr.To("initial"), Purpose: ptr.To("service ownership"), PermanentMembers: []*string{ptr.To("Uone"), ptr.To("Utwo")}, ActionOnDestroy: ptr.To("archive"), ActionOnUpdatePermanentMembers: ptr.To("kick"), AdoptExistingChannel: ptr.To(true), IsArchived: ptr.To(false), IsPrivate: ptr.To(false)}}}
	m.SetProviderConfigReference(&xpv2.ProviderConfigReference{Kind: "ProviderConfig", Name: "workspace"})
	m.SetManagementPolicies(xpv2.ManagementPolicies{xpv2.ManagementActionAll})
	return m
}

func TestNativeLifecycle(t *testing.T) {
	ctx := context.Background()
	s, k, c, setup := newBoundary(t)
	cfg := config.GetProvider().Resources["slack_conversation"]
	m := newConversation("channel", "uid-one")
	logger := logging.NewNopLogger()
	store := ujcontroller.NewOperationStore(logger)
	connect := func(m *conversation.Conversation) managed.ExternalClient {
		t.Helper()
		e, err := ujcontroller.NewTerraformPluginSDKConnector(c, setup, cfg, store, ujcontroller.WithTerraformPluginSDKLogger(logger)).Connect(ctx, m)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	observe := func(e managed.ExternalClient, m *conversation.Conversation) managed.ExternalObservation {
		t.Helper()
		o, err := e.Observe(ctx, m)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	e := connect(m)
	if observe(e, m).ResourceExists {
		t.Fatal("unexpected existing channel")
	}
	created, err := e.Create(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if string(created.ConnectionDetails["channel_id"]) != "C123" || meta.GetExternalName(m) != "C123" {
		t.Fatalf("incorrect create outputs: %#v annotations=%v", created, m.Annotations)
	}
	e = connect(m)
	o := observe(e, m)
	if !o.ResourceExists || !o.ResourceUpToDate || string(o.ConnectionDetails["channel_id"]) != "C123" {
		t.Fatalf("not converged after create: %#v", o)
	}
	if m.Status.AtProvider.ID == nil || *m.Status.AtProvider.ID != "C123" {
		t.Fatal("missing typed ID observation")
	}
	m.Spec.ForProvider.Topic = ptr.To("updated")
	m.Spec.ForProvider.Name = ptr.To("renamed-channel")
	m.Spec.ForProvider.PermanentMembers = []*string{ptr.To("Uone")}
	e = connect(m)
	if observe(e, m).ResourceUpToDate {
		t.Fatal("update not detected")
	}
	if _, err := e.Update(ctx, m); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	if s.members["Utwo"] || !s.members["Uone"] || !s.members["Uapi"] || !s.members["Ucreator"] || s.channel.Topic.Value != "updated" || s.channel.Name != "renamed-channel" {
		t.Fatal("upstream update/removal semantics not preserved")
	}
	delete(s.members, "Uone")
	s.mu.Unlock()
	e = connect(m)
	if observe(e, m).ResourceUpToDate {
		t.Fatal("missing managed member not detected")
	}
	if _, err := e.Update(ctx, m); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	recovered := s.members["Uone"]
	s.members["Uorganic"] = true
	s.mu.Unlock()
	if !recovered {
		t.Fatal("managed member not recovered")
	}
	e = connect(m)
	if !observe(e, m).ResourceUpToDate {
		t.Fatal("organic membership must not trigger false drift")
	}
	// Rotation crosses the real Secret extraction and upstream configuration boundary.
	k.put("/api/v1/namespaces/team/secrets/slack", &corev1.Secret{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"}, ObjectMeta: metav1.ObjectMeta{Name: "slack", Namespace: "team"}, Data: map[string][]byte{"credentials": []byte(`{"token":"xoxp-rotated"}`)}})
	e = connect(m)
	observe(e, m)
	s.mu.Lock()
	lastToken := s.tokens[len(s.tokens)-1]
	s.mu.Unlock()
	if lastToken != "xoxp-rotated" {
		t.Fatalf("rotation not used: %q", lastToken)
	}
	// Fresh operation store simulates restart with only persisted Kubernetes data.
	store = ujcontroller.NewOperationStore(logger)
	restarted := m.DeepCopy()
	e = connect(restarted)
	o = observe(e, restarted)
	if !o.ResourceExists || !o.ResourceUpToDate {
		t.Fatalf("restart failed: %#v", o)
	}
	s.mu.Lock()
	creates := s.calls["conversations.create"]
	s.mu.Unlock()
	if creates != 1 {
		t.Fatal("restart created duplicate channel")
	}
	// External ID alone is sufficient to reconstruct Terraform state.
	store = ujcontroller.NewOperationStore(logger)
	imported := m.DeepCopy()
	imported.Status = conversation.ConversationStatus{}
	imported.Annotations = map[string]string{"crossplane.io/external-name": "C123"}
	e = connect(imported)
	if !observe(e, imported).ResourceExists {
		t.Fatal("external ID alone did not reconstruct state")
	}
	if _, err := e.Delete(ctx, restarted); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	archived := s.channel.IsArchived
	s.mu.Unlock()
	if !archived {
		t.Fatal("delete did not archive")
	}
	// New declaration with no external ID adopts by name, paginates and restores.
	store = ujcontroller.NewOperationStore(logger)
	restored := newConversation("restored", "uid-restored")
	restored.Spec.ForProvider.Name = ptr.To("renamed-channel")
	e = connect(restored)
	if observe(e, restored).ResourceExists {
		t.Fatal("new declaration unexpectedly had state")
	}
	if _, err := e.Create(ctx, restored); err != nil {
		t.Fatal(err)
	}
	if meta.GetExternalName(restored) != "C123" {
		t.Fatal("adoption changed Slack ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.channel.IsArchived || s.calls["conversations.unarchive"] != 1 || s.channelPages != 2 || s.memberPages < 2 {
		t.Fatalf("restoration/pagination incomplete: calls=%v channelPages=%d memberPages=%d", s.calls, s.channelPages, s.memberPages)
	}
}

func TestNativeUpstreamValidation(t *testing.T) {
	s, _, c, setup := newBoundary(t)
	ctx := context.Background()
	logger := logging.NewNopLogger()
	cfg := config.GetProvider().Resources["slack_conversation"]
	for _, field := range []string{"action_on_destroy", "action_on_update_permanent_members", "name"} {
		t.Run(field, func(t *testing.T) {
			m := newConversation("invalid", "uid-invalid")
			switch field {
			case "action_on_destroy":
				m.Spec.ForProvider.ActionOnDestroy = ptr.To("delete")
			case "action_on_update_permanent_members":
				m.Spec.ForProvider.ActionOnUpdatePermanentMembers = ptr.To("ignore")
			case "name":
				m.Spec.ForProvider.Name = nil
			}
			// Complete writable configuration is validated during Connect, before
			// observation or any lifecycle write can reach Slack.
			_, err := ujcontroller.NewTerraformPluginSDKConnector(c, setup, cfg, ujcontroller.NewOperationStore(logger), ujcontroller.WithTerraformPluginSDKLogger(logger), ujcontroller.WithTerraformPluginSDKManagementPolicies(true)).Connect(ctx, m)
			if err == nil || !strings.Contains(err.Error(), "validation failed") {
				t.Fatalf("expected real upstream validation error, got %v", err)
			}
		})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) != 0 {
		t.Fatalf("invalid write configuration reached Slack APIs: %v", s.calls)
	}
}

func TestObserveOnlyExternalIDWithoutWriteParameters(t *testing.T) {
	s, _, c, setup := newBoundary(t)
	s.mu.Lock()
	s.channel = &slackapi.Channel{GroupConversation: slackapi.GroupConversation{Conversation: slackapi.Conversation{ID: "C123", Created: 123}, Creator: "Ucreator", Name: "adopted"}}
	s.members = map[string]bool{"Ucreator": true}
	s.mu.Unlock()
	m := newConversation("observed", "uid-observed")
	m.Spec.ForProvider = conversation.ConversationParameters{}
	m.SetManagementPolicies(xpv2.ManagementPolicies{xpv2.ManagementActionObserve})
	meta.SetExternalName(m, "C123")
	logger := logging.NewNopLogger()
	e, err := ujcontroller.NewTerraformPluginSDKConnector(c, setup, config.GetProvider().Resources["slack_conversation"], ujcontroller.NewOperationStore(logger), ujcontroller.WithTerraformPluginSDKLogger(logger), ujcontroller.WithTerraformPluginSDKManagementPolicies(true)).Connect(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	o, err := e.Observe(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if !o.ResourceExists || string(o.ConnectionDetails["channel_id"]) != "C123" || m.Status.AtProvider.ID == nil || *m.Status.AtProvider.ID != "C123" {
		t.Fatalf("observation-only import failed: %#v", o)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) != 1 || s.calls["conversations.info"] != 1 {
		t.Fatalf("observation-only import must not call mutating APIs: %v", s.calls)
	}
}

func TestProviderConfigScopes(t *testing.T) {
	_, k, c, _ := newBoundary(t)
	ctx := context.Background()
	m := newConversation("scopes", "uid-scopes")
	setup := clients.TerraformSetupBuilder(true)
	// Cluster configs resolve their explicitly selected namespace.
	cpc := &v1beta1.ClusterProviderConfig{TypeMeta: metav1.TypeMeta{APIVersion: v1beta1.SchemeGroupVersion.String(), Kind: "ClusterProviderConfig"}, ObjectMeta: metav1.ObjectMeta{Name: "default"}, Spec: v1beta1.ProviderConfigSpec{Credentials: v1beta1.ProviderCredentials{Source: xpv2.CredentialsSourceSecret, SecretRef: &v1beta1.CredentialSecretReference{Name: "central", Namespace: "provider", Key: "credentials"}}}}
	k.put("/apis/slack.m.arsvivendi.io/v1beta1/clusterproviderconfigs/default", cpc)
	k.put("/api/v1/namespaces/provider/secrets/central", &corev1.Secret{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"}, ObjectMeta: metav1.ObjectMeta{Name: "central", Namespace: "provider"}, Data: map[string][]byte{"credentials": []byte(`{"token":"xoxp-central"}`)}})
	m.SetProviderConfigReference(nil)
	ts, err := setup(ctx, c, m)
	if err != nil || ts.Configuration["token"] != "xoxp-central" {
		t.Fatalf("default cluster config: configuration=%v err=%v", ts.Configuration, err)
	}
	cpc.Spec.Credentials.SecretRef.Namespace = ""
	k.put("/apis/slack.m.arsvivendi.io/v1beta1/clusterproviderconfigs/default", cpc)
	if _, err := setup(ctx, c, m); err == nil {
		t.Fatal("cluster secret without namespace accepted")
	}
	m.SetProviderConfigReference(&xpv2.ProviderConfigReference{Kind: "Secret", Name: "workspace"})
	if _, err := setup(ctx, c, m); err == nil {
		t.Fatal("invalid provider config kind accepted")
	}
	m.SetProviderConfigReference(&xpv2.ProviderConfigReference{Kind: "ProviderConfig", Name: "workspace"})
	m.Namespace = "other-team"
	if _, err := setup(ctx, c, m); err == nil {
		t.Fatal("namespaced provider config leaked across namespace")
	}
	// Missing key and malformed rotated credentials fail closed.
	m.Namespace = "team"
	for _, data := range []map[string][]byte{{}, {"credentials": []byte(`{"token":"xoxb-bot"}`)}, {"credentials": []byte(`bad-json`)}} {
		k.put("/api/v1/namespaces/team/secrets/slack", &corev1.Secret{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"}, ObjectMeta: metav1.ObjectMeta{Name: "slack", Namespace: "team"}, Data: data})
		if _, err := setup(ctx, c, m); err == nil {
			t.Fatal("invalid rotated credential accepted")
		}
	}
}
