// adr: 20261026141841
package mcpmanagement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// localFixture fakes the customer API and the CLI the local tools run.
type localFixture struct {
	mu          sync.Mutex
	exists      bool
	app         api.AppResponse
	current     string
	latest      string
	deployments map[string]api.DeploymentResponse
	keys        map[string]string
	revision    int64
	env         map[string]string
	calls       [][]string
	stdins      []string
	cliProblem  *api.Problem
	failAfter   bool
}

func newLocalFixture(t *testing.T, exists bool, current string) *localFixture {
	t.Helper()
	f := &localFixture{exists: exists, app: api.AppResponse{ID: "app-one", Slug: "alpha", Type: "app", RAMMB: 256, Status: "active"}, current: current, latest: current, deployments: map[string]api.DeploymentResponse{}, keys: map[string]string{}, env: map[string]string{}}
	if current != "" {
		f.deployments[current] = api.DeploymentResponse{ID: current, AppID: "app-one", Status: "live", Scope: "default", TrafficPercent: 100}
	}
	return f
}

func (f *localFixture) connect(t *testing.T, writes bool, mutate ...func(*Options)) (*mcp.ClientSession, context.Context) {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(backend.Close)
	opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: writes, LocalCLI: f.cli}
	for _, change := range mutate {
		change(&opts)
	}
	return connectConfiguredTestServer(t, backend, opts, "2026-07-28")
}

func (f *localFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer "+testToken {
		w.WriteHeader(401)
		return
	}
	if r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/traffic") {
		var body api.UpdateDeploymentTrafficRequest
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.ExpectedServingDeploymentID == nil || *body.ExpectedServingDeploymentID != f.current {
			api.WriteProblem(w, api.NewProblem(409, "traffic_serving_changed", "Serving release changed", "Create a new release plan"))
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/deployments/"), "/traffic")
		d := f.deployments[id]
		d.TrafficPercent, f.current = 100, id
		f.deployments[id] = d
		_ = json.NewEncoder(w).Encode(d)
		return
	}
	if !f.exists && strings.HasPrefix(r.URL.Path, "/v1/apps/alpha") {
		api.WriteProblem(w, api.NewProblem(404, "app_not_found", "App not found", "No app named alpha"))
		return
	}
	serving := []string{}
	if f.current != "" {
		serving = append(serving, f.current)
	}
	switch r.URL.Path {
	case "/v1/account":
		_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: "account-one"})
	case "/v1/apps/alpha":
		_ = json.NewEncoder(w).Encode(f.app)
	case "/v1/apps/alpha/health":
		_ = json.NewEncoder(w).Encode(api.AppHealthResponse{AppID: f.app.ID, Scope: "default", ServingDeploymentIDs: serving, LatestDeploymentID: f.latest})
	case "/v1/apps/alpha/release-review":
		_ = json.NewEncoder(w).Encode(api.AppReleaseReview{AppID: f.app.ID, ConfigurationHash: strings.Repeat("a", 64), LatestDeploymentID: f.latest, ServingDeploymentIDs: serving})
	case "/v1/apps/alpha/config/revisions":
		out := api.ListConfigRevisionsResponse{}
		if f.revision > 0 {
			out.Revisions = []api.ConfigRevision{{Revision: f.revision, Scope: "default"}}
		}
		_ = json.NewEncoder(w).Encode(out)
	default:
		d, ok := f.deployments[strings.TrimPrefix(r.URL.Path, "/v1/deployments/")]
		if !ok {
			api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", r.URL.Path))
			return
		}
		_ = json.NewEncoder(w).Encode(d)
	}
}

func (f *localFixture) cli(_ context.Context, args []string, stdin []byte) (LocalCLIResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, slices.Clone(args))
	f.stdins = append(f.stdins, string(stdin))
	if f.cliProblem != nil {
		problem := *f.cliProblem
		f.cliProblem = nil
		data, _ := json.Marshal(problem)
		return LocalCLIResult{Stderr: append([]byte("Checking project\n"), data...), ExitCode: 1}, nil
	}
	ok := func(v any) (LocalCLIResult, error) {
		data, _ := json.Marshal(v)
		return LocalCLIResult{Stdout: data}, nil
	}
	switch {
	case args[0] == "deploy" && slices.Contains(args, "--plan"):
		return ok(map[string]any{"slug": "alpha", "source": "directory", "port": 8080, "health_path": "/", "execution_mode": "request"})
	case args[0] == "deploy" && slices.Contains(args, "--create-only"):
		if slices.Contains(args, "--yes") || slices.Contains(args, "--no-wait") || slices.Contains(args, "--idempotency-key") {
			// Mirrors the CLI: --create-only refuses deployment-only options.
			data, _ := json.Marshal(api.Problem{Code: "invalid_request", Title: "Invalid flags"})
			return LocalCLIResult{Stderr: data, ExitCode: 1}, nil
		}
		f.exists = true
		return ok(map[string]any{"slug": "alpha"})
	case args[0] == "deploy":
		key := args[slices.Index(args, "--idempotency-key")+1]
		id, seen := f.keys[key]
		if !seen {
			f.exists = true
			id = fmt.Sprintf("release-%d", len(f.deployments)+1)
			d := api.DeploymentResponse{ID: id, AppID: f.app.ID, Status: "live", Scope: "default", TrafficPercent: 100}
			if slices.Contains(args, "--no-traffic") {
				d.TrafficPercent = 0
			} else {
				f.current = id
			}
			f.deployments[id], f.keys[key], f.latest = d, id, id
		}
		if f.failAfter {
			f.failAfter = false
			data, _ := json.Marshal(api.Problem{Code: "transport_error", Title: "Connection reset", Status: 0})
			return LocalCLIResult{Stderr: data, ExitCode: 3}, nil
		}
		return ok(map[string]any{"id": id, "app_id": f.app.ID, "status": "live", "app_url": "https://alpha.gregale.dev"})
	case args[0] == "env" && slices.Contains(args, "--dry-run"):
		changes, findings := []map[string]string{}, []map[string]any{}
		for _, line := range strings.Split(strings.TrimSpace(string(stdin)), "\n") {
			key, value, _ := strings.Cut(line, "=")
			changes = append(changes, map[string]string{"key": key, "action": "add"})
			if strings.HasPrefix(value, "sk_live_") {
				findings = append(findings, map[string]any{"key": key, "pair": 1, "provider": "stripe", "severity": "high"})
			}
		}
		return ok(map[string]any{"app_slug": "alpha", "scope": "default", "dry_run": true, "can_push": len(findings) == 0, "changes": changes, "scan_findings": findings, "blockers": []string{}, "apply_mode": "next_cold_wake"})
	case args[0] == "env":
		keys := []string{}
		for _, line := range strings.Split(strings.TrimSpace(string(stdin)), "\n") {
			key, value, _ := strings.Cut(line, "=")
			f.env[key] = value
			keys = append(keys, key)
		}
		f.revision++
		return ok(map[string]any{"app_slug": "alpha", "scope": "default", "updated_keys": keys, "apply_mode": "next_cold_wake"})
	}
	return LocalCLIResult{Stderr: []byte("unexpected command"), ExitCode: 2}, nil
}

func (f *localFixture) deployCalls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, call := range f.calls {
		if call[0] == "deploy" && !slices.Contains(call, "--plan") {
			out = append(out, call)
		}
	}
	return out
}

func projectDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server.js"), []byte("require('http').createServer().listen(8080)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func toolCode(t *testing.T, out map[string]json.RawMessage) string {
	t.Helper()
	var code string
	if err := json.Unmarshal(out["code"], &code); err != nil {
		t.Fatalf("no error code in %v", out)
	}
	return code
}

func listedTools(t *testing.T, ctx context.Context, cs *mcp.ClientSession) map[string]bool {
	t.Helper()
	page, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range page.Tools {
		names[tool.Name] = true
	}
	return names
}

func TestLocalToolsOnlyOnLocalConnections(t *testing.T) {
	local := []string{"gregale_plan_local_deploy", "gregale_plan_env_change", "gregale_apply_local_deploy", "gregale_apply_env_change"}
	for _, tc := range []struct {
		name   string
		writes bool
		mutate func(*Options)
		want   []string
	}{
		{"local with writes", true, func(*Options) {}, local},
		{"local read-only", false, func(*Options) {}, local[:2]},
		{"no CLI runner", true, func(o *Options) { o.LocalCLI = nil }, nil},
		{"app consent", true, func(o *Options) { o.AllowedApps = map[string]string{"alpha": "app-one"} }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs, ctx := newLocalFixture(t, true, "").connect(t, tc.writes, tc.mutate)
			tools := listedTools(t, ctx, cs)
			for _, name := range local {
				if tools[name] != slices.Contains(tc.want, name) {
					t.Errorf("%s listed=%v", name, tools[name])
				}
			}
		})
	}
}

func TestLocalDeployCreatesANewAppAndIsIdempotent(t *testing.T) {
	f := newLocalFixture(t, false, "")
	cs, ctx := f.connect(t, true)
	dir := projectDir(t)
	plan := callTool(t, ctx, cs, "gregale_plan_local_deploy", map[string]any{"path": dir, "app": "alpha", "reason": "first deploy"}, false)
	var action string
	_ = json.Unmarshal(plan["action"], &action)
	if action != "local_deploy" || !strings.Contains(string(plan["local_source"]), `"new_app":true`) || !strings.Contains(string(plan["local_source"]), `"execution_mode":"request"`) {
		t.Fatalf("plan = %v", plan)
	}
	id := planID(t, plan)
	first := callTool(t, ctx, cs, "gregale_apply_local_deploy", map[string]any{"plan_id": id}, false)
	again := callTool(t, ctx, cs, "gregale_apply_local_deploy", map[string]any{"plan_id": id}, false)
	if string(first["deployment"]) != string(again["deployment"]) || !strings.Contains(string(first["app_url"]), "alpha.gregale.dev") {
		t.Fatalf("apply = %v, again = %v", first, again)
	}
	calls := f.deployCalls()
	if len(calls) != 1 {
		t.Fatalf("deploy ran %d times, want 1", len(calls))
	}
	want := []string{"deploy", "--path", dir, "--name", "alpha", "--app", "--yes", "--no-wait", "--idempotency-key", "mcp-local-" + id}
	if !slices.Equal(calls[0], want) {
		t.Fatalf("deploy args = %v, want %v", calls[0], want)
	}
}

func TestLocalDeployStagesAServingAppAndPromotesAtomically(t *testing.T) {
	f := newLocalFixture(t, true, "release-base")
	cs, ctx := f.connect(t, true)
	plan := callTool(t, ctx, cs, "gregale_plan_local_deploy", map[string]any{"path": projectDir(t), "app": "alpha", "reason": "ship fix"}, false)
	var action string
	_ = json.Unmarshal(plan["action"], &action)
	if action != "deploy" || !strings.Contains(string(plan["expected_current_deployment_id"]), "release-base") {
		t.Fatalf("plan = %v", plan)
	}
	id := planID(t, plan)
	if out := callTool(t, ctx, cs, "gregale_apply_deploy", map[string]any{"plan_id": id}, true); toolCode(t, out) != "invalid_plan" {
		t.Fatalf("image apply accepted a local plan: %v", out)
	}
	staged := callTool(t, ctx, cs, "gregale_apply_local_deploy", map[string]any{"plan_id": id}, false)
	if !slices.Contains(f.deployCalls()[0], "--no-traffic") || !strings.Contains(string(staged["deployment"]), `"traffic_percent":0`) {
		t.Fatalf("staged = %v; calls = %v", staged, f.deployCalls())
	}
	callTool(t, ctx, cs, "gregale_promote_deployment", map[string]any{"plan_id": id}, false)
	if f.current == "release-base" {
		t.Fatal("promotion did not move traffic")
	}
}

func TestLocalDeployRetriesAnAmbiguousAttemptWithTheSameKey(t *testing.T) {
	f := newLocalFixture(t, false, "")
	f.failAfter = true
	cs, ctx := f.connect(t, true)
	id := planID(t, callTool(t, ctx, cs, "gregale_plan_local_deploy", map[string]any{"path": projectDir(t), "app": "alpha", "reason": "first deploy"}, false))
	if out := callTool(t, ctx, cs, "gregale_apply_local_deploy", map[string]any{"plan_id": id}, true); toolCode(t, out) != "transport_error" {
		t.Fatalf("ambiguous apply = %v", out)
	}
	callTool(t, ctx, cs, "gregale_apply_local_deploy", map[string]any{"plan_id": id}, false)
	if len(f.deployments) != 1 {
		t.Fatalf("deployments = %d, want 1", len(f.deployments))
	}
	calls := f.deployCalls()
	if len(calls) != 2 || !slices.Equal(calls[0], calls[1]) {
		t.Fatalf("retry changed the command: %v", calls)
	}
}

func TestLocalDeployRefusesAChangedDirectory(t *testing.T) {
	f := newLocalFixture(t, false, "")
	cs, ctx := f.connect(t, true)
	dir := projectDir(t)
	id := planID(t, callTool(t, ctx, cs, "gregale_plan_local_deploy", map[string]any{"path": dir, "app": "alpha", "reason": "first deploy"}, false))
	later := time.Now().Add(time.Minute)
	if err := os.WriteFile(filepath.Join(dir, "server.js"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "server.js"), later, later); err != nil {
		t.Fatal(err)
	}
	if out := callTool(t, ctx, cs, "gregale_apply_local_deploy", map[string]any{"plan_id": id}, true); toolCode(t, out) != "source_changed" {
		t.Fatalf("apply = %v", out)
	}
	if calls := f.deployCalls(); len(calls) != 0 {
		t.Fatalf("deploy ran: %v", calls)
	}
}

func TestLocalDeployRefusesUnsafeDirectories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(home, "work", "api")
	linked := filepath.Join(home, "linked", "svc")
	for _, dir := range []string{project, filepath.Join(home, "linked", ".gregale"), linked} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "linked", ".gregale", "project.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newLocalFixture(t, false, "")
	cs, ctx := f.connect(t, true)
	for path, code := range map[string]string{"relative/dir": "invalid_path", "/": "invalid_path", home: "invalid_path", filepath.Join(home, "work", "missing"): "invalid_path", linked: "linked_project"} {
		out := callTool(t, ctx, cs, "gregale_plan_local_deploy", map[string]any{"path": path, "app": "alpha", "reason": "deploy"}, true)
		if got := toolCode(t, out); got != code {
			t.Errorf("%s: code %q, want %q", path, got, code)
		}
	}
	callTool(t, ctx, cs, "gregale_plan_local_deploy", map[string]any{"path": project, "app": "alpha", "reason": "deploy"}, false)
}

func TestLocalDeployCreateOnly(t *testing.T) {
	f := newLocalFixture(t, false, "")
	cs, ctx := f.connect(t, true)
	dir := projectDir(t)
	id := planID(t, callTool(t, ctx, cs, "gregale_plan_local_deploy", map[string]any{"path": dir, "app": "alpha", "reason": "reserve", "create_only": true}, false))
	out := callTool(t, ctx, cs, "gregale_apply_local_deploy", map[string]any{"plan_id": id}, false)
	if !strings.Contains(string(out["app"]), "app-one") || !slices.Contains(f.deployCalls()[0], "--create-only") || len(f.deployments) != 0 {
		t.Fatalf("create-only = %v; calls %v", out, f.deployCalls())
	}
	if out := callTool(t, ctx, cs, "gregale_plan_local_deploy", map[string]any{"path": dir, "app": "alpha", "reason": "reserve", "create_only": true}, true); toolCode(t, out) != "app_exists" {
		t.Fatalf("second create-only plan = %v", out)
	}
}

func TestLocalDeployReturnsTheCLIProblem(t *testing.T) {
	f := newLocalFixture(t, false, "")
	f.cliProblem = &api.Problem{Code: "invalid_manifest", Title: "Invalid deploy manifest", Detail: "gregale.yaml: port must be a number", Hint: "fix gregale.yaml"}
	cs, ctx := f.connect(t, true)
	out := callTool(t, ctx, cs, "gregale_plan_local_deploy", map[string]any{"path": projectDir(t), "app": "alpha", "reason": "deploy"}, true)
	if toolCode(t, out) != "invalid_manifest" || !strings.Contains(string(out["message"]), "port must be a number") || !strings.Contains(string(out["hint"]), "gregale.yaml") {
		t.Fatalf("error = %v", out)
	}
}

func TestEnvChangePlansKeysAndAppliesWithARevisionGuard(t *testing.T) {
	f := newLocalFixture(t, true, "release-base")
	f.revision = 7
	cs, ctx := f.connect(t, true)
	values := map[string]any{"LOG_LEVEL": "debug", "API_URL": "https://example.invalid/v1?a=b"}
	plan := callTool(t, ctx, cs, "gregale_plan_env_change", map[string]any{"app": "alpha", "values": values, "reason": "verbose logs"}, false)
	data, _ := json.Marshal(plan)
	if strings.Contains(string(data), "example.invalid") {
		t.Fatalf("plan echoed values: %s", data)
	}
	if !strings.Contains(string(plan["env_change"]), `"expected_revision":7`) || !strings.Contains(string(plan["env_change"]), `"API_URL"`) {
		t.Fatalf("plan = %v", plan)
	}
	wantArgs := []string{"env", "push", "--app", "alpha", "--scope", "default", "--from-stdin", "--secret-scan", "strict", "--dry-run"}
	if !slices.Equal(f.calls[0], wantArgs) || f.stdins[0] != "API_URL=https://example.invalid/v1?a=b\nLOG_LEVEL=debug\n" {
		t.Fatalf("preview call = %v %q", f.calls[0], f.stdins[0])
	}
	id := planID(t, plan)
	out := callTool(t, ctx, cs, "gregale_apply_env_change", map[string]any{"plan_id": id}, false)
	if f.env["LOG_LEVEL"] != "debug" || !strings.Contains(string(out["updated_keys"]), "LOG_LEVEL") || slices.Contains(f.calls[1], "--dry-run") {
		t.Fatalf("apply = %v; env %v; calls %v", out, f.env, f.calls)
	}

	stale := planID(t, callTool(t, ctx, cs, "gregale_plan_env_change", map[string]any{"app": "alpha", "values": map[string]any{"LOG_LEVEL": "info"}, "reason": "quiet"}, false))
	f.mu.Lock()
	f.revision++
	f.mu.Unlock()
	if out := callTool(t, ctx, cs, "gregale_apply_env_change", map[string]any{"plan_id": stale}, true); toolCode(t, out) != "plan_stale" {
		t.Fatalf("stale apply = %v", out)
	}
}

func TestEnvChangeRefusesSecretsAndBadInput(t *testing.T) {
	f := newLocalFixture(t, true, "release-base")
	cs, ctx := f.connect(t, true)
	out := callTool(t, ctx, cs, "gregale_plan_env_change", map[string]any{"app": "alpha", "values": map[string]any{"STRIPE_KEY": "sk_live_abc"}, "reason": "billing"}, true)
	if toolCode(t, out) != "secret_detected" || !strings.Contains(string(out["message"]), "gregale secrets set alpha") {
		t.Fatalf("finding = %v", out)
	}
	f.cliProblem = &api.Problem{Code: "secret_scan_strict", Title: "Secret scan rejected the push"}
	if out := callTool(t, ctx, cs, "gregale_plan_env_change", map[string]any{"app": "alpha", "values": map[string]any{"TOKEN": "x"}, "reason": "auth"}, true); toolCode(t, out) != "secret_detected" {
		t.Fatalf("strict rejection = %v", out)
	}
	many := map[string]any{}
	for i := 0; i <= maxEnvChangeKeys; i++ {
		many[fmt.Sprintf("KEY_%d", i)] = "v"
	}
	for _, values := range []map[string]any{{"lower": "v"}, {"MULTI": "a\nB=b"}, many, {}} {
		if out := callTool(t, ctx, cs, "gregale_plan_env_change", map[string]any{"app": "alpha", "values": values, "reason": "x"}, true); toolCode(t, out) != "invalid_argument" {
			t.Errorf("values %v: %v", values, out)
		}
	}
}

func TestFingerprintTreeTracksFilesAndSkipsGit(t *testing.T) {
	dir := projectDir(t)
	first, files, _, err := fingerprintTree(dir)
	if err != nil || files != 1 {
		t.Fatalf("fingerprint = %q, %d, %v", first, files, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref"), 0o644); err != nil {
		t.Fatal(err)
	}
	if again, _, _, _ := fingerprintTree(dir); again != first {
		t.Fatal(".git changed the fingerprint")
	}
	if err := os.WriteFile(filepath.Join(dir, "added.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, _, _, _ := fingerprintTree(dir); changed == first {
		t.Fatal("a new file kept the fingerprint")
	}
}
