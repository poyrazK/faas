package mcpmanagement

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type publicationFixture struct {
	journal        map[string]api.ToolPublicationRecord
	base           *localFixture
	mu             sync.Mutex
	calls          [][]string
	deploys        int
	tests          int
	fail           bool
	lose           bool
	loseBegin      bool
	block          <-chan struct{}
	started        chan struct{}
	observedSource string
}

func publicationProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"gregale-mcp.json":       `{"version":1,"endpoint":"/mcp","transport":"streamable-http","mode":"stateless","legacy":true,"allowed_origins":[],"auth":{"mode":"open","tool_scopes":{"add":[]}}}`,
		"gregale-mcp.tests.json": `{"version":1,"cases":[{"name":"adds","kind":"tool","target":"add","arguments":{"a":1,"b":2},"expected_result":{"content":[{"type":"text","text":"3"}]}}]}`,
		"server.js":              "v1",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func newPublicationFixture(t *testing.T) (*publicationFixture, *httptest.Server) {
	t.Helper()
	f := &publicationFixture{base: newLocalFixture(t, true, "stable"), journal: map[string]api.ToolPublicationRecord{}}
	f.base.app.URL = "https://alpha.gregale.dev"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/tool-publications/") || r.URL.Path == "/v1/tool-registry" {
			f.serveJournal(w, r)
			return
		}
		if r.URL.Path == "/v1/deployments/candidate/url" {
			_ = json.NewEncoder(w).Encode(api.DeploymentPreviewURL{URL: "https://candidate.example", Alive: true})
			return
		}
		f.base.serveHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return f, s
}

func (f *publicationFixture) cli(ctx context.Context, args []string, _ []byte) (LocalCLIResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, slices.Clone(args))
	value := func(flag string) string {
		i := slices.Index(args, flag)
		if i < 0 || i+1 >= len(args) {
			return ""
		}
		return args[i+1]
	}
	if slices.Contains(args, "init") {
		dir := value("--path")
		f.mu.Unlock()
		// Match the real CLI's atomic-publish destination contract. The fake
		// must not accept an existing directory that real init rejects.
		if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
			return LocalCLIResult{}, errors.New("init destination already exists")
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return LocalCLIResult{}, err
		}
		for name, content := range map[string]string{"server.js": "const token = 'fp_test_fake_fixture';\n", "go.mod": "module tool\n", "gregale-mcp.json": "{}"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
				return LocalCLIResult{}, err
			}
		}
		return LocalCLIResult{Stdout: []byte(`{"created":true}`)}, nil
	}
	if slices.Contains(args, "test") {
		f.tests++
		f.mu.Unlock()
		data, _ := json.Marshal(mcphosting.SmokeReport{OK: true, Checks: []mcphosting.Check{{Name: "adds", Status: "passed"}}})
		return LocalCLIResult{Stdout: data}, nil
	}
	f.deploys++
	fail, lose, block, started := f.fail, f.lose, f.block, f.started
	f.mu.Unlock()
	if started != nil {
		close(started)
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return LocalCLIResult{}, ctx.Err()
		}
	}
	source, err := os.ReadFile(filepath.Join(value("--path"), "server.js"))
	if err != nil {
		return LocalCLIResult{}, err
	}
	f.mu.Lock()
	f.observedSource = string(source)
	f.mu.Unlock()
	f.base.mu.Lock()
	traffic := 0
	if !fail {
		traffic = 100
		f.base.current = "published"
	}
	f.base.deployments["published"] = api.DeploymentResponse{ID: "published", AppID: "app-one", Status: "live", Scope: "default", TrafficPercent: traffic, OverrideEnvKeys: []string{"MCP_CATALOG_EXPECTED_SHA256"}}
	f.base.mu.Unlock()
	if lose {
		return LocalCLIResult{}, errors.New("lost publication response")
	}
	checkStatus := "passed"
	if fail {
		checkStatus = "failed"
	}
	receipt := map[string]any{"app": "alpha", "deployment_id": "published", "endpoint": "https://alpha.gregale.dev/mcp", "auth_mode": "open", "promoted": !fail,
		"verification": map[string]any{"ok": true, "capabilities": []string{"tools"}, "tools": []mcphosting.Tool{{Name: "add", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "number"}, "b": map[string]any{"type": "number"}}}}}}, "smoke_tests": mcphosting.SmokeReport{OK: !fail, Checks: []mcphosting.Check{{Name: "adds", Status: checkStatus}}},
		"catalog_integrity":  map[string]any{"deployment_id": "published", "pin": map[string]any{"sha256": strings.Repeat("a", 64)}, "health": map[string]any{"healthy": true}},
		"protocol_integrity": map[string]any{"qualification": map[string]any{"qualified": true}}, "private_secret": "must-not-return"}
	data, _ := json.Marshal(receipt)
	code := 0
	if fail {
		code = 1
	}
	return LocalCLIResult{Stdout: data, ExitCode: code}, nil
}

func publicationCall(t *testing.T, ctx context.Context, cs *mcp.ClientSession, name string, args any, wantError bool) map[string]json.RawMessage {
	t.Helper()
	out := callTool(t, ctx, cs, name, args, wantError)
	data, _ := json.Marshal(out)
	schema := toolOutputSchema(name)
	if wantError {
		schema = map[string]any{"$defs": map[string]any{"recovery": recoverySchema()}, "anyOf": []any{toolErrorSchema()}}
	}
	validateWorkflowSchema(t, schema, data)
	if strings.Contains(string(data), "must-not-return") {
		t.Fatal("publication leaked raw CLI fields")
	}
	return out
}

func waitPublication(t *testing.T, ctx context.Context, cs *mcp.ClientSession, id string) map[string]json.RawMessage {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out := publicationCall(t, ctx, cs, "gregale_get_tool_deploy", planLookupArgs{PlanID: id}, false)
		if string(out["status"]) != `"running"` {
			return out
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("publication did not finish")
	return nil
}

func publicationArgs(dir string) planToolDeployArgs {
	return planToolDeployArgs{Path: dir, App: "alpha", Reason: "publish tested tool", AccessMode: "open", AllowToolExecution: true}
}

func TestToolBuildingPublicationJourney(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			f, backend := newPublicationFixture(t)
			cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, LocalCLI: f.cli, Toolsets: []string{ToolsetBuildTools}}, protocol)
			catalog, err := cs.ListTools(ctx, nil)
			if err != nil || len(catalog.Tools) != 6 {
				t.Fatalf("catalog=%v error=%v", catalog, err)
			}
			manifest := publicationCall(t, ctx, cs, "gregale_get_tool_template", toolTemplateArgs{Runtime: "go"}, false)
			if !strings.Contains(string(manifest["files"]), "go.mod") {
				t.Fatal("starter manifest omitted build files")
			}
			file := publicationCall(t, ctx, cs, "gregale_get_tool_template", toolTemplateArgs{Runtime: "go", File: "server.js"}, false)
			var encoded string
			_ = json.Unmarshal(file["content"], &encoded)
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil || !strings.Contains(string(decoded), "fp_test_fake_fixture") {
				t.Fatal("template bytes were changed by output redaction")
			}
			dir := publicationProject(t)
			f.base.mu.Lock()
			f.base.deployments["candidate"] = api.DeploymentResponse{ID: "candidate", AppID: "app-one", Status: "live", Scope: "default"}
			f.base.mu.Unlock()
			tested := publicationCall(t, ctx, cs, "gregale_test_tool", testToolArgs{Path: dir, App: "alpha", DeploymentID: "candidate", AccessMode: "open", AllowToolExecution: true}, false)
			if string(tested["passed"]) != "true" {
				t.Fatal("behavioral tests failed")
			}
			plan := publicationCall(t, ctx, cs, "gregale_plan_tool_deploy", publicationArgs(dir), false)
			var id string
			_ = json.Unmarshal(plan["plan_id"], &id)
			publicationCall(t, ctx, cs, "gregale_apply_tool_deploy", applyArgs{PlanID: id}, false)
			out := waitPublication(t, ctx, cs, id)
			if string(out["status"]) != `"completed"` || !strings.Contains(string(out["result"]), `"promoted":true`) {
				t.Fatalf("publication: %v", out)
			}
			publicationCall(t, ctx, cs, "gregale_apply_tool_deploy", applyArgs{PlanID: id}, false)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.deploys != 1 || f.tests != 1 {
				t.Fatalf("deploys=%d tests=%d", f.deploys, f.tests)
			}
			call := f.calls[len(f.calls)-1]
			for _, flag := range []string{"--require-catalog-pin", "--require-reviewed-tools", "--expected-app-id", "--expected-serving-deployment", "--allow-tool-execution", "--idempotency-key"} {
				if !slices.Contains(call, flag) {
					t.Fatalf("publication omitted %s", flag)
				}
			}
		})
	}
}

func TestToolBuildingFailedAndUnknownAttemptsNeverRepeat(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed verification", true: "lost response"}[lost], func(t *testing.T) {
			f, backend := newPublicationFixture(t)
			f.fail, f.lose = !lost, lost
			cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true, LocalCLI: f.cli, Toolsets: []string{ToolsetBuildTools}}, "2026-07-28")
			plan := publicationCall(t, ctx, cs, "gregale_plan_tool_deploy", publicationArgs(publicationProject(t)), false)
			var id string
			_ = json.Unmarshal(plan["plan_id"], &id)
			publicationCall(t, ctx, cs, "gregale_apply_tool_deploy", applyArgs{PlanID: id}, false)
			out := waitPublication(t, ctx, cs, id)
			want := `"failed"`
			if lost {
				want = `"outcome_unknown"`
			}
			if string(out["status"]) != want {
				t.Fatalf("status=%s", out["status"])
			}
			publicationCall(t, ctx, cs, "gregale_apply_tool_deploy", applyArgs{PlanID: id}, false)
			f.mu.Lock()
			calls := f.deploys
			f.mu.Unlock()
			if calls != 1 {
				t.Fatal("receipt recovery reran publishing")
			}
			if !lost {
				f.base.mu.Lock()
				defer f.base.mu.Unlock()
				if f.base.current != "stable" {
					t.Fatal("failed fixtures replaced production")
				}
			}
		})
	}
}

func TestToolBuildingDetectsSourceBytesAndServingChanges(t *testing.T) {
	for _, source := range []bool{true, false} {
		t.Run(map[bool]string{true: "source bytes", false: "serving release"}[source], func(t *testing.T) {
			f, backend := newPublicationFixture(t)
			cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true, LocalCLI: f.cli, Toolsets: []string{ToolsetBuildTools}}, "2026-07-28")
			dir := publicationProject(t)
			plan := publicationCall(t, ctx, cs, "gregale_plan_tool_deploy", publicationArgs(dir), false)
			var id string
			_ = json.Unmarshal(plan["plan_id"], &id)
			if source {
				path := filepath.Join(dir, "server.js")
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("v2"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			} else {
				f.base.mu.Lock()
				f.base.current = "other-release"
				f.base.mu.Unlock()
			}
			publicationCall(t, ctx, cs, "gregale_apply_tool_deploy", applyArgs{PlanID: id}, true)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.deploys != 0 {
				t.Fatal("stale plan reached publisher")
			}
		})
	}
}

func TestToolBuildingConsentAndReadOnly(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  Options
		count int
	}{
		{"read only", Options{}, 4},
		{"hosted", Options{AllowWrites: true, Environments: &EnvironmentConsent{}}, 0},
		{"headless hosted", Options{AllowWrites: true, agentKey: true}, 0},
		{"app scope", Options{AllowWrites: true, AllowedApps: map[string]string{"alpha": "app-one"}}, 0},
		{"organization", Options{AllowWrites: true, Org: "acme"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, backend := newPublicationFixture(t)
			tc.opts.APIBase, tc.opts.LocalCLI, tc.opts.Toolsets = backend.URL, f.cli, []string{ToolsetBuildTools}
			cs, ctx := connectSelectedTestServer(t, backend, tc.opts, "2026-07-28")
			catalog, err := cs.ListTools(ctx, nil)
			if err != nil || len(catalog.Tools) != tc.count {
				t.Fatalf("catalog=%v error=%v", catalog, err)
			}
			for _, name := range []string{"gregale_test_tool", "gregale_apply_tool_deploy"} {
				result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
				if err == nil && !result.IsError {
					t.Fatalf("unlisted %s was callable", name)
				}
			}
		})
	}
}

func TestToolBuildingRejectsUnsafeProjectAndTargets(t *testing.T) {
	f, backend := newPublicationFixture(t)
	cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, AllowWrites: true, LocalCLI: f.cli, Toolsets: []string{ToolsetBuildTools}}, "2026-07-28")
	dir := publicationProject(t)
	for _, mutate := range []func(*planToolDeployArgs){
		func(a *planToolDeployArgs) { a.AllowToolExecution = false }, func(a *planToolDeployArgs) { a.AccessMode = "" },
		func(a *planToolDeployArgs) { a.TokenEnv = "FAAS_TOKEN" }, func(a *planToolDeployArgs) { a.Fixtures = "../private" },
	} {
		a := publicationArgs(dir)
		mutate(&a)
		publicationCall(t, ctx, cs, "gregale_plan_tool_deploy", a, true)
	}
	publicationCall(t, ctx, cs, "gregale_test_tool", testToolArgs{Path: dir, App: "alpha", DeploymentID: "stable", AccessMode: "open", AllowToolExecution: true}, true)
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	publicationCall(t, ctx, cs, "gregale_plan_tool_deploy", publicationArgs(dir), true)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deploys != 0 || f.tests != 0 {
		t.Fatal("unsafe request executed work")
	}
}

func TestToolBuildingPublicationOutlivesTransport(t *testing.T) {
	f, backend := newPublicationFixture(t)
	release := make(chan struct{})
	f.block, f.started = release, make(chan struct{})
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, AllowWrites: true, LocalCLI: f.cli, Toolsets: []string{ToolsetBuildTools}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dir := publicationProject(t)
	plan, err := m.planToolDeploy(ctx, publicationArgs(dir))
	if err != nil {
		t.Fatal(err)
	}
	id := plan.(map[string]any)["plan_id"].(string)
	if _, err := m.applyToolDeploy(ctx, applyArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.started:
	case <-time.After(time.Second):
		t.Fatal("publication never started")
	}
	cancel()
	if err := os.WriteFile(filepath.Join(dir, "server.js"), []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, err := m.getToolDeploy(t.Context(), planLookupArgs{PlanID: id})
		if err != nil {
			t.Fatal(err)
		}
		if out.(map[string]any)["status"] == "completed" {
			f.mu.Lock()
			source := f.observedSource
			f.mu.Unlock()
			if source != "v1" {
				t.Fatal("edits after apply changed the reviewed source snapshot")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("publication was lost with its requesting transport")
}

func TestToolBuildingSnapshotPreservesModesAndCleansReadOnlyDirectories(t *testing.T) {
	source := publicationProject(t)
	lib := filepath.Join(source, "lib")
	if err := os.Mkdir(lib, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "handler.js"), []byte("tool"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lib, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(lib, 0o700) })
	digest, err := toolSourceDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := snapshotToolSource(source, digest)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeToolSnapshot(snapshot) })
	info, err := os.Stat(filepath.Join(snapshot, "project", "lib"))
	if err != nil || info.Mode().Perm() != 0o555 {
		t.Fatalf("snapshot directory mode: %v %v", info, err)
	}
	if err := removeToolSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatal("snapshot was not removed")
	}
}
