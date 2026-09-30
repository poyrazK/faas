package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestUDPListenerAPI(t *testing.T) {
	h, key, store, acct := buildTestServer(t)
	ctx := context.Background()
	manifest := state.AppManifest{Ports: []api.WorkloadPort{{Name: "dns", Port: 5353, Protocol: api.WorkloadPortUDP}}}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "udp-api", RAMMB: 256, Status: state.AppActive, Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: status %d, want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	path := "/v1/apps/udp-api/udp-listeners"
	request(http.MethodPost, path, `{"name":"dns","guest_port":53}`, 400)
	w := request(http.MethodPost, path, `{"name":"dns","guest_port":5353}`, 201)
	var listener api.UDPListenerResponse
	if err := json.Unmarshal(w.Body.Bytes(), &listener); err != nil {
		t.Fatal(err)
	}
	if listener.Enabled || listener.Protocol != "udp" || listener.PublicPort < api.UDPListenerPublicPortMin {
		t.Fatalf("unexpected listener: %+v", listener)
	}
	request(http.MethodPost, path, `{"name":"dns","guest_port":5353}`, 409)
	request(http.MethodPatch, path+"/dns", `{"enabled":true}`, 200)
	request(http.MethodGet, path, "", 200)
	// Manifest changes must prevent re-enabling a stale public route.
	empty := state.AppManifest{}
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &empty}); err != nil {
		t.Fatal(err)
	}
	request(http.MethodPatch, path+"/dns", `{"enabled":false}`, 200)
	request(http.MethodPatch, path+"/dns", `{"enabled":true}`, 400)
	other, err := store.CreateAccount(ctx, "udp-other@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "udp-other", RAMMB: 256, Status: state.AppActive, Manifest: manifest}); err != nil {
		t.Fatal(err)
	}
	request(http.MethodGet, "/v1/apps/udp-other/udp-listeners", "", 404)
	request(http.MethodPost, "/v1/apps/udp-other/udp-listeners", `{"name":"dns","guest_port":5353}`, 404)
	request(http.MethodDelete, path+"/dns", "", 204)
	request(http.MethodPatch, path+"/dns", `{"enabled":true}`, 404)
}

func TestContainerListenerReadOnlyScope(t *testing.T) {
	e := setupWithScopes(t, []string{api.ScopeAppsRead})
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "listener-scopes", RAMMB: 256, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := e.store.CreateTCPListener(t.Context(), state.TCPListener{AccountID: e.acct.ID, AppID: app.ID, ListenerName: "echo", GuestPort: 9000, PublicPort: 40151, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/apps/listener-scopes"
	for _, scenario := range []struct {
		method, path string
		body         any
		want         int
	}{
		{http.MethodGet, "/tcp-listeners/echo/tls-status", nil, http.StatusOK},
		{http.MethodGet, "/udp-listeners", nil, http.StatusOK},
		{http.MethodPost, "/udp-listeners", map[string]any{"name": "dns", "guest_port": 5353}, http.StatusForbidden},
		{http.MethodPatch, "/udp-listeners/dns", map[string]bool{"enabled": true}, http.StatusForbidden},
		{http.MethodDelete, "/udp-listeners/dns", nil, http.StatusForbidden},
		{http.MethodPatch, "/tcp-listeners/echo", map[string]bool{"enabled": false}, http.StatusForbidden},
		{http.MethodPatch, "/tcp-listeners/echo", map[string]any{"tls": api.TCPListenerTLSConfig{Mode: api.TCPListenerTLSTerminate, Hostname: "echo.example"}}, http.StatusForbidden},
		{http.MethodDelete, "/tcp-listeners/echo", nil, http.StatusForbidden},
	} {
		t.Run(scenario.method+scenario.path, func(t *testing.T) {
			response := e.do(t, scenario.method, base+scenario.path, scenario.body, nil)
			if scenario.want == http.StatusForbidden {
				assertProblem(t, response, scenario.want, api.CodeForbidden)
			} else if response.Code != scenario.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, scenario.want, response.Body)
			}
		})
	}
	current, err := e.store.TCPListenerByAppAndName(t.Context(), app.ID, "echo")
	if err != nil || current.Enabled != listener.Enabled || current.TLSMode != listener.TLSMode {
		t.Fatalf("read-only key changed TCP intent: listener=%+v err=%v", current, err)
	}
	udp, err := e.store.ListUDPListenersForApp(t.Context(), app.ID)
	if err != nil || len(udp) != 0 {
		t.Fatalf("read-only key created UDP intent: listeners=%+v err=%v", udp, err)
	}
}
