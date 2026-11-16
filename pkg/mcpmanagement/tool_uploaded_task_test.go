package mcpmanagement

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type uploadedTaskFixture struct {
	token        string
	opts         Options
	backend      *httptest.Server
	account      api.AccountResponse
	mu           sync.Mutex
	pins         map[string]api.AppReleaseReview
	beforeReview func()
	writes, cli  atomic.Int32
}

func newUploadedTaskFixture(t *testing.T) *uploadedTaskFixture {
	t.Helper()
	f := &uploadedTaskFixture{token: testToken, account: api.AccountResponse{ID: uuid.NewString(), Plan: "pro", KeyScopes: []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}}, pins: map[string]api.AppReleaseReview{}}
	for _, slug := range []string{"web", "worker", "observer", "previous"} {
		f.pins[slug] = api.AppReleaseReview{AppID: uuid.NewString(), ConfigurationHash: strings.Repeat("b", 64), Scope: api.DefaultEnvScope, ServingDeploymentIDs: []string{}}
	}
	f.backend = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.backend.Close)
	uploads, err := newUploadStore(filepath.Join(t.TempDir(), "uploads"), "https://api.example/mcp")
	if err != nil {
		t.Fatal(err)
	}
	f.opts = Options{APIBase: f.backend.URL, Token: testToken, Toolsets: []string{ToolsetBuildTools}, AllowWrites: true, AllowToolPublishing: true, agentKey: true, uploads: uploads, accountID: f.account.ID, grantID: "task-review-key", LocalCLI: func(context.Context, []string, []byte) (LocalCLIResult, error) {
		f.cli.Add(1)
		return LocalCLIResult{}, errors.New("Task review must not invoke CLI or customer code")
	}}
	return f
}

func (f *uploadedTaskFixture) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		w.WriteHeader(401)
		return
	}
	if r.Method != http.MethodGet {
		f.writes.Add(1)
		w.WriteHeader(500)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/v1/account" {
		_ = json.NewEncoder(w).Encode(f.account)
		return
	}
	for slug, pin := range f.pins {
		if r.URL.Path == "/v1/apps/"+slug+"/release-review" {
			if f.beforeReview != nil {
				f.beforeReview()
			}
			_ = json.NewEncoder(w).Encode(pin)
			return
		}
	}
	w.WriteHeader(404)
}

func uploadedTaskArgs(t *testing.T) planUploadedTaskArgs {
	t.Helper()
	enabled := true
	cfg := mcphosting.Config{Version: 1, Endpoint: "/mcp", Transport: "streamable-http", Mode: "stateless", AllowedOrigins: []string{}, Auth: mcphosting.AuthConfig{Mode: "open", ToolScopes: mcphosting.ScopePolicy{"report": {}}}, Tasks: &mcphosting.TasksConfig{Enabled: &enabled, DatabaseURLEnv: "DATABASE_URL", OwnerKeyEnv: "MCP_TASK_OWNER_KEY", NamespaceEnv: "MCP_TASK_NAMESPACE", TTLSeconds: 3600, WorkerConcurrency: 2}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cfg)
	files := func() []inlineFile {
		return []inlineFile{{Path: mcphosting.ConfigFile, Content: string(raw)}, {Path: "package.json", Content: `{"scripts":{"start":"node server.js","preinstall":"exit 93"}}`}, {Path: "server.js", Content: "throw new Error('customer source must not execute');"}, {Path: "tasks-release.js", Content: "throw new Error('migration adapter must not execute');"}}
	}
	return planUploadedTaskArgs{Web: uploadedTaskSource{App: "web", Files: files()}, Worker: uploadedTaskSource{App: "worker", Files: files()}, ObserverApp: "observer", ObserverMetricApp: "worker", PreviousWorkerApps: []string{"previous"}}
}

func (f *uploadedTaskFixture) archived(t *testing.T, source uploadedTaskSource, extra ...tarEntry) uploadedTaskSource {
	t.Helper()
	up, uploadURL, err := f.opts.uploads.create(f.opts.grantID, f.account.ID, source.App, defaultSourceLimit)
	if err != nil {
		t.Fatal(err)
	}
	entries := []tarEntry{}
	for _, file := range source.Files {
		entries = append(entries, tarEntry{name: file.Path, typeflag: tar.TypeReg, body: file.Content})
	}
	entries = append(entries, extra...)
	parsed, err := url.Parse(uploadURL)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, parsed.RequestURI(), bytes.NewReader(tarball(t, entries...)))
	f.opts.uploads.receive(response, req)
	if response.Code != http.StatusNoContent && response.Code != http.StatusCreated {
		t.Fatalf("archive upload: %d %s", response.Code, response.Body.String())
	}
	source.UploadID, source.Files = up.id, nil
	return source
}

func TestHostedUploadedTaskReviewBothProtocols(t *testing.T) {
	for _, protocol := range []string{api.MCPProtocolVersion, api.MCPLegacyProtocolVersion} {
		for _, archive := range []bool{false, true} {
			t.Run(protocol+map[bool]string{true: "/archive", false: "/inline"}[archive], func(t *testing.T) {
				f := newUploadedTaskFixture(t)
				cs, ctx := connectSelectedTestServer(t, f.backend, f.opts, protocol)
				if !listedTools(t, ctx, cs)["gregale_plan_uploaded_task_tool_deploy"] {
					t.Fatal("hosted Task planner missing")
				}
				catalog, err := cs.ListTools(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				for _, tool := range catalog.Tools {
					if tool.Name == "gregale_plan_uploaded_task_tool_deploy" && (tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || tool.Annotations.IdempotentHint) {
						t.Fatal("one-time uploaded review has misleading annotations")
					}
				}
				var first uploadedTaskReview
				for round := range 2 {
					args := uploadedTaskArgs(t)
					if archive {
						args.Web = f.archived(t, args.Web)
						args.Worker = f.archived(t, args.Worker)
					}
					if round == 1 {
						args.Web.ExpectedSHA256 = first.Sources["web"]
						args.Worker.ExpectedSHA256 = first.Sources["worker"]
					}
					body := publicationCall(t, ctx, cs, "gregale_plan_uploaded_task_tool_deploy", args, false)
					raw, _ := json.Marshal(body)
					var out uploadedTaskReview
					if err := json.Unmarshal(raw, &out); err != nil {
						t.Fatal(err)
					}
					if out.Kind != "hosted_task_source_review" || out.Status != "review_only" || out.ReadyToApply || out.BindingsVerified || !out.MigrationsRequired || out.AccountID != f.account.ID || len(out.Apps) != 4 || out.Apps[0].Review.AppID != f.pins["web"].AppID || len(out.RequiredSetup) == 0 || out.WebLimits.TTLSeconds != 3600 || out.WorkerLimits.WorkerConcurrency != 2 || out.ToolScopes["report"] == nil || len(out.Sources) != 2 || out.TimeoutSeconds != 300 {
						t.Fatalf("incomplete source/native Task review: %s", raw)
					}
					for _, forbidden := range []string{"plan_id", "intent_id", "DATABASE_URL", "MCP_TASK_OWNER_KEY", "MCP_TASK_NAMESPACE", f.opts.uploads.root} {
						if strings.Contains(string(raw), forbidden) {
							t.Fatal("review leaked private source or executable authority", forbidden)
						}
					}
					if round == 1 && out.Fingerprint != first.Fingerprint {
						t.Fatal("same source and native identities changed fingerprint across staging paths")
					}
					first = out
					if archive {
						publicationCall(t, ctx, cs, "gregale_plan_uploaded_task_tool_deploy", args, true)
					}
					f.opts.uploads.mu.Lock()
					retained := len(f.opts.uploads.dirs)
					f.opts.uploads.mu.Unlock()
					if retained != 0 {
						t.Fatal("review retained staged sources/reservations", retained)
					}
				}
				publicationCall(t, ctx, cs, "gregale_apply_tool_deploy", planLookupArgs{PlanID: first.Fingerprint}, true)
				if f.writes.Load() != 0 || f.cli.Load() != 0 {
					t.Fatal("review executed native writes, CLI, build or migrations")
				}
			})
		}
	}
}

func TestHostedUploadedTaskReviewFingerprintAndTampering(t *testing.T) {
	f := newUploadedTaskFixture(t)
	_, m, err := newManagementServer(api.NewClient(f.backend.URL, testToken), f.opts)
	if err != nil {
		t.Fatal(err)
	}
	run := func(args planUploadedTaskArgs) uploadedTaskReview {
		t.Helper()
		value, err := m.planUploadedTaskToolDeploy(t.Context(), args)
		if err != nil {
			t.Fatal(err)
		}
		return value.(uploadedTaskReview)
	}
	first := run(uploadedTaskArgs(t))
	for _, change := range []string{"source", "permissions", "limits", "app_baseline", "timeout", "mode"} {
		t.Run(change, func(t *testing.T) {
			args := uploadedTaskArgs(t)
			switch change {
			case "source":
				args.Web.Files[2].Content = "changed source bytes"
			case "permissions":
				args.Web.Files[0].Content = strings.ReplaceAll(args.Web.Files[0].Content, "report", "another_report")
			case "limits":
				args.Worker.Files[0].Content = strings.ReplaceAll(args.Worker.Files[0].Content, "\"ttl_seconds\":3600", "\"ttl_seconds\":7200")
			case "app_baseline":
				f.mu.Lock()
				pin := f.pins["web"]
				pin.LatestDeploymentID = uuid.NewString()
				f.pins["web"] = pin
				f.mu.Unlock()
				t.Cleanup(func() { f.mu.Lock(); pin.LatestDeploymentID = ""; f.pins["web"] = pin; f.mu.Unlock() })
			case "timeout":
				args.TimeoutSeconds = 180
			case "mode":
				args.Web.Files[2].Executable = true
			}
			changed := run(args)
			if changed.Fingerprint == first.Fingerprint {
				t.Fatal("review fingerprint omitted changed input", change)
			}
			if change == "source" || change == "mode" {
				args.Web.ExpectedSHA256 = first.Sources["web"]
				if _, err := m.planUploadedTaskToolDeploy(t.Context(), args); err == nil {
					t.Fatal("changed source satisfied original expected digest")
				}
			}
		})
	}
	f.mu.Lock()
	f.beforeReview = func() {
		f.opts.uploads.mu.Lock()
		defer f.opts.uploads.mu.Unlock()
		for dir := range f.opts.uploads.dirs {
			if err := os.WriteFile(filepath.Join(dir, "server.js"), []byte("mutated during native review"), 0600); err != nil {
				t.Error(err)
			}
		}
	}
	f.mu.Unlock()
	if _, err := m.planUploadedTaskToolDeploy(t.Context(), uploadedTaskArgs(t)); err == nil {
		t.Fatal("source changed during review was accepted")
	}
	if len(m.toolPublications) != 0 || len(m.taskExecutions) != 0 || f.writes.Load() != 0 || f.cli.Load() != 0 {
		t.Fatal("offline source review acquired release authority")
	}
}

func TestHostedUploadedTaskReviewRejectsUnavailableAndUnsafeSource(t *testing.T) {
	for _, scenario := range []string{"missing", "expired", "foreign_key", "foreign_account", "wrong_app", "pending", "traversal", "symlink", "invalid_config", "missing_adapter", "disabled_tasks", "mixed_runtime", "conflicting_sources", "duplicate_roles", "unknown_metric", "bad_deadline", "malformed_expected", "absent_app", "invalid_native_pin"} {
		t.Run(scenario, func(t *testing.T) {
			f := newUploadedTaskFixture(t)
			args := uploadedTaskArgs(t)
			switch scenario {
			case "missing":
				args.Web.Files = nil
				args.Web.UploadID = "missing"
			case "expired", "foreign_key", "foreign_account", "wrong_app":
				args.Web = f.archived(t, args.Web)
				f.opts.uploads.mu.Lock()
				up := f.opts.uploads.uploads[args.Web.UploadID]
				switch scenario {
				case "expired":
					up.expires = time.Now().Add(-time.Second)
				case "foreign_key":
					up.grantID = "another-connection"
				case "foreign_account":
					up.accountID = uuid.NewString()
				case "wrong_app":
					up.app = "worker"
				}
				f.opts.uploads.mu.Unlock()
			case "pending":
				up, _, err := f.opts.uploads.create(f.opts.grantID, f.account.ID, "web", defaultSourceLimit)
				if err != nil {
					t.Fatal(err)
				}
				args.Web.UploadID, args.Web.Files = up.id, nil
			case "traversal":
				args.Web.Files = append(args.Web.Files, inlineFile{Path: "../TASK_SECRET_SENTINEL", Content: "private"})
			case "symlink":
				args.Web = f.archived(t, args.Web, tarEntry{name: "escape", typeflag: tar.TypeSymlink, link: "/private/TASK_SECRET_SENTINEL"})
			case "invalid_config":
				args.Web.Files[0].Content = `{"credential":"TASK_SECRET_SENTINEL"}`
			case "missing_adapter":
				args.Worker.Files = args.Worker.Files[:3]
			case "disabled_tasks":
				args.Worker.Files[0].Content = strings.ReplaceAll(args.Worker.Files[0].Content, "\"enabled\":true", "\"enabled\":false")
			case "mixed_runtime":
				args.Web.Files = append(args.Web.Files, inlineFile{Path: "Dockerfile", Content: "FROM scratch"})
			case "conflicting_sources":
				args.Web.UploadID = "also-an-upload"
			case "duplicate_roles":
				args.Worker.App = args.Web.App
			case "unknown_metric":
				args.ObserverMetricApp = "foreign"
			case "bad_deadline":
				args.TimeoutSeconds = 601
			case "malformed_expected":
				args.Web.ExpectedSHA256 = "not-a-sha256"
			case "absent_app":
				delete(f.pins, "observer")
			case "invalid_native_pin":
				pin := f.pins["web"]
				pin.Protected = true
				f.pins["web"] = pin
			}
			_, m, err := newManagementServer(api.NewClient(f.backend.URL, testToken), f.opts)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.planUploadedTaskToolDeploy(t.Context(), args); err == nil || strings.Contains(err.Error(), "TASK_SECRET_SENTINEL") {
				t.Fatal("unsafe review succeeded or leaked private input", err)
			}
			if len(f.opts.uploads.dirs) != 0 || f.writes.Load() != 0 || f.cli.Load() != 0 {
				t.Fatal("failed review retained source or dispatched effects")
			}
		})
	}
}

func TestHostedUploadedTaskReviewConsentAndAccountRestrictions(t *testing.T) {
	for _, scenario := range []string{"publishing_off", "writes_off", "no_uploads", "app_consent", "org_consent", "environment_consent", "local", "foreign_identity", "key_project", "key_app", "key_org"} {
		t.Run(scenario, func(t *testing.T) {
			f := newUploadedTaskFixture(t)
			switch scenario {
			case "publishing_off":
				f.opts.AllowToolPublishing = false
			case "writes_off":
				f.opts.AllowWrites = false
			case "no_uploads":
				f.opts.uploads = nil
			case "app_consent":
				f.opts.AllowedApps = map[string]string{"web": "app"}
			case "org_consent":
				f.opts.Org = "shared"
			case "environment_consent":
				f.opts.Environments = &EnvironmentConsent{}
			case "local":
				f.opts.agentKey = false
			case "foreign_identity":
				f.opts.accountID = uuid.NewString()
			case "key_project":
				f.account.KeyProjects = []string{uuid.NewString()}
			case "key_app":
				f.account.KeyApp = &api.AppAgentBinding{ID: uuid.NewString(), Slug: "web"}
			case "key_org":
				f.account.KeyOrg = &api.KeyOrg{ID: uuid.NewString()}
			}
			_, m, err := newManagementServer(api.NewClient(f.backend.URL, testToken), f.opts)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.planUploadedTaskToolDeploy(t.Context(), uploadedTaskArgs(t)); err == nil {
				t.Fatal("restricted principal reviewed Task source", scenario)
			}
			if f.writes.Load() != 0 || f.cli.Load() != 0 {
				t.Fatal("restricted principal dispatched effects")
			}
		})
	}
}

func TestHostedUploadedTaskReviewRejectsHostPathsAndExecutionArguments(t *testing.T) {
	f := newUploadedTaskFixture(t)
	cs, ctx := connectSelectedTestServer(t, f.backend, f.opts, api.MCPProtocolVersion)
	raw, _ := json.Marshal(uploadedTaskArgs(t))
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"plan_path", "bindings_file", "credential", "allow_tool_execution", "release_policy"} {
		args[key] = "TASK_SECRET_SENTINEL"
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_plan_uploaded_task_tool_deploy", Arguments: args})
		if err == nil && !result.IsError {
			t.Fatal("undeclared host path or execution field accepted", key)
		}
		delete(args, key)
	}
	if f.writes.Load() != 0 || f.cli.Load() != 0 {
		t.Fatal("malformed request dispatched effects")
	}
}
