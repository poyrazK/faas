package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// fakeRealtimeEndpointClient serves realtime endpoints, consumer groups and
// the realtime trigger API for one app.
type fakeRealtimeEndpointClient struct {
	*fakeRealtimeTriggerClient
	endpoints map[string]api.ManagedRealtimeEndpointResponse // by ID
	groups    map[string][]string                            // endpoint ID + channel → names
	secrets   map[string]string                              // app/key → value
	restarts  []string
	nextID    int
	log       []string
}

func newFakeRealtimeEndpointClient(existing ...api.ManagedRealtimeEndpointResponse) *fakeRealtimeEndpointClient {
	f := &fakeRealtimeEndpointClient{fakeRealtimeTriggerClient: newFakeRealtimeTriggerClient(),
		endpoints: map[string]api.ManagedRealtimeEndpointResponse{}, groups: map[string][]string{}, secrets: map[string]string{}}
	for _, e := range existing {
		f.endpoints[e.ID] = e
	}
	return f
}

func (f *fakeRealtimeEndpointClient) GetApp(_ context.Context, slug string) (api.AppResponse, error) {
	return api.AppResponse{ID: "app-" + slug, Slug: slug, URL: "https://" + slug + ".gregale.app"}, nil
}

func (f *fakeRealtimeEndpointClient) ListManagedRealtimeEndpoints(context.Context, string) ([]api.ManagedRealtimeEndpointResponse, error) {
	out := make([]api.ManagedRealtimeEndpointResponse, 0, len(f.endpoints))
	for _, e := range f.endpoints {
		out = append(out, e)
	}
	return out, nil
}

func (f *fakeRealtimeEndpointClient) CreateManagedRealtimeEndpoint(_ context.Context, _ string, req api.CreateManagedRealtimeEndpointRequest) (api.ManagedRealtimeEndpointResponse, error) {
	f.nextID++
	e := api.ManagedRealtimeEndpointResponse{ID: fmt.Sprintf("00000000-0000-0000-0000-%012d", f.nextID), Slug: req.Slug,
		CallbackURL: req.CallbackURL, ConnectPath: req.ConnectPath, MessagePath: req.MessagePath, DisconnectPath: req.DisconnectPath,
		AuthMode: req.AuthMode, AuthIssuer: req.AuthIssuer, AuthJWKSURL: req.AuthJWKSURL, AuthAudience: req.AuthAudience,
		AuthAlgorithms: req.AuthAlgorithms, AllowedOrigins: req.AllowedOrigins, MaxConnections: req.MaxConnections, Enabled: *req.Enabled,
		ChannelRules: req.ChannelRules}
	f.endpoints[e.ID] = e
	f.log = append(f.log, "create "+req.Slug)
	return e, nil
}

func (f *fakeRealtimeEndpointClient) UpdateManagedRealtimeEndpoint(_ context.Context, _, id string, req api.UpdateManagedRealtimeEndpointRequest) (api.ManagedRealtimeEndpointResponse, error) {
	e := f.endpoints[id]
	if req.Enabled != nil {
		e.Enabled = *req.Enabled
	}
	if req.AllowedOrigins != nil {
		e.AllowedOrigins = *req.AllowedOrigins
	}
	if req.MaxConnections != nil {
		e.MaxConnections = *req.MaxConnections
	}
	if req.AuthMode != nil {
		e.AuthMode = *req.AuthMode
	}
	if req.ConnectPath != nil {
		e.ConnectPath, e.MessagePath, e.DisconnectPath = *req.ConnectPath, *req.MessagePath, *req.DisconnectPath
	}
	if req.ChannelRules != nil {
		e.ChannelRules = *req.ChannelRules
	}
	f.endpoints[id] = e
	f.log = append(f.log, "update "+e.Slug)
	return e, nil
}

func (f *fakeRealtimeEndpointClient) DeleteManagedRealtimeEndpoint(_ context.Context, _, id string) error {
	f.log = append(f.log, "delete "+f.endpoints[id].Slug)
	delete(f.endpoints, id)
	return nil
}

func (f *fakeRealtimeEndpointClient) ListRealtimeConsumerGroups(_ context.Context, _, id, channel string) ([]api.RealtimeConsumerGroup, error) {
	var out []api.RealtimeConsumerGroup
	for _, name := range f.groups[id+"/"+channel] {
		out = append(out, api.RealtimeConsumerGroup{Name: name, Channel: channel})
	}
	return out, nil
}

func (f *fakeRealtimeEndpointClient) CreateRealtimeConsumerGroup(_ context.Context, _, id, channel string, req api.RealtimeConsumerGroupCreateRequest) (api.RealtimeConsumerGroup, error) {
	f.groups[id+"/"+channel] = append(f.groups[id+"/"+channel], req.Name)
	f.log = append(f.log, "group "+channel+"/"+req.Name)
	return api.RealtimeConsumerGroup{Name: req.Name, Channel: channel}, nil
}

func (f *fakeRealtimeEndpointClient) DeleteRealtimeConsumerGroup(_ context.Context, _, id, channel, group string) error {
	f.groups[id+"/"+channel] = slices.DeleteFunc(f.groups[id+"/"+channel], func(n string) bool { return n == group })
	f.log = append(f.log, "ungroup "+channel+"/"+group)
	return nil
}

func (f *fakeRealtimeEndpointClient) SetSecretWithScope(_ context.Context, slug, key, value, _ string) error {
	f.secrets[slug+"/"+key] = value
	return nil
}

func (f *fakeRealtimeEndpointClient) UnsetSecretWithScope(_ context.Context, slug, key, _ string) error {
	delete(f.secrets, slug+"/"+key)
	return nil
}

func (f *fakeRealtimeEndpointClient) RestartAppFresh(_ context.Context, slug string) (api.AppRestartResponse, error) {
	f.restarts = append(f.restarts, slug)
	return api.AppRestartResponse{}, nil
}

func (f *fakeRealtimeEndpointClient) bySlug(slug string) (api.ManagedRealtimeEndpointResponse, bool) {
	for _, e := range f.endpoints {
		if e.Slug == slug {
			return e, true
		}
	}
	return api.ManagedRealtimeEndpointResponse{}, false
}

// adr: 20261019102657 — a deploy creates declared endpoints and groups,
// resolves triggers' endpoint slugs, and rolls all of it back.
func TestReconcileManifestRealtimeCreatesAndRollsBack(t *testing.T) {
	t.Setenv("FAAS_REALTIME_PREVIEW_ACCOUNTS", "*")
	client := newFakeRealtimeEndpointClient()
	m := &gregalemanifest.Manifest{
		Realtime: &gregalemanifest.RealtimeConfig{Endpoints: []gregalemanifest.RealtimeEndpoint{{
			Slug: "chat", Auth: gregalemanifest.RealtimeAuthNone, AllowedOrigins: []string{"https://shop.example.com"},
			Callbacks:      &gregalemanifest.RealtimeCallbacks{Message: "/rt/message"},
			ConsumerGroups: []gregalemanifest.RealtimeConsumerGroup{{Channel: "orders", Name: "billing"}},
		}}},
		Triggers: []gregalemanifest.Trigger{{Kind: gregalemanifest.TriggerKindRealtime, App: "shop", Slug: "orders",
			Config: map[string]any{"endpoint": "chat", "channel": "orders", "group": "billing"}}},
	}
	txn := &manifestCronTransaction{}
	if err := reconcileManifestRealtime(t.Context(), client, "shop", m, txn); err != nil {
		t.Fatal(err)
	}
	chat, ok := client.bySlug("chat")
	if !ok || chat.AuthMode != api.RealtimeAuthModeNone || chat.CallbackURL != "https://shop.gregale.app" ||
		chat.MessagePath != "/rt/message" || chat.ConnectPath != api.DefaultRealtimeConnectPath {
		t.Fatalf("endpoint = %+v", chat)
	}
	if client.secrets["shop/REALTIME_CHAT_CALLBACK_TOKEN"] == "" || !slices.Equal(client.restarts, []string{"shop"}) {
		t.Fatalf("secrets %v restarts %v, want the callback token on the app and a fresh restart", client.secrets, client.restarts)
	}
	if !slices.Equal(client.groups[chat.ID+"/orders"], []string{"billing"}) {
		t.Fatalf("groups = %v", client.groups)
	}
	var created api.Trigger
	for _, tr := range client.triggers {
		created = tr
	}
	if !strings.Contains(string(created.Config), `"endpoint_id":"`+chat.ID+`"`) {
		t.Fatalf("trigger config = %s, want the endpoint's ID", created.Config)
	}

	// A second deploy with nothing changed makes no calls.
	client.log = nil
	if err := reconcileManifestRealtimeEndpointsOnly(t, client, m); len(client.log) != 0 || err != nil {
		t.Fatalf("unchanged redeploy = %v, calls %v", err, client.log)
	}

	if err := txn.rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := client.bySlug("chat"); ok || len(client.secrets) != 0 {
		t.Fatalf("after rollback endpoints %v secrets %v, want both gone", client.endpoints, client.secrets)
	}
}

func reconcileManifestRealtimeEndpointsOnly(t *testing.T, client *fakeRealtimeEndpointClient, m *gregalemanifest.Manifest) error {
	t.Helper()
	_, err := reconcileManifestRealtimeEndpoints(t.Context(), client, "shop", m, &manifestCronTransaction{})
	return err
}

// adr: 20261019102657 — a changed endpoint is updated in place, an undeclared
// slugged endpoint is disabled (never deleted), and endpoints without a slug
// are left alone; rollback restores both.
func TestReconcileManifestRealtimeUpdatesAndDisables(t *testing.T) {
	t.Setenv("FAAS_REALTIME_PREVIEW_ACCOUNTS", "*")
	client := newFakeRealtimeEndpointClient(
		api.ManagedRealtimeEndpointResponse{ID: "e-chat", Slug: "chat", AuthMode: api.RealtimeAuthModeNone, Enabled: true,
			AllowedOrigins: []string{}, ConnectPath: api.DefaultRealtimeConnectPath, MessagePath: api.DefaultRealtimeMessagePath,
			DisconnectPath: api.DefaultRealtimeDisconnectPath, MaxConnections: 100},
		api.ManagedRealtimeEndpointResponse{ID: "e-old", Slug: "old", AuthMode: api.RealtimeAuthModeNone, Enabled: true},
		api.ManagedRealtimeEndpointResponse{ID: "e-api", AuthMode: api.RealtimeAuthModeNone, Enabled: true},
	)
	m := &gregalemanifest.Manifest{Realtime: &gregalemanifest.RealtimeConfig{Endpoints: []gregalemanifest.RealtimeEndpoint{
		{Slug: "chat", Auth: gregalemanifest.RealtimeAuthNone, MaxConnections: 500},
	}}}
	txn := &manifestCronTransaction{}
	ids, err := reconcileManifestRealtimeEndpoints(t.Context(), client, "shop", m, txn)
	if err != nil {
		t.Fatal(err)
	}
	if ids["chat"] != "e-chat" || client.endpoints["e-chat"].MaxConnections != 500 {
		t.Fatalf("ids %v chat %+v", ids, client.endpoints["e-chat"])
	}
	if client.endpoints["e-old"].Enabled || !client.endpoints["e-api"].Enabled {
		t.Fatalf("old enabled=%v api enabled=%v, want old disabled and the unslugged endpoint untouched",
			client.endpoints["e-old"].Enabled, client.endpoints["e-api"].Enabled)
	}
	if err := txn.rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !client.endpoints["e-old"].Enabled || client.endpoints["e-chat"].MaxConnections != 100 {
		t.Fatalf("after rollback old=%+v chat=%+v", client.endpoints["e-old"], client.endpoints["e-chat"])
	}
	if _, ok := client.endpoints["e-old"]; !ok {
		t.Fatal("an undeclared endpoint was deleted")
	}
}

// adr: 20261019102657 — `auth: <app>` names an Application Auth app, which
// stays unavailable while Auth is dark-launched.
func TestReconcileManifestRealtimeAuthAppNeedsApplicationAuth(t *testing.T) {
	t.Setenv("FAAS_REALTIME_PREVIEW_ACCOUNTS", "*")
	t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "")
	client := newFakeRealtimeEndpointClient()
	m := &gregalemanifest.Manifest{Realtime: &gregalemanifest.RealtimeConfig{Endpoints: []gregalemanifest.RealtimeEndpoint{
		{Slug: "chat", Auth: "shop-auth"},
	}}}
	if _, err := reconcileManifestRealtimeEndpoints(t.Context(), client, "shop", m, &manifestCronTransaction{}); err == nil {
		t.Fatal("an Auth app endpoint was created while Application Auth is off")
	}
	t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "1")
	if _, err := reconcileManifestRealtimeEndpoints(t.Context(), client, "shop", m, &manifestCronTransaction{}); err != nil {
		t.Fatal(err)
	}
	chat, _ := client.bySlug("chat")
	if chat.AuthMode != api.RealtimeAuthModeOIDCJWT || chat.CallbackURL != "https://shop-auth.gregale.app" ||
		chat.AuthIssuer != "https://shop-auth.gregale.app/auth/v1" || !slices.Equal(client.restarts, []string{"shop-auth"}) {
		t.Fatalf("endpoint %+v restarts %v", chat, client.restarts)
	}
}

// adr: 20261019102657 — declared channel rules reach the endpoint and a
// changed rule set is updated in place.
func TestReconcileManifestRealtimeChannelRules(t *testing.T) {
	t.Setenv("FAAS_REALTIME_PREVIEW_ACCOUNTS", "*")
	client := newFakeRealtimeEndpointClient()
	oidc := &gregalemanifest.RealtimeOIDC{Issuer: "https://id.example", JWKSURL: "https://id.example/jwks", Algorithms: []string{"RS256"}}
	m := &gregalemanifest.Manifest{Realtime: &gregalemanifest.RealtimeConfig{Endpoints: []gregalemanifest.RealtimeEndpoint{
		{Slug: "rooms", OIDC: oidc, Channels: []gregalemanifest.RealtimeChannelRule{{Pattern: "org:{org_id}:room:*", Read: true}}},
	}}}
	if _, err := reconcileManifestRealtimeEndpoints(t.Context(), client, "shop", m, &manifestCronTransaction{}); err != nil {
		t.Fatal(err)
	}
	rooms, _ := client.bySlug("rooms")
	if len(rooms.ChannelRules) != 1 || rooms.ChannelRules[0].Pattern != "org:{org_id}:room:*" {
		t.Fatalf("rules = %+v", rooms.ChannelRules)
	}
	m.Realtime.Endpoints[0].Channels = append(m.Realtime.Endpoints[0].Channels, gregalemanifest.RealtimeChannelRule{Pattern: "user:{sub}", Read: true})
	client.log = nil
	if _, err := reconcileManifestRealtimeEndpoints(t.Context(), client, "shop", m, &manifestCronTransaction{}); err != nil {
		t.Fatal(err)
	}
	rooms, _ = client.bySlug("rooms")
	if !slices.Equal(client.log, []string{"update rooms"}) || len(rooms.ChannelRules) != 2 {
		t.Fatalf("calls %v rules %+v, want one update with both rules", client.log, rooms.ChannelRules)
	}
}
