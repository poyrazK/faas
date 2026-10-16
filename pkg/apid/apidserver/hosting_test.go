package apidserver_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

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
			for _, route := range []struct{ method, path string }{{"GET", "/v1/internal/hosting/capabilities"}, {"GET", root}, {"POST", root}, {"GET", site}, {"GET", site + "/status"}, {"GET", site + "/domains"}, {"POST", site + "/domains"}, {"DELETE", site + "/domains/33333333-3333-4333-8333-333333333333"}, {"DELETE", site}, {"PUT", site + "/suspension"}, {"GET", site + "/releases"}, {"POST", site + "/releases"}, {"POST", site + "/promotions"}, {"POST", release + "/uploads"}, {"POST", release + "/finalize"}, {"DELETE", release}} {
				response := env.Do(t, route.method, route.path, map[string]any{}, nil)
				want := 503
				code := "frontend_hosting_disabled"
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
	if replay.Code != 503 || !strings.Contains(replay.Body.String(), "frontend_hosting_disabled") {
		t.Fatalf("replayed enabled response after disable: %d %s", replay.Code, replay.Body)
	}
}
