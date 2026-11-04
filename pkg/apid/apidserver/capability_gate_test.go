// adr: 20261023115741 — every apid route is released by its capability's
// platform flag; a route whose flag is off answers like a route that does not
// exist.
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

func gatedServer(t *testing.T) (*apidserver.Server, func(method, path string) *httptest.ResponseRecorder, string) {
	t.Helper()
	e := apidserver.Setup(t, api.PlanPro)
	srv := *e.S()
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	do := func(method, path string) *httptest.ResponseRecorder { return e.Do(t, method, path, nil, nil) }
	return srv, do, (*e.Acct()).ID
}

func missingRoute(r *httptest.ResponseRecorder) bool {
	return r.Code == http.StatusNotFound && strings.Contains(r.Body.String(), "no such API route")
}

func capabilityFlag(t *testing.T, capability string) string {
	t.Helper()
	key, ok := platformflags.CapabilityFlag(capability)
	if !ok {
		t.Fatalf("capability %q has no flag", capability)
	}
	return key
}

func TestCapabilityGateReleasesByTier(t *testing.T) {
	_, do, _ := gatedServer(t)
	if r := do(http.MethodGet, "/v1/apps"); r.Code != http.StatusOK {
		t.Fatalf("GA route GET /v1/apps = %d %s, want 200", r.Code, r.Body)
	}
	for _, path := range []string{"/v1/realtime/usage", "/v1/account/platform-tenants", "/v1/service-map"} {
		if r := do(http.MethodGet, path); !missingRoute(r) {
			t.Errorf("preview/internal route %s = %d %s, want the missing-route answer", path, r.Code, r.Body)
		}
	}
}

func TestCapabilityGateAccountRuleRevealsPreview(t *testing.T) {
	srv, do, acct := gatedServer(t)
	allow := true
	srv.SeedPlatformFlag(capabilityFlag(t, "platform-tenants"), platformflags.Definition{
		Enabled: true,
		Rules:   []platformflags.Rule{{ID: "design-partner", Accounts: []string{acct}, Value: &allow}},
	})
	if r := do(http.MethodGet, "/v1/account/platform-tenants"); missingRoute(r) {
		t.Fatalf("an account rule must release the preview to that account: %d %s", r.Code, r.Body)
	}
}

func TestCapabilityGateKillSwitchHidesBeforeAuth(t *testing.T) {
	srv, do, _ := gatedServer(t)
	srv.SeedPlatformFlag(capabilityFlag(t, "starter-templates"), platformflags.Definition{Enabled: false})
	if r := do(http.MethodGet, "/v1/templates"); !missingRoute(r) {
		t.Fatalf("authenticated request to a killed capability = %d %s, want 404", r.Code, r.Body)
	}
	anonymous := httptest.NewRecorder()
	srv.Handler().ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/v1/apps", nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET /v1/apps with apps on = %d, want 401", anonymous.Code)
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "apps"), platformflags.Definition{Enabled: false})
	anonymous = httptest.NewRecorder()
	srv.Handler().ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/v1/apps", nil))
	if !missingRoute(anonymous) {
		t.Fatalf("anonymous GET /v1/apps with apps killed = %d %s, want the missing-route answer before auth", anonymous.Code, anonymous.Body)
	}
	wrongMethod := httptest.NewRecorder()
	srv.Handler().ServeHTTP(wrongMethod, httptest.NewRequest(http.MethodPut, "/v1/apps", nil))
	if !missingRoute(wrongMethod) {
		t.Fatalf("a method mismatch on a killed route = %d, want 404 rather than a revealing 405", wrongMethod.Code)
	}
}

func TestCoreCapabilityFlagWriteNeedsForce(t *testing.T) {
	srv, _, _ := gatedServer(t)
	key := capabilityFlag(t, "apps")
	off := json.RawMessage(`{"enabled":false,"default":false}`)
	prob := srv.CheckPlatformFlagWrite(key, off, false)
	if prob == nil || prob.Status != http.StatusConflict {
		t.Fatalf("turning a core flag off without force = %+v, want 409", prob)
	}
	if prob := srv.CheckPlatformFlagWrite(key, off, true); prob != nil {
		t.Fatalf("turning a core flag off with force = %+v, want accepted", prob)
	}
	preview := capabilityFlag(t, "realtime")
	if prob := srv.CheckPlatformFlagWrite(preview, off, false); prob != nil {
		t.Fatalf("turning a non-core flag off = %+v, want accepted without force", prob)
	}
}

func TestCapabilitiesReportGateDecision(t *testing.T) {
	_, do, _ := gatedServer(t)
	r := do(http.MethodGet, "/v1/capabilities")
	if r.Code != http.StatusOK {
		t.Fatalf("GET /v1/capabilities = %d %s", r.Code, r.Body)
	}
	var out api.CapabilitiesResponse
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	enabled := map[string]bool{}
	for _, c := range out.Capabilities {
		enabled[c.Key] = c.Enabled
	}
	if !enabled["crons"] {
		t.Error("a GA capability the plan includes must be enabled")
	}
	if enabled["realtime"] {
		t.Error("a preview capability must be unavailable until its flag includes the account")
	}
	hidden := map[string]bool{}
	for _, id := range out.HiddenCapabilities {
		hidden[id] = true
	}
	if !hidden["realtime"] || !hidden["realtime-push"] || !hidden["service-map"] || hidden["crons"] {
		t.Errorf("hidden_capabilities = %v, want preview, sub-feature and internal capabilities but not GA crons", out.HiddenCapabilities)
	}
}

func TestCapabilityGateSubFeatureFollowsItsParent(t *testing.T) {
	srv, do, _ := gatedServer(t)
	if r := do(http.MethodGet, "/v1/notification-channels"); missingRoute(r) {
		t.Fatalf("a beta sub-feature of a beta parent must be released by default: %d %s", r.Code, r.Body)
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "notification-channels"), platformflags.Definition{Enabled: true, Default: false})
	if r := do(http.MethodGet, "/v1/notification-channels"); !missingRoute(r) {
		t.Fatalf("a sub-feature switched off must be hidden: %d %s", r.Code, r.Body)
	}
	if r := do(http.MethodGet, "/v1/alert-presets"); missingRoute(r) {
		t.Fatalf("a sibling sub-feature must stay released: %d %s", r.Code, r.Body)
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "notification-channels"), platformflags.Definition{Enabled: true, Default: true})
	srv.SeedPlatformFlag(capabilityFlag(t, "alerts"), platformflags.Definition{Enabled: true, Default: false})
	if r := do(http.MethodGet, "/v1/notification-channels"); !missingRoute(r) {
		t.Fatalf("a sub-feature must be hidden while its parent is off: %d %s", r.Code, r.Body)
	}
}

func capabilityRefused(r *httptest.ResponseRecorder) bool {
	return r.Code == http.StatusForbidden && strings.Contains(r.Body.String(), api.CodeCapabilityNotReleased)
}

func TestCapabilitySettingsRefuseFieldsTheirFlagHides(t *testing.T) {
	e := apidserver.Setup(t, api.PlanScale)
	srv := *e.S()
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	create := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "settings-api", RAMMB: 256}, nil)
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", create.Code, create.Body)
	}
	streaming := true
	if r := e.Do(t, http.MethodPatch, "/v1/apps/settings-api", api.UpdateAppRequest{StreamingEnabled: &streaming}, nil); capabilityRefused(r) {
		t.Fatalf("a GA setting must pass: %d %s", r.Code, r.Body)
	}
	grpc := "grpc"
	srv.SeedPlatformFlag(capabilityFlag(t, "grpc"), platformflags.Definition{Enabled: true, Default: false})
	if r := e.Do(t, http.MethodPatch, "/v1/apps/settings-api", api.UpdateAppRequest{AppProtocol: &grpc}, nil); !capabilityRefused(r) {
		t.Fatalf("a setting whose sub-feature is off must be refused: %d %s", r.Code, r.Body)
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "grpc"), platformflags.Definition{Enabled: true, Default: true})
	if r := e.Do(t, http.MethodPatch, "/v1/apps/settings-api", api.UpdateAppRequest{AppProtocol: &grpc}, nil); capabilityRefused(r) {
		t.Fatalf("gRPC with its flag on must pass: %d %s", r.Code, r.Body)
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "grpc"), platformflags.Definition{Enabled: true, Default: false})
	if r := e.Do(t, http.MethodPatch, "/v1/apps/settings-api", api.UpdateAppRequest{AppProtocol: &grpc}, nil); capabilityRefused(r) {
		t.Fatalf("re-sending a value the app already has must pass: %d %s", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodPost, "/v1/apps/settings-api/edge-rules", map[string]any{"kind": "async", "match_path": "/jobs/*", "action": map[string]any{}}, nil); !capabilityRefused(r) {
		t.Fatalf("an edge rule kind whose capability is off must be refused: %d %s", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodPost, "/v1/domains", map[string]any{"domain": "*.example.com", "app_id": "settings-api"}, nil); capabilityRefused(r) {
		t.Fatalf("wildcard domains are GA and must not be refused by the capability gate: %s", r.Body)
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "wildcard-domains"), platformflags.Definition{Enabled: true, Default: false})
	if r := e.Do(t, http.MethodPost, "/v1/domains", map[string]any{"domain": "*.example.com", "app_id": "settings-api"}, nil); !capabilityRefused(r) {
		t.Fatalf("a wildcard domain with its flag off must be refused: %d %s", r.Code, r.Body)
	}

	acct := *e.Acct()
	if prob := srv.CheckManifestSettings(acct, &gregalemanifest.Manifest{Triggers: []gregalemanifest.Trigger{{Kind: gregalemanifest.TriggerKindCron}}}); prob != nil {
		t.Fatalf("a cron trigger (GA crons) must pass: %+v", prob)
	}
	prob := srv.CheckManifestSettings(acct, &gregalemanifest.Manifest{Triggers: []gregalemanifest.Trigger{{Kind: gregalemanifest.TriggerKindKafka}}})
	if prob == nil || prob.Code != api.CodeCapabilityNotReleased {
		t.Fatalf("a Kafka trigger (preview event triggers) must be refused, got %+v", prob)
	}
}

// adr: 20261023115741 — the anonymous spec documents what every account gets:
// no operator routes, and a preview only once it is on by default.
func TestServedOpenAPIFollowsTheDefaultRelease(t *testing.T) {
	srv, do, _ := gatedServer(t)
	paths := func() map[string]any {
		t.Helper()
		r := do(http.MethodGet, "/v1/openapi.json")
		var spec struct {
			Paths map[string]any `json:"paths"`
		}
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &spec) != nil || len(spec.Paths) == 0 {
			t.Fatalf("GET /v1/openapi.json = %d %.300s", r.Code, r.Body.String())
		}
		return spec.Paths
	}
	got := paths()
	if _, ok := got["/v1/apps"]; !ok {
		t.Fatal("a GA path left the served spec")
	}
	for _, path := range []string{"/v1/apps/{slug}/tasks", "/v1/admin/config"} {
		if _, ok := got[path]; ok {
			t.Errorf("%s is served while it is preview or operator", path)
		}
	}
	srv.SeedPlatformFlag(capabilityFlag(t, "app-tasks"), platformflags.Definition{Enabled: true, Default: true})
	if _, ok := paths()["/v1/apps/{slug}/tasks"]; !ok {
		t.Error("a preview released to every account must be served")
	}
}
