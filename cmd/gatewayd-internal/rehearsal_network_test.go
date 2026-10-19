// adr: 20261017201005
package main

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRehearsalNetworkServiceAuthorizationUsesRetainedRun(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "network-proxy@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	create := func(slug string) state.App {
		t.Helper()
		a, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: slug, RAMMB: 128, PreviewOfSlug: "source", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	caller, target, other := create("contained-caller"), create("contained-target"), create("other-run")
	const run = "abababababababababababababababab"
	if err := store.RegisterScenarioTestMembers(t.Context(), account.ID, run, []state.ScenarioTestMember{{AppID: caller.ID, Workload: "caller", WebhookMode: "capture", NetworkMode: "contained"}, {AppID: target.ID, Workload: "target", WebhookMode: "capture", NetworkMode: "contained"}}); err != nil {
		t.Fatal(err)
	}
	if got, err := newServiceProxyAuthorizer(store)(t.Context(), caller.ID, target.ID); err != nil || got.RehearsalRunID != run {
		t.Fatalf("authorizer lost retained containment: %+v %v", got, err)
	}
	if _, err := authorizeRehearsalService(t.Context(), store, caller, target); err != nil {
		t.Fatal(err)
	}
	if _, err := authorizeRehearsalService(t.Context(), store, caller, other); !errors.Is(err, gateway.ErrServiceProxyDenied) {
		t.Fatalf("escaped to another run: %v", err)
	}
	// Simulate namespace pruning while stale app metadata remains cached at
	// the proxy. The retained enrollment must still deny production fallback.
	for _, app := range []state.App{caller, target} {
		if _, err := store.SoftDeleteAppCascade(t.Context(), app.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteScenarioTestMembers(t.Context(), account.ID, run); err != nil {
		t.Fatal(err)
	}
	if _, err := authorizeRehearsalService(t.Context(), store, caller, target); !errors.Is(err, gateway.ErrServiceProxyDenied) {
		t.Fatalf("pruned membership reopened service path: %v", err)
	}
}

func TestRehearsalNetworkPublicDevelopmentTunnelRetainsFence(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "network-bridge@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "network-bridge", RAMMB: 128, PreviewOfSlug: "source", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	const run = "abababababababababababababababab"
	if err := store.RegisterScenarioTestMembers(t.Context(), account.ID, run, []state.ScenarioTestMember{{AppID: app.ID, Workload: "primary", WebhookMode: "capture", NetworkMode: "contained"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SoftDeleteAppCascade(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteScenarioTestMembers(t.Context(), account.ID, run); err != nil {
		t.Fatal(err)
	}
	target, _ := url.Parse("http://127.0.0.1:1")
	forward := developmentBridgeForwarder(store, target)
	req := httptest.NewRequest("GET", "/path", nil)
	req.Header.Set(devbridge.SessionHeader, "synthetic")
	response := httptest.NewRecorder()
	if !forward(response, req, gateway.App{ID: app.ID, AccountID: account.ID}) || response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "rehearsal apps cannot use development tunnels") {
		t.Fatalf("retained enrollment bypassed: %d %s", response.Code, response.Body.String())
	}
}
