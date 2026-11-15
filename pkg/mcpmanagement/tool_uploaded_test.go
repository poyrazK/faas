package mcpmanagement

import (
	"archive/tar"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func uploadedToolFiles(t *testing.T) []inlineFile {
	t.Helper()
	dir := publicationProject(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := []inlineFile{{Path: "package.json", Content: `{"scripts":{"start":"node server.js"}}`}}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, inlineFile{Path: entry.Name(), Content: string(data)})
	}
	return files
}

func hostedPublicationOptions(t *testing.T, f *publicationFixture, backend *httptest.Server) Options {
	t.Helper()
	uploads, err := newUploadStore(filepath.Join(t.TempDir(), "uploads"), "https://api.gregale.test/mcp")
	if err != nil {
		t.Fatal(err)
	}
	return Options{APIBase: backend.URL, Token: testToken, LocalCLI: f.cli, Toolsets: []string{ToolsetBuildTools}, AllowWrites: true, AllowToolPublishing: true, agentKey: true, uploads: uploads, accountID: "account-one", grantID: "publishing-key"}
}

func uploadedToolArgs(t *testing.T) planUploadedToolArgs {
	t.Helper()
	return planUploadedToolArgs{App: "alpha", Reason: "publish reviewed Node tool", Files: uploadedToolFiles(t), AccessMode: "open", AllowToolExecution: true}
}

func uploadedRuntimeToolArgs(t *testing.T, runtime string) planUploadedToolArgs {
	t.Helper()
	a := uploadedToolArgs(t)
	a.Runtime, a.Reason = runtime, "publish reviewed "+runtime+" tool"
	for i := range a.Files {
		if a.Files[i].Path == "package.json" {
			switch runtime {
			case "go":
				a.Files[i] = inlineFile{Path: "go.mod", Content: "module tool\n\ngo 1.26.0\n"}
			case "python":
				a.Files[i] = inlineFile{Path: "requirements.txt", Content: "mcp==1.26.0\n"}
			}
		}
		if a.Files[i].Path == "server.js" && runtime != "node" {
			a.Files[i].Path = map[string]string{"go": "main.go", "python": "server.py"}[runtime]
		}
	}
	return a
}

func TestHostedToolPublicationJourney(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		for _, runtime := range []string{"node", "go", "python"} {
			for _, archive := range []bool{false, true} {
				t.Run(protocol+"/"+runtime+map[bool]string{false: "/inline", true: "/archive"}[archive], func(t *testing.T) {
					hostedToolPublicationJourney(t, protocol, runtime, archive)
				})
			}
		}
	}
}

func hostedToolPublicationJourney(t *testing.T, protocol, runtime string, archive bool) {
	t.Helper()
	f, backend := newPublicationFixture(t)
	opts := hostedPublicationOptions(t, f, backend)
	cs, ctx := connectSelectedTestServer(t, backend, opts, protocol)
	tools := listedTools(t, ctx, cs)
	for _, name := range []string{"gregale_get_tool_template", "gregale_create_source_upload", "gregale_plan_uploaded_tool_deploy", "gregale_apply_tool_deploy", "gregale_get_tool_deploy"} {
		if !tools[name] {
			t.Fatalf("missing hosted tool %s", name)
		}
	}
	for _, name := range []string{"gregale_plan_tool_deploy", "gregale_test_tool", "gregale_plan_local_deploy"} {
		if tools[name] {
			t.Fatalf("host filesystem tool listed: %s", name)
		}
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if err == nil && !result.IsError {
			t.Fatalf("unlisted %s callable", name)
		}
	}
	publicationCall(t, ctx, cs, "gregale_get_tool_template", toolTemplateArgs{Runtime: runtime}, false)
	args := uploadedRuntimeToolArgs(t, runtime)
	f.mu.Lock()
	f.sourceFile = map[string]string{"node": "server.js", "go": "main.go", "python": "server.py"}[runtime]
	f.mu.Unlock()
	if archive {
		created := callTool(t, ctx, cs, "gregale_create_source_upload", createUploadArgs{App: "alpha"}, false)
		var uploadURL string
		_ = json.Unmarshal(created["upload_url"], &uploadURL)
		_ = json.Unmarshal(created["upload_id"], &args.UploadID)
		entries := []tarEntry{}
		for _, file := range args.Files {
			entries = append(entries, tarEntry{name: file.Path, typeflag: tar.TypeReg, body: file.Content})
		}
		parsed, err := url.Parse(uploadURL)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		opts.uploads.receive(rec, httptest.NewRequest(http.MethodPut, parsed.RequestURI(), strings.NewReader(string(tarball(t, entries...)))))
		if rec.Code != http.StatusCreated {
			t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
		}
		args.Files = nil
	}
	plan := publicationCall(t, ctx, cs, "gregale_plan_uploaded_tool_deploy", args, false)
	if string(plan["runtime"]) != `"`+runtime+`"` {
		t.Fatalf("review runtime: %v", plan)
	}
	var id string
	_ = json.Unmarshal(plan["plan_id"], &id)
	raw, _ := json.Marshal(plan)
	if strings.Contains(string(raw), opts.uploads.root) || strings.Contains(string(raw), "node server.js") {
		t.Fatal("review leaked host path or source")
	}
	f.mu.Lock()
	if f.deploys != 0 || f.tests != 0 {
		t.Fatal("planning executed customer code")
	}
	f.mu.Unlock()
	publicationCall(t, ctx, cs, "gregale_apply_tool_deploy", applyArgs{PlanID: id}, false)
	done := waitPublication(t, ctx, cs, id)
	if string(done["status"]) != `"completed"` {
		t.Fatalf("publication: %v", done)
	}
	fresh, freshCtx := connectSelectedTestServer(t, backend, opts, protocol)
	recovered := publicationCall(t, freshCtx, fresh, "gregale_get_tool_deploy", planLookupArgs{PlanID: id}, false)
	if string(recovered["durable"]) != "true" || string(recovered["status"]) != `"completed"` || !strings.Contains(string(recovered["result"]), "published") {
		t.Fatalf("recovery: %v", recovered)
	}
	publicationCall(t, freshCtx, fresh, "gregale_apply_tool_deploy", applyArgs{PlanID: id}, false)
	reader, readerCtx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, Toolsets: []string{ToolsetRegistry}}, protocol)
	described := publicationCall(t, readerCtx, reader, "gregale_describe_tool", describeToolArgs{Version: id, Name: "add"}, false)
	if string(described["execution_authorized"]) != "false" || string(described["version"]) != `"`+id+`"` || string(described["deployment_id"]) != `"published"` {
		t.Fatal("published version did not retain discovery/invocation separation")
	}
	f.mu.Lock()
	if f.deploys != 1 || f.observedSource != "v1" {
		t.Fatalf("dispatches=%d source=%q", f.deploys, f.observedSource)
	}
	f.mu.Unlock()
	opts.uploads.mu.Lock()
	if len(opts.uploads.dirs) != 0 {
		t.Fatal("publication retained consumed upload source")
	}
	opts.uploads.mu.Unlock()
}

func TestHostedToolPublicationRefusesInvalidSourceAndConsent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*planUploadedToolArgs)
	}{
		{"missing source", func(a *planUploadedToolArgs) { a.Files = nil }},
		{"both source kinds", func(a *planUploadedToolArgs) { a.UploadID = "unknown" }},
		{"private OAuth", func(a *planUploadedToolArgs) { a.AccessMode = "external-oauth" }},
		{"missing execution consent", func(a *planUploadedToolArgs) { a.AllowToolExecution = false }},
		{"traversal", func(a *planUploadedToolArgs) {
			a.Files = append(a.Files, inlineFile{Path: "../escape", Content: "bad"})
		}},
		{"duplicate path", func(a *planUploadedToolArgs) { a.Files = append(a.Files, a.Files[0]) }},
		{"missing Node project", func(a *planUploadedToolArgs) { a.Files = a.Files[1:] }},
		{"missing fixture", func(a *planUploadedToolArgs) { a.Fixtures = "missing.json" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, backend := newPublicationFixture(t)
			opts := hostedPublicationOptions(t, f, backend)
			cs, ctx := connectSelectedTestServer(t, backend, opts, "2026-07-28")
			args := uploadedToolArgs(t)
			tc.change(&args)
			publicationCall(t, ctx, cs, "gregale_plan_uploaded_tool_deploy", args, true)
			opts.uploads.mu.Lock()
			if len(opts.uploads.dirs) != 0 {
				t.Fatal("rejected source was retained")
			}
			opts.uploads.mu.Unlock()
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.deploys != 0 || f.tests != 0 {
				t.Fatal("invalid project executed")
			}
		})
	}
}

func TestHostedToolPublicationAccessAndSourceBinding(t *testing.T) {
	f, backend := newPublicationFixture(t)
	opts := hostedPublicationOptions(t, f, backend)
	for _, tc := range []struct {
		name   string
		change func(*Options)
	}{
		{"operator disabled", func(o *Options) { o.AllowToolPublishing = false }},
		{"read only", func(o *Options) { o.AllowWrites = false }},
		{"app scoped", func(o *Options) { o.AllowedApps = map[string]string{"alpha": "app-one"} }},
		{"organization", func(o *Options) { o.Org = "team" }},
		{"no default writes", func(o *Options) { o.Environments = &EnvironmentConsent{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			narrowed := opts
			tc.change(&narrowed)
			cs, ctx := connectSelectedTestServer(t, backend, narrowed, "2026-07-28")
			if tools := listedTools(t, ctx, cs); tools["gregale_plan_uploaded_tool_deploy"] || tools["gregale_create_source_upload"] {
				t.Fatal("publishing listed outside consent")
			}
			_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), narrowed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.planUploadedToolDeploy(t.Context(), uploadedToolArgs(t)); err == nil {
				t.Fatal("direct handler bypassed consent")
			}
		})
	}
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.planUploadedToolDeploy(t.Context(), uploadedToolArgs(t))
	if err != nil {
		t.Fatal(err)
	}
	id := out.(map[string]any)["plan_id"].(string)
	if m.idle(time.Now().Add(11 * time.Minute)) {
		t.Fatal("idle eviction lost unstarted publication")
	}
	if _, err = m.planToolDeploy(t.Context(), publicationArgs(publicationProject(t))); err == nil {
		t.Fatal("hosted local path accepted")
	}
	if _, err = m.testTool(t.Context(), testToolArgs{}); err == nil {
		t.Fatal("standalone hosted fixtures accepted")
	}
	m.mu.Lock()
	source := m.toolPublications[id].Path
	m.mu.Unlock()
	if err = os.WriteFile(filepath.Join(source, "server.js"), []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.applyToolDeploy(t.Context(), applyArgs{PlanID: id}); err == nil {
		t.Fatal("changed source published")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deploys != 0 {
		t.Fatal("source change reached worker")
	}
}

func TestHostedToolPublicationUploadCannotCrossConnections(t *testing.T) {
	f, backend := newPublicationFixture(t)
	opts := hostedPublicationOptions(t, f, backend)
	up, _, err := opts.uploads.create("another-key", "account-one", "alpha", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	opts.uploads.mu.Lock()
	up.received = true
	opts.uploads.mu.Unlock()
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	args := uploadedToolArgs(t)
	args.Files = nil
	args.UploadID = up.id
	if _, err = m.planUploadedToolDeploy(t.Context(), args); err == nil {
		t.Fatal("foreign upload accepted")
	}
	opts.uploads.mu.Lock()
	defer opts.uploads.mu.Unlock()
	if up.taken {
		t.Fatal("foreign upload consumed")
	}
}

func TestHostedToolPublishingOAuthScopeAndConsent(t *testing.T) {
	r := &remoteServer{opts: HTTPOptions{AllowWrites: true, AllowToolPublishing: true}}
	for _, raw := range []string{scopeRead + " " + scopePublish, scopeRead + " " + scopeInvoke} {
		if _, ok := r.requestedScopes(raw); ok {
			t.Fatalf("invalid publishing authority: %s", raw)
		}
	}
	if _, ok := r.requestedScopes(scopeRead + " " + scopeWrite + " " + scopePublish); !ok {
		t.Fatal("valid publishing scope refused")
	}
	if slices.Contains(grantScopes([]string{scopeRead, scopeWrite, scopePublish}, []string{api.ScopeAppsRead}), scopePublish) {
		t.Fatal("read key retained publishing consent")
	}
	store, _, _ := testStateStore(t)
	f := newRemoteFixtureWithOptions(t, true, HTTPOptions{Onboarding: "browser", Hosted: true, StateStore: store, AllowToolPublishing: true, UploadDir: filepath.Join(t.TempDir(), "uploads"), Toolsets: []string{ToolsetCore, ToolsetBuildTools}, CLIRunner: func(string) LocalCLI {
		return func(context.Context, []string, []byte) (LocalCLIResult, error) { return LocalCLIResult{}, nil }
	}})
	flow := f.begin(scopeRead + " " + scopeWrite + " " + scopePublish)
	start := f.submitConsent(flow, url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"start-login"}, "toolsets": {"core,build-tools"}}, f.server.URL)
	_ = start.Body.Close()
	if start.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", start.StatusCode)
	}
	review := f.consent(flow, "check-login", "", f.server.URL, flow.csrf, false)
	page, err := io.ReadAll(review.Body)
	_ = review.Body.Close()
	if err != nil || review.StatusCode != http.StatusOK || !strings.Contains(string(page), `name="allow_tool_publishing"`) {
		t.Fatal("publishing checkbox absent")
	}
	for _, tc := range []struct{ name, appAccess, defaultWrite, publish string }{
		{"missing checkbox", "all", "yes", ""}, {"app scope", "selected", "yes", "yes"}, {"missing environment write", "all", "", "yes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := f.submitConsent(flow, url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"approve"}, "app_access": {tc.appAccess}, "allow_writes": {"yes"}, "default_write": {tc.defaultWrite}, "allow_tool_publishing": {tc.publish}}, f.server.URL)
			_ = res.Body.Close()
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("unsafe approval: %d", res.StatusCode)
			}
		})
	}
	approved := f.submitConsent(flow, url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"approve"}, "app_access": {"all"}, "allow_writes": {"yes"}, "default_write": {"yes"}, "allow_tool_publishing": {"yes"}}, f.server.URL)
	_ = approved.Body.Close()
	if approved.StatusCode != http.StatusSeeOther {
		t.Fatalf("approval: %d", approved.StatusCode)
	}
	redirect, err := url.Parse(approved.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	var token tokenResponse
	res := f.exchange(redirect.Query().Get("code"), testVerifier, f.resource, f.clientID)
	decodeResponse(t, res, &token)
	_ = res.Body.Close()
	cs, ctx := f.connect(token, "2026-07-28")
	tools := listedTools(t, ctx, cs)
	if !tools["gregale_plan_uploaded_tool_deploy"] || tools["gregale_plan_tool_deploy"] || tools["gregale_invoke_tool"] {
		t.Fatal("publishing OAuth advertised wrong authority")
	}
	restored, err := NewHTTPService(f.opts)
	if err != nil || len(restored.remote.grants) != 1 {
		t.Fatal("publishing consent not retained", err)
	}
	disabled := f.opts
	disabled.AllowToolPublishing = false
	narrowed, err := NewHTTPService(disabled)
	if err != nil || len(narrowed.remote.grants) != 0 {
		t.Fatal("operator disable retained publication consent", err)
	}
}

func TestHostedToolPublicationUncertainAndFailedAttemptsNeverRepeat(t *testing.T) {
	for _, runtime := range []string{"node", "go", "python"} {
		for _, mode := range []string{"lost begin", "lost worker reply", "failed fixture"} {
			t.Run(runtime+"/"+mode, func(t *testing.T) {
				hostedToolPublicationFailedAttempt(t, runtime, mode)
			})
		}
	}
}

func hostedToolPublicationFailedAttempt(t *testing.T, runtime, mode string) {
	t.Helper()
	f, backend := newPublicationFixture(t)
	f.sourceFile = map[string]string{"node": "server.js", "go": "main.go", "python": "server.py"}[runtime]
	f.loseBegin, f.lose, f.fail = mode == "lost begin", mode == "lost worker reply", mode == "failed fixture"
	opts := hostedPublicationOptions(t, f, backend)
	cs, ctx := connectSelectedTestServer(t, backend, opts, "2026-07-28")
	plan := publicationCall(t, ctx, cs, "gregale_plan_uploaded_tool_deploy", uploadedRuntimeToolArgs(t, runtime), false)
	var id string
	_ = json.Unmarshal(plan["plan_id"], &id)
	publicationCall(t, ctx, cs, "gregale_apply_tool_deploy", applyArgs{PlanID: id}, mode == "lost begin")
	if mode != "lost begin" {
		terminal := waitPublication(t, ctx, cs, id)
		want := `"outcome_unknown"`
		if f.fail {
			want = `"failed"`
		}
		if string(terminal["status"]) != want {
			t.Fatalf("terminal: %v", terminal)
		}
	}
	fresh, freshCtx := connectSelectedTestServer(t, backend, opts, "2026-07-28")
	publicationCall(t, freshCtx, fresh, "gregale_apply_tool_deploy", applyArgs{PlanID: id}, false)
	publicationCall(t, ctx, cs, "gregale_apply_tool_deploy", applyArgs{PlanID: id}, false)
	f.mu.Lock()
	defer f.mu.Unlock()
	want := 1
	if mode == "lost begin" {
		want = 0
	}
	if f.deploys != want {
		t.Fatalf("uncertain attempt executed %d times; want %d", f.deploys, want)
	}
	f.base.mu.Lock()
	defer f.base.mu.Unlock()
	if f.fail && f.base.current != "stable" {
		t.Fatal("failed publication changed the serving release")
	}
}

func TestHostedToolPublishingKeyCatalogIsNarrowed(t *testing.T) {
	f, backend := newPublicationFixture(t)
	opts := hostedPublicationOptions(t, f, backend)
	r := &remoteServer{opts: HTTPOptions{AllowToolPublishing: true, AllowWrites: true, Toolsets: []string{ToolsetBuildTools}, APIBase: backend.URL, CLIRunner: func(string) LocalCLI { return f.cli }}, uploads: opts.uploads, keyConns: map[string]*keyConnection{}}
	hash := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name    string
		limited bool
		scopes  []string
		want    bool
	}{
		{"publisher", false, []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}, true},
		{"project narrowed", true, []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}, false},
		{"read only", false, []string{api.ScopeAgent, api.ScopeAppsRead}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, release, err := r.holdKeyConnection(hash, "account-one", tc.scopes, api.NewClient(backend.URL, testToken), tc.limited)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if got := r.keyConns[hash].management.toolNames["gregale_plan_uploaded_tool_deploy"]; got != tc.want {
				t.Fatalf("publishing catalog=%v, want %v", got, tc.want)
			}
		})
	}
}
