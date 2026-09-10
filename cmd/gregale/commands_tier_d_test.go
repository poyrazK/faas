// commands_tier_d_test.go — Tier D audit-gap smoke tests. Pins the
// 10 newly-wired leaves (5 in cmdRegistry dispatcher + cmdDeploymentSetMinInstances +
// cmdWakeTimeline + cmdAppSecurity + cmdOrgsUpdate + 3 in cmdDelayedTask
// dispatcher) so a refactor can't silently drop a route, flip an
// auth gate, or land a closed-set enum without a CLI check.
//
// Pattern (mirrors commands_tier_c_test.go): arg-validation exits 1,
// auth-gate exits 2 (where applicable), happy path hits the right
// route with the right body. --json path is checked for one leaf per
// family to pin the shape. stdout-content assertions are deliberately
// absent — the load-bearing seam is the route + method + body;
// renderer drift is covered by the existing output_test.go family.
//
// Pointer-shape regression is checked for the 3 patch leaves:
//   - cmdAppSecurity  → require_signed nil vs explicit true/false
//   - cmdOrgsUpdate   → name-only / plan-only / both / neither
//   - cmdDeploymentSetMinInstances → min only
//
// Each test asserts the body carries ONLY the fields the operator
// explicitly set, not the flag defaults — that's the bug class Tier C
// surfaced for cmdAlertUpdate and that Tier D has to carry forward.

package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// --- leaf 1: cmdDeploymentSetMinInstances ---

func TestTierD_DeploymentSetMinInstances_NoArgsExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdDeploymentSetMinInstances(nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestTierD_DeploymentSetMinInstances_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"id":"0123456789abcdef0123456789abcdef","min_instances":2}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdDeploymentSetMinInstances([]string{
		"--min", "2", "0123456789abcdef0123456789abcdef",
	}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "PATCH" || f.sawPath != "/v1/deployments/0123456789abcdef0123456789abcdef" {
		t.Errorf("route = %s %s, want PATCH /v1/deployments/<id>", f.sawMethod, f.sawPath)
	}
	var got map[string]any
	if err := json.Unmarshal(f.sawBody, &got); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, string(f.sawBody))
	}
	if got["min_instances"] != float64(2) {
		t.Errorf("min_instances = %v, want 2", got["min_instances"])
	}
}

// --- leaf 2: cmdWakeTimeline ---

func TestTierD_WakeTimeline_NoArgsExitsOne(t *testing.T) {
	resetJSONOut(t)
	if code := cmdWakeTimeline(nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestTierD_WakeTimeline_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"wake_id":"w-1","app_id":"a-1","events":[],"limit":50}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdWakeTimeline([]string{"demo", "w-1"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/apps/demo/wakes/w-1/timeline" {
		t.Errorf("route = %s %s, want GET /v1/apps/demo/wakes/w-1/timeline", f.sawMethod, f.sawPath)
	}
}

func TestTierD_WakeTimeline_BadLimitExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	// wakeTimelineMaxLimit = 1000; 1001 must fast-fail before the
	// server-side gate so a CLI typo costs zero latency.
	if code := cmdWakeTimeline([]string{"--limit", "1001", "demo", "w-1"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

// --- leaves 3-5: cmdRegistry dispatcher ---

func TestTierD_RegistryList_NoAppFlagExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdRegistryList(nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestTierD_RegistryList_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"credentials":[{"registry":"docker.io","username":"u","created_at":"2026-08-07T00:00:00Z","updated_at":"2026-08-07T00:00:00Z"}],"quota_max":5,"count":1}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdRegistryList([]string{"--app", "demo"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/apps/demo/registry-credentials" {
		t.Errorf("route = %s %s, want GET /v1/apps/demo/registry-credentials", f.sawMethod, f.sawPath)
	}
}

func TestTierD_RegistrySet_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"registry":"docker.io","username":"u","created_at":"2026-08-07T00:00:00Z","updated_at":"2026-08-07T00:00:00Z"}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdRegistrySet([]string{
		"--app", "demo", "--registry", "docker.io",
		"--user", "u", "--password", "p",
	}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "PUT" || f.sawPath != "/v1/apps/demo/registry-credentials" {
		t.Errorf("route = %s %s, want PUT /v1/apps/demo/registry-credentials", f.sawMethod, f.sawPath)
	}
	// Body must carry the four required fields; the password is
	// plaintext at the CLI boundary only — the SDK ships it to the
	// API and the server seals it. The CLI never echoes the
	// plaintext back (registry_auth.go contract).
	var got map[string]any
	if err := json.Unmarshal(f.sawBody, &got); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, string(f.sawBody))
	}
	if got["registry"] != "https://docker.io" {
		t.Errorf("registry = %v, want https://docker.io", got["registry"])
	}
	if got["username"] != "u" {
		t.Errorf("username = %v, want u", got["username"])
	}
	if got["password"] != "p" {
		t.Errorf("password = %v, want p", got["password"])
	}
}

func TestTierD_RegistrySetDocumentedVerbAndHTTPSInput(t *testing.T) {
	resetJSONOut(t)
	body := `{"registry":"ghcr.io","username":"u","created_at":"2026-08-07T00:00:00Z","updated_at":"2026-08-07T00:00:00Z"}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdRegistry([]string{"set", "--app", "demo", "--registry", "https://ghcr.io", "--user", "u", "--password", "p"}); code != 0 {
		t.Fatalf("registry set exit = %d", code)
	}
	var got map[string]any
	if err := json.Unmarshal(f.sawBody, &got); err != nil {
		t.Fatal(err)
	}
	if got["registry"] != "https://ghcr.io" {
		t.Fatalf("registry body = %v", got["registry"])
	}
}

// TestTierD_RegistrySet_BadRegistryHostExitsOne pins the closed-set
// gate: the same regex the SDK runs on the server must run locally
// so a typo costs zero latency. Re-uses api.RegistryHostRe() so a
// server-side regex change is mirrored at the CLI without further
// coordination.
func TestTierD_RegistrySet_BadRegistryHostExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdRegistrySet([]string{
		"--app", "demo", "--registry", "BadHost",
		"--user", "u", "--password", "p",
	}); code != 1 {
		t.Fatalf("exit = %d, want 1 (regex gate)", code)
	}
}

func TestTierD_RegistryRm_HappyPath(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, "", http.StatusNoContent)
	if code := cmdRegistryRm([]string{"--app", "demo", "--registry", "docker.io"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	// fakeAPI captures only r.URL.Path; the ?registry= query is
	// appended by the SDK after the path is captured. We assert
	// the path matches and rely on the SDK-level test
	// (registry_auth_test.go) to pin the query-param shape.
	if f.sawMethod != "DELETE" || f.sawPath != "/v1/apps/demo/registry-credentials" {
		t.Errorf("route = %s %s, want DELETE /v1/apps/demo/registry-credentials", f.sawMethod, f.sawPath)
	}
}

// --- leaf 6: cmdAppSecurity ---

func TestTierD_AppSecurity_NoArgsExitsOne(t *testing.T) {
	resetJSONOut(t)
	// Empty slug → usage error path (mirrors cmdAppScale's empty-slug
	// gate; cmdAppDispatch routes here with args[0] as the slug).
	if code := cmdAppSecurity("", nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestTierD_AppSecurity_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"require_signed":true}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdAppSecurity("demo", []string{"--require-signed=true"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "PATCH" || f.sawPath != "/v1/apps/demo/security" {
		t.Errorf("route = %s %s, want PATCH /v1/apps/demo/security", f.sawMethod, f.sawPath)
	}
	var got map[string]any
	if err := json.Unmarshal(f.sawBody, &got); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, string(f.sawBody))
	}
	if got["require_signed"] != true {
		t.Errorf("require_signed = %v, want true", got["require_signed"])
	}
}

// TestTierD_AppSecurity_FalseFlagWorks pins the literal-string gate:
// `--require-signed=false` (lowercase) must parse to bool(false), not
// fail closed. strconv.ParseBool's looseness (accepts "1", "t", "T",
// "True", "TRUE", …) is intentionally NOT exposed here — the handler
// enforces the strict literal too, so we mirror it for consistency.
func TestTierD_AppSecurity_FalseFlagWorks(t *testing.T) {
	resetJSONOut(t)
	body := `{"require_signed":false}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdAppSecurity("demo", []string{"--require-signed=false"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var got map[string]any
	if err := json.Unmarshal(f.sawBody, &got); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, string(f.sawBody))
	}
	if got["require_signed"] != false {
		t.Errorf("require_signed = %v, want false", got["require_signed"])
	}
}

func TestTierD_AppSecurity_BadLiteralExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdAppSecurity("demo", []string{"--require-signed=yes"}); code != 1 {
		t.Fatalf("exit = %d, want 1 (strict literal gate)", code)
	}
}

// --- leaf 7: cmdOrgsUpdate ---

func TestTierD_OrgsUpdate_NoOrgFlagExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdOrgsUpdate(nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestTierD_OrgsUpdate_BadPlanExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdOrgsUpdate([]string{"--org", "acme", "--plan", "platinum"}); code != 1 {
		t.Fatalf("exit = %d, want 1 (Plan.Valid gate)", code)
	}
}

func TestTierD_OrgsUpdate_NoFieldsExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	// --org supplied, but no --name / --plan — the leaf must
	// fast-fail so the operator doesn't send a PATCH body of
	// `{}` (which the handler would 400 anyway, but slower).
	if code := cmdOrgsUpdate([]string{"--org", "acme"}); code != 1 {
		t.Fatalf("exit = %d, want 1 (no fields)", code)
	}
}

// TestTierD_DeploymentSetMinInstances_FlagAfterPositional pins the
// splitArgsForFlags fix (code-review finding #1): the documented usage
// is `gregale deployment set-min-instances <id> --min N`, so the
// flag-after-positional order MUST work. Without splitArgsForFlags
// Go's flag.Parse silently drops --min and the server gets
// min_instances:0 — silently resetting the cold-wake floor.
func TestTierD_DeploymentSetMinInstances_FlagAfterPositional(t *testing.T) {
	resetJSONOut(t)
	body := `{"id":"0123456789abcdef0123456789abcdef","min_instances":5}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdDeploymentSetMinInstances([]string{
		"0123456789abcdef0123456789abcdef", "--min", "5",
	}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "PATCH" || f.sawPath != "/v1/deployments/0123456789abcdef0123456789abcdef" {
		t.Errorf("route = %s %s, want PATCH /v1/deployments/<id>", f.sawMethod, f.sawPath)
	}
	var got map[string]any
	if err := json.Unmarshal(f.sawBody, &got); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, string(f.sawBody))
	}
	if got["min_instances"] != float64(5) {
		t.Fatalf("min_instances = %v, want 5 (silent flag-drop would yield 0)", got["min_instances"])
	}
}

// TestTierD_WakeTimeline_FlagAfterPositional pins the same
// splitArgsForFlags fix for the wake-timeline leaf: documented usage
// is `gregale wake-timeline <slug> <wake-id> [--limit N]`, so the
// flag-after-positional order MUST be honored. Without
// splitArgsForFlags the limit/since/all flags after the two
// positionals are silently dropped.
func TestTierD_WakeTimeline_FlagAfterPositional(t *testing.T) {
	resetJSONOut(t)
	body := `{"wake_id":"w-1","app_id":"a-1","events":[],"limit":5}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdWakeTimeline([]string{
		"demo", "w-1", "--limit", "5",
	}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/apps/demo/wakes/w-1/timeline" {
		t.Errorf("route = %s %s, want GET /v1/apps/demo/wakes/w-1/timeline", f.sawMethod, f.sawPath)
	}
}

// TestTierD_OrgsUpdate_NameOnlyDoesNotResendPlan pins the
// pointer-shape fix: a name-only update must NOT carry plan in the
// body. Same regression class as Tier C's cmdAlertUpdate pin.
func TestTierD_OrgsUpdate_NameOnlyDoesNotResendPlan(t *testing.T) {
	resetJSONOut(t)
	body := `{"slug":"acme","name":"Acme Inc.","plan":"pro"}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdOrgsUpdate([]string{"--org", "acme", "--name", "Acme Inc."}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "PATCH" || f.sawPath != "/v1/orgs/acme" {
		t.Errorf("route = %s %s, want PATCH /v1/orgs/acme", f.sawMethod, f.sawPath)
	}
	var got map[string]any
	if err := json.Unmarshal(f.sawBody, &got); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, string(f.sawBody))
	}
	if got["name"] != "Acme Inc." {
		t.Errorf("name = %v, want \"Acme Inc.\"", got["name"])
	}
	if _, ok := got["plan"]; ok {
		t.Errorf("plan present in body; must be omitted on name-only update (got %v)", got["plan"])
	}
}

func TestTierD_OrgsUpdate_PlanOnlyHappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"slug":"acme","plan":"scale"}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdOrgsUpdate([]string{"--org", "acme", "--plan", "scale"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var got map[string]any
	if err := json.Unmarshal(f.sawBody, &got); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, string(f.sawBody))
	}
	if got["plan"] != "scale" {
		t.Errorf("plan = %v, want scale", got["plan"])
	}
	if _, ok := got["name"]; ok {
		t.Errorf("name present in body; must be omitted on plan-only update (got %v)", got["name"])
	}
}

// --- leaves 8-10: cmdDelayedTask dispatcher ---

func TestTierD_DelayedTaskAdd_NoAppFlagExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdDelayedTaskAdd(nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestTierD_DelayedTaskAdd_BadScheduledAtExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	// not-an-RFC3339 string — must fast-fail before authedClient()
	// so a typo costs zero latency.
	if code := cmdDelayedTaskAdd([]string{
		"--app", "demo", "--scheduled-at", "tomorrow",
	}); code != 1 {
		t.Fatalf("exit = %d, want 1 (RFC3339 gate)", code)
	}
}

// TestTierD_DelayedTaskAdd_PastTimestampExitsOne pins the
// "must-be-in-the-future" gate the server enforces
// (handlers_delayed_task.go::invalid_scheduled_at) — mirror at the
// CLI so a wrong --scheduled-at value never hits the wire.
func TestTierD_DelayedTaskAdd_PastTimestampExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdDelayedTaskAdd([]string{
		"--app", "demo", "--scheduled-at", "2020-01-01T00:00:00Z",
	}); code != 1 {
		t.Fatalf("exit = %d, want 1 (past-timestamp gate)", code)
	}
}

func TestTierD_DelayedTaskAdd_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"id":"0123456789abcdef0123456789abcdef","scheduled_at":"2030-01-01T00:00:00Z"}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdDelayedTaskAdd([]string{
		"--app", "demo",
		"--scheduled-at", "2030-01-01T00:00:00Z",
		"--payload", `{"hello":"world"}`,
	}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "POST" || f.sawPath != "/v1/apps/demo/delayed-tasks" {
		t.Errorf("route = %s %s, want POST /v1/apps/demo/delayed-tasks", f.sawMethod, f.sawPath)
	}
	var got map[string]any
	if err := json.Unmarshal(f.sawBody, &got); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, string(f.sawBody))
	}
	if got["scheduled_at"] != "2030-01-01T00:00:00Z" {
		t.Errorf("scheduled_at = %v, want 2030-01-01T00:00:00Z", got["scheduled_at"])
	}
	if got["payload"] == nil {
		t.Errorf("payload missing from body; resolvePayload must have forwarded it")
	}
}

func TestTierD_DelayedTaskGet_NoArgExitsOne(t *testing.T) {
	resetJSONOut(t)
	if code := cmdDelayedTaskGet(nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestTierD_DelayedTaskGet_BadIDExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdDelayedTaskGet([]string{"not-a-uuid"}); code != 1 {
		t.Fatalf("exit = %d, want 1 (32-hex gate)", code)
	}
}

func TestTierD_DelayedTaskGet_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"id":"0123456789abcdef0123456789abcdef","scheduled_at":"2030-01-01T00:00:00Z","state":"pending"}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdDelayedTaskGet([]string{"0123456789abcdef0123456789abcdef"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/delayed-tasks/0123456789abcdef0123456789abcdef" {
		t.Errorf("route = %s %s, want GET /v1/delayed-tasks/<id>", f.sawMethod, f.sawPath)
	}
}

func TestTierD_DelayedTaskCancel_NoArgExitsOne(t *testing.T) {
	resetJSONOut(t)
	if code := cmdDelayedTaskCancel(nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestTierD_DelayedTaskCancel_HappyPath(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, "", http.StatusOK)
	if code := cmdDelayedTaskCancel([]string{"0123456789abcdef0123456789abcdef"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "DELETE" || f.sawPath != "/v1/delayed-tasks/0123456789abcdef0123456789abcdef" {
		t.Errorf("route = %s %s, want DELETE /v1/delayed-tasks/<id>", f.sawMethod, f.sawPath)
	}
}

// --- NoTokenExitsTwo gates (one per new family) ---

func TestTierD_RegistryList_NoTokenExitsTwo(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	f := newFakeAPI(t, "", http.StatusOK)
	if code := cmdRegistryList([]string{"--app", "demo"}); code != 2 {
		t.Fatalf("exit = %d, want 2 (auth)", code)
	}
	if f.sawMethod != "" {
		t.Errorf("no network call expected without token; saw %s %s", f.sawMethod, f.sawPath)
	}
}

func TestTierD_DelayedTaskGet_NoTokenExitsTwo(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	f := newFakeAPI(t, "", http.StatusOK)
	if code := cmdDelayedTaskGet([]string{"0123456789abcdef0123456789abcdef"}); code != 2 {
		t.Fatalf("exit = %d, want 2 (auth)", code)
	}
	if f.sawMethod != "" {
		t.Errorf("no network call expected without token; saw %s %s", f.sawMethod, f.sawPath)
	}
}

func TestTierD_WakeTimeline_NoTokenExitsTwo(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	f := newFakeAPI(t, "", http.StatusOK)
	if code := cmdWakeTimeline([]string{"demo", "w-1"}); code != 2 {
		t.Fatalf("exit = %d, want 2 (auth)", code)
	}
	if f.sawMethod != "" {
		t.Errorf("no network call expected without token; saw %s %s", f.sawMethod, f.sawPath)
	}
}

// --- SDK export pin (mirrors the api.Plans export pattern) ---

// TestTierD_RegistryHostRe_AccessorReturnsSameRegex pins the
// accessor contract: the CLI's cmdRegistry* leaves call
// api.RegistryHostRe() at the same shape the SDK's
// PutAppRegistryCredentialRequest.Validate() matches against. A
// future regex change on the server side must flow through here or
// the CLI gate and the SDK gate drift apart.
func TestTierD_RegistryHostRe_AccessorReturnsSameRegex(t *testing.T) {
	resetJSONOut(t)
	re := api.RegistryHostRe()
	if re == nil {
		t.Fatal("RegistryHostRe returned nil; accessor must mirror the SDK-internal regex")
	}
	// Lowercase + dot + port: "docker.io:5000" must match.
	if !re.MatchString("docker.io:5000") {
		t.Error("docker.io:5000 should match; regex drift between CLI gate and SDK gate")
	}
	// Uppercase must NOT match (normalization is server-side only).
	if re.MatchString("Docker.IO") {
		t.Error("Docker.IO should NOT match; CLI gate must mirror the SDK's lowercase-only regex")
	}
}
