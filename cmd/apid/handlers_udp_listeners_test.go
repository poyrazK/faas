package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	for _, body := range []string{
		`{"name":"dns","guest_port":5353,"enabled":true}`,
		`{"name":"dns","guest_port":5353} {}`,
		`{"name":"dns","guest_port":5353,"public_port":39999}`,
		`{"name":"dns","guest_port":5353,"public_port":50000}`,
		`{"name":"dns","guest_port":65536}`,
		`null`,
	} {
		request(http.MethodPost, path, body, 400)
	}
	rows, err := store.ListUDPListenersForApp(ctx, app.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("invalid create reserved listeners: rows=%+v err=%v", rows, err)
	}
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
	before, err := store.UDPListenerByAppAndName(ctx, app.ID, "dns")
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{}`, `null`, `{"enabled":null}`, `{"enabled":"true"}`, `{"enabled":true,"guest_port":53}`, `{"enabled":true} false`} {
		request(http.MethodPatch, path+"/dns", body, 400)
	}
	after, err := store.UDPListenerByAppAndName(ctx, app.ID, "dns")
	if err != nil || after != before {
		t.Fatalf("invalid update changed listener: before=%+v after=%+v err=%v", before, after, err)
	}
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
	foreignApp, err := store.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "udp-other", RAMMB: 256, Status: state.AppActive, Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateUDPListener(ctx, state.UDPListener{AppID: foreignApp.ID, AccountID: other.ID, ListenerName: "dns", GuestPort: 5353, PublicPort: 40199, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	request(http.MethodGet, "/v1/apps/udp-other/udp-listeners", "", 404)
	request(http.MethodPost, "/v1/apps/udp-other/udp-listeners", `{"name":"dns","guest_port":5353}`, 404)
	request(http.MethodPatch, "/v1/apps/udp-other/udp-listeners/dns", `{"enabled":false}`, 404)
	request(http.MethodPatch, "/v1/apps/udp-other/udp-listeners/dns", `{"enabled":true}`, 404)
	request(http.MethodDelete, "/v1/apps/udp-other/udp-listeners/dns", "", 404)
	retained, err := store.UDPListenerByAppAndName(ctx, foreignApp.ID, "dns")
	if err != nil || retained != foreign {
		t.Fatalf("foreign listener mutated: got=%+v want=%+v err=%v", retained, foreign, err)
	}
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

func TestUDPListenerQuotaProblem(t *testing.T) {
	h, key, store, account := buildTestServer(t)
	ctx := context.Background()
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "udp-quota", RAMMB: 256, Status: state.AppActive, Manifest: state.AppManifest{Ports: []api.WorkloadPort{{Name: "current", Port: 5353, Protocol: api.WorkloadPortUDP}}}})
	if err != nil {
		t.Fatal(err)
	}
	// Model reservations retained from older manifests; none is enabled.
	for i := 0; i < api.UDPListenerReservationsPerAppMax; i++ {
		if _, err := store.CreateUDPListener(ctx, state.UDPListener{AccountID: account.ID, AppID: app.ID, ListenerName: fmt.Sprintf("reserved-%d", i), GuestPort: 1000 + i, PublicPort: 41000 + i}); err != nil {
			t.Fatal(err)
		}
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	path := "/v1/apps/udp-quota/udp-listeners"
	for _, body := range []string{`{"name":"current","guest_port":5353}`, `{"name":"current","guest_port":5353,"public_port":43000}`} {
		w := request(http.MethodPost, path, body)
		var problem api.Problem
		if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusConflict || problem.Code != api.CodeUDPListenerLimit || problem.Limit == nil || *problem.Limit != int64(api.UDPListenerReservationsPerAppMax) || problem.Observed == nil || *problem.Observed != int64(api.UDPListenerReservationsPerAppMax+1) || !strings.HasSuffix(problem.DocsURL, "/containers#udp-listeners") {
			t.Fatalf("status=%d quota problem=%+v", w.Code, problem)
		}
	}
	if w := request(http.MethodDelete, path+"/reserved-0", ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if w := request(http.MethodPost, path, `{"name":"current","guest_port":5353}`); w.Code != http.StatusCreated {
		t.Fatalf("create after delete: %d %s", w.Code, w.Body.String())
	}
}
