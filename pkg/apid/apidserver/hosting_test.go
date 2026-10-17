package apidserver_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHostingDisabledNamespaceBeforeAuthenticationAndMethodRouting(t *testing.T) {
	// adr: 202610171453 — default-off paths must behave like unregistered routes.
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "false")
	env := apidserver.Setup(t, api.PlanHobby)
	baseline := env.Do(t, "GET", "/v1/does-not-exist", nil, nil)
	for _, route := range []struct{ method, path string }{
		{"GET", "/v1/internal/hosting/sites"},
		{"POST", "/v1/internal/hosting/oidc/exchange"},
		{"PATCH", "/v1/internal/hosting/capabilities"},
		{"OPTIONS", "/v1/internal/hosting/sites"},
		{"GET", "/v1/internal/hosting/unknown"},
		{"GET", "/v1/internal/%68osting/sites"},
	} {
		for _, credential := range []string{"", "Bearer invalid", "Bearer " + (*env.Key())} {
			r := httptest.NewRequest(route.method, route.path, nil)
			r.Header.Set("Authorization", credential)
			r.Header.Set("Origin", "https://customer.example")
			r.Header.Set("Access-Control-Request-Method", "POST")
			w := httptest.NewRecorder()
			(*env.H()).ServeHTTP(w, r)
			if w.Code != 404 || w.Body.String() != baseline.Body.String() || w.Header().Get("Allow") != "" || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%s %s: %d %v %s", route.method, route.path, w.Code, w.Header(), w.Body)
			}
		}
	}
}

func TestHostingRoutesDefaultOffAndOperatorOnly(t *testing.T) {
	// adr: 20261015 — all hosting routes require both opt-in and operator identity.
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", fmt.Sprint(enabled))
			t.Setenv("FAAS_ADMIN_EMAILS", "")
			env := apidserver.Setup(t, api.PlanHobby)
			if !enabled {
				(*env.S()).WithAdminAllowlist(env.Acct().Email)
			}
			root := "/v1/internal/hosting/sites"
			site := root + "/11111111-1111-4111-8111-111111111111"
			release := site + "/releases/22222222-2222-4222-8222-222222222222"
			for _, route := range []struct{ method, path string }{{"GET", "/v1/internal/hosting/capabilities"}, {"GET", "/v1/internal/hosting/usage"}, {"GET", root}, {"POST", root}, {"GET", site}, {"GET", site + "/status"}, {"GET", site + "/traffic"}, {"GET", site + "/history"}, {"GET", site + "/diagnostics"}, {"GET", site + "/cleanup"}, {"PUT", site + "/retention"}, {"GET", site + "/oidc-trusts"}, {"POST", site + "/oidc-trusts"}, {"DELETE", site + "/oidc-trusts/33333333-3333-4333-8333-333333333333"}, {"GET", site + "/automation-credentials"}, {"POST", site + "/automation-credentials"}, {"DELETE", site + "/automation-credentials/33333333-3333-4333-8333-333333333333"}, {"PUT", site + "/ci/33333333-3333-4333-8333-333333333333"}, {"GET", site + "/ci/33333333-3333-4333-8333-333333333333"}, {"POST", site + "/ci/33333333-3333-4333-8333-333333333333/preview"}, {"GET", site + "/previews"}, {"POST", site + "/previews"}, {"DELETE", site + "/previews/33333333-3333-4333-8333-333333333333"}, {"GET", site + "/domains"}, {"POST", site + "/domains"}, {"DELETE", site + "/domains/33333333-3333-4333-8333-333333333333"}, {"POST", site + "/domains/33333333-3333-4333-8333-333333333333/recover"}, {"DELETE", site}, {"PUT", site + "/suspension"}, {"GET", site + "/releases"}, {"POST", site + "/releases"}, {"POST", site + "/promotions"}, {"POST", release + "/uploads"}, {"POST", release + "/finalize"}, {"DELETE", release}} {
				response := env.Do(t, route.method, route.path, map[string]any{}, nil)
				want := 404
				code := api.CodeNotFound
				if enabled {
					want = 403
					code = "admin_required"
				}
				if response.Code != want || !strings.Contains(response.Body.String(), code) {
					t.Fatalf("%s %s: %d %s", route.method, route.path, response.Code, response.Body)
				}
			}
		})
	}
}

func TestHostingDisablePrecedesIdempotencyReplay(t *testing.T) {
	// adr: 20261015 — a kill switch applies to replayed successes too.
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "true")
	env := apidserver.Setup(t, api.PlanHobby)
	(*env.S()).WithAdminAllowlist(env.Acct().Email)
	values := map[string]string{
		"FAAS_FRONTEND_HOSTING_DOMAIN":          "hosting.example",
		"FAAS_FRONTEND_HOSTING_R2_ENDPOINT":     "https://example.r2.cloudflarestorage.com",
		"FAAS_FRONTEND_HOSTING_R2_BUCKET":       "hosting",
		"FAAS_FRONTEND_HOSTING_R2_ACCESS_KEY":   "test-key",
		"FAAS_FRONTEND_HOSTING_R2_SECRET_KEY":   "test-secret",
		"FAAS_FRONTEND_HOSTING_CF_ACCOUNT_ID":   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"FAAS_FRONTEND_HOSTING_KV_NAMESPACE_ID": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"FAAS_FRONTEND_HOSTING_CF_TOKEN":        "test-token",
	}
	if err := (*env.S()).ConfigureHostingForTest(func(key string) string { return values[key] }); err != nil {
		t.Fatal(err)
	}
	if _, err := (*env.Store()).CreateProject(t.Context(), state.Project{AccountID: env.Acct().ID, Slug: "shop"}); err != nil {
		t.Fatal(err)
	}
	request := api.CreateHostingSiteRequest{Project: "shop", Environment: "production", Name: "web"}
	headers := map[string]string{"Idempotency-Key": "hosting-create-once"}
	first := env.Do(t, "POST", "/v1/internal/hosting/sites", request, headers)
	if first.Code != 201 {
		t.Fatalf("create: %d %s", first.Code, first.Body)
	}
	manager := apidserver.NewRuntimeConfigManager(func(string) string { return "" })
	(*env.S()).WithRuntimeConfigManager(manager)
	replay := env.Do(t, "POST", "/v1/internal/hosting/sites", request, headers)
	if replay.Code != 404 || !strings.Contains(replay.Body.String(), api.CodeNotFound) {
		t.Fatalf("replayed enabled response after disable: %d %s", replay.Code, replay.Body)
	}
}

func TestHostingReleaseConfigRuntimeOptIn(t *testing.T) {
	// adr: 202610161909 — response policies have independent default-off admission.
	const key = "frontend_hosting_config_enabled"
	for _, value := range []string{"", "false", "true", "1"} {
		m := apidserver.NewRuntimeConfigManager(func(name string) string {
			if name == "FAAS_FRONTEND_HOSTING_CONFIG_ENABLED" {
				return value
			}
			return ""
		})
		if m.Bool(key, false) != (value == "true" || value == "1") {
			t.Fatalf("bootstrap opt-in %q", value)
		}
		if err := m.Apply(key, []byte("false")); err != nil {
			t.Fatal(err)
		}
		if m.Bool(key, true) {
			t.Fatal("hot disable did not override bootstrap")
		}
	}
}

func TestHostingPreviewRuntimeOptIn(t *testing.T) {
	// adr: 202610161954 — preview admission is independent and defaults off.
	for _, value := range []string{"", "false", "true", "1"} {
		m := apidserver.NewRuntimeConfigManager(func(name string) string {
			if name == "FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED" {
				return value
			}
			return ""
		})
		if m.Bool("frontend_hosting_previews_enabled", false) != (value == "true" || value == "1") {
			t.Fatal(value)
		}
		if err := m.Apply("frontend_hosting_previews_enabled", []byte("false")); err != nil {
			t.Fatal(err)
		}
		if m.Bool("frontend_hosting_previews_enabled", true) {
			t.Fatal("hot disable ignored")
		}
	}
}

func TestHostingHistoryRuntimeOptIn(t *testing.T) {
	// adr: 202610170902 — independent capture/provenance/history admission is default off.
	for _, value := range []string{"", "false", "true", "1"} {
		m := apidserver.NewRuntimeConfigManager(func(name string) string {
			if name == "FAAS_FRONTEND_HOSTING_HISTORY_ENABLED" {
				return value
			}
			return ""
		})
		if m.Bool("frontend_hosting_history_enabled", false) != (value == "true" || value == "1") {
			t.Fatal(value)
		}
		if err := m.Apply("frontend_hosting_history_enabled", []byte("false")); err != nil {
			t.Fatal(err)
		}
		if m.Bool("frontend_hosting_history_enabled", true) {
			t.Fatal("hot disable ignored")
		}
	}
}

func TestHostingHistoryDisablePrecedesReleaseReplay(t *testing.T) {
	// adr: 202610170902 — metadata admission applies before cached response replay.
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "true")
	env := apidserver.Setup(t, api.PlanHobby)
	(*env.S()).WithAdminAllowlist(env.Acct().Email)
	values := map[string]string{
		"FAAS_FRONTEND_HOSTING_ENABLED": "true", "FAAS_FRONTEND_HOSTING_HISTORY_ENABLED": "true",
		"FAAS_FRONTEND_HOSTING_DOMAIN": "hosting.example", "FAAS_FRONTEND_HOSTING_R2_ENDPOINT": "https://example.r2.cloudflarestorage.com",
		"FAAS_FRONTEND_HOSTING_R2_BUCKET": "hosting", "FAAS_FRONTEND_HOSTING_R2_ACCESS_KEY": "test-key", "FAAS_FRONTEND_HOSTING_R2_SECRET_KEY": "test-secret",
		"FAAS_FRONTEND_HOSTING_CF_ACCOUNT_ID": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "FAAS_FRONTEND_HOSTING_KV_NAMESPACE_ID": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "FAAS_FRONTEND_HOSTING_CF_TOKEN": "test-token",
	}
	getenv := func(key string) string { return values[key] }
	if err := (*env.S()).ConfigureHostingForTest(getenv); err != nil {
		t.Fatal(err)
	}
	manager := apidserver.NewRuntimeConfigManager(getenv)
	(*env.S()).WithRuntimeConfigManager(manager)
	_, err := (*env.Store()).CreateProject(t.Context(), state.Project{AccountID: env.Acct().ID, Slug: "history"})
	if err != nil {
		t.Fatal(err)
	}
	created := env.Do(t, "POST", "/v1/internal/hosting/sites", api.CreateHostingSiteRequest{Project: "history", Environment: "production", Name: "history"}, nil)
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body)
	}
	var site api.HostingSite
	if err = json.Unmarshal(created.Body.Bytes(), &site); err != nil {
		t.Fatal(err)
	}
	id := site.ID
	request := api.CreateHostingReleaseRequest{HostingManifest: api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", Size: 1, SHA256: strings.Repeat("a", 64), ContentType: "text/html"}}}, Provenance: &api.HostingReleaseProvenance{Description: "caller provided"}}
	path := "/v1/internal/hosting/sites/" + id + "/releases"
	headers := map[string]string{"Idempotency-Key": "history-release-once"}
	first := env.Do(t, "POST", path, request, headers)
	if first.Code != 201 {
		t.Fatal(first.Code, first.Body)
	}
	if err = manager.Apply("frontend_hosting_history_enabled", []byte("false")); err != nil {
		t.Fatal(err)
	}
	replay := env.Do(t, "POST", path, request, headers)
	if replay.Code != 503 || !strings.Contains(replay.Body.String(), "frontend_hosting_history_disabled") {
		t.Fatal(replay.Code, replay.Body)
	}
	legacy := env.Do(t, "POST", path, request.HostingManifest, nil)
	if legacy.Code != 201 {
		t.Fatal("off metadata gate blocked content-only release", legacy.Code, legacy.Body)
	}
}
