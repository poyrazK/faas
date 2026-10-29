package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 20261019102657 — endpoint slugs are optional, unique within an app,
// and can be renamed or cleared.
func TestManagedRealtimeEndpointSlug(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).WithRealtimePreview(apidserver.RealtimePreviewEveryone())
	t.Cleanup(apidserver.WithTestRecipient(t))
	apidserver.MustSeedApp(t, e, "rt-slugs")
	create := func(slug string) (int, api.ManagedRealtimeEndpointResponse) {
		t.Helper()
		rec := e.Do(t, http.MethodPost, "/v1/apps/rt-slugs/realtime/endpoints", api.CreateManagedRealtimeEndpointRequest{
			CallbackURL: "https://example.com/callback", CallbackAuthToken: "callback-secret", Slug: slug,
		}, nil)
		var out api.ManagedRealtimeEndpointResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	status, chat := create("chat")
	if status != http.StatusCreated || chat.Slug != "chat" {
		t.Fatalf("create with slug = %d %+v", status, chat)
	}
	if status, plain := create(""); status != http.StatusCreated || plain.Slug != "" {
		t.Fatalf("create without slug = %d %+v", status, plain)
	}
	for _, slug := range []string{"chat", "Chat", "-chat", "a_b"} {
		if status, _ := create(slug); status != http.StatusBadRequest {
			t.Fatalf("create with slug %q = %d, want 400", slug, status)
		}
	}
	path := "/v1/apps/rt-slugs/realtime/endpoints/" + chat.ID
	patch := func(slug string) (int, api.ManagedRealtimeEndpointResponse) {
		t.Helper()
		rec := e.Do(t, http.MethodPatch, path, api.UpdateManagedRealtimeEndpointRequest{Slug: &slug}, nil)
		var out api.ManagedRealtimeEndpointResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if status, renamed := patch("lobby"); status != http.StatusOK || renamed.Slug != "lobby" {
		t.Fatalf("rename = %d %+v", status, renamed)
	}
	if status, _ := create("chat"); status != http.StatusCreated {
		t.Fatalf("reusing a released slug = %d", status)
	}
	if status, _ := patch("chat"); status != http.StatusBadRequest {
		t.Fatalf("renaming onto a taken slug = %d, want 400", status)
	}
	if status, cleared := patch(""); status != http.StatusOK || cleared.Slug != "" {
		t.Fatalf("clear = %d %+v", status, cleared)
	}

	// Channel rules are validated, stored and replaced (ADR-20261019102657).
	rules := []api.RealtimeChannelRule{{Pattern: "org:{org_id}:room:*", Read: true}}
	rec := e.Do(t, http.MethodPatch, path, api.UpdateManagedRealtimeEndpointRequest{ChannelRules: &rules}, nil)
	var updated api.ManagedRealtimeEndpointResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &updated) != nil || len(updated.ChannelRules) != 1 {
		t.Fatalf("set rules = %d %s", rec.Code, rec.Body)
	}
	bad := []api.RealtimeChannelRule{{Pattern: "a*b", Read: true}}
	if rec := e.Do(t, http.MethodPatch, path, api.UpdateManagedRealtimeEndpointRequest{ChannelRules: &bad}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad rule = %d %s, want 400", rec.Code, rec.Body)
	}
	none := []api.RealtimeChannelRule{}
	rec = e.Do(t, http.MethodPatch, path, api.UpdateManagedRealtimeEndpointRequest{ChannelRules: &none}, nil)
	updated = api.ManagedRealtimeEndpointResponse{}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &updated) != nil || len(updated.ChannelRules) != 0 {
		t.Fatalf("clear rules = %d %s", rec.Code, rec.Body)
	}
}
