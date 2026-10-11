package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
)

// resolvedTempDir returns t.TempDir() with symlinks resolved. On macOS
// /var is a symlink to /private/var, and gregale start keys its session by
// filepath.Abs of the working directory, which resolves through it.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func startTestEnvironment(t *testing.T) (*bytes.Buffer, func() string) {
	t.Helper()
	resetJSONOut(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// macOS ignores XDG_CONFIG_HOME; HOME isolates os.UserConfigDir there.
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))
	t.Setenv("FAAS_TOKEN", "start-test-token")
	oldTTY := testOnlyTTY
	tty := true
	testOnlyTTY = &tty
	t.Cleanup(func() { testOnlyTTY = oldTTY })
	stdout, stderr, restore := swapIO(t)
	t.Cleanup(restore)
	return stdout, stderr
}

func startTestSource(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(resolvedTempDir(t), "first-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := templates.Materialize("hello-node", dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func startTestRunner(t *testing.T, source, input string) *startRunner {
	t.Helper()
	statePath, err := startSessionPath(source)
	if err != nil {
		t.Fatal(err)
	}
	return &startRunner{
		ctx:         context.Background(),
		prompt:      startPrompt{reader: bufio.NewReader(strings.NewReader(input)), writer: osStdout},
		waitTimeout: time.Second,
		account:     api.AccountResponse{ID: "acct-1", Email: "person@example.test", Plan: "free"},
		client:      NewClient(apiBase(), loadToken()), statePath: statePath,
		session: startSession{ScopePath: source, SourcePath: source, APIBase: apiBase(), AccountID: "acct-1", AppSlug: "first-app"},
	}
}

func startTestAPI(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/account" {
			_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: "acct-1", Email: "person@example.test", Plan: "free", Status: "active"})
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	return srv
}

func TestCmdStartJSONRejectsWithoutNetwork(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	jsonOutput = true
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	if code := cmdStart(nil); code == 0 {
		t.Fatal("JSON interactive command unexpectedly succeeded")
	}
	var problem api.Problem
	if err := json.Unmarshal([]byte(stderr()), &problem); err != nil {
		t.Fatalf("expected structured JSON error: %v; %s", err, stderr())
	}
	if stdout.Len() != 0 || !strings.Contains(problem.Detail, "does not support --json") {
		t.Fatalf("unexpected output: stdout=%s problem=%+v", stdout, problem)
	}
}

func TestCmdStartRequiresTerminal(t *testing.T) {
	_, stderr := startTestEnvironment(t)
	noTTY := false
	testOnlyTTY = &noTTY
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	if code := cmdStart(nil); code == 0 || !strings.Contains(stderr(), "terminal") {
		t.Fatalf("code=%d stderr=%s", code, stderr())
	}
}

func TestCmdStartRejectsOptionsBeforeNetwork(t *testing.T) {
	for _, argument := range []string{"--path", "--template", "--name", "--secrets-file", "--timeout", "--resume", "./api"} {
		t.Run(argument, func(t *testing.T) {
			stdout, stderr := startTestEnvironment(t)
			var requests atomic.Int32
			startTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			})
			if code := run([]string{"start", argument}); code == 0 || requests.Load() != 0 || stdout.Len() != 0 || !strings.Contains(stderr(), "no flags or arguments") {
				t.Fatalf("code=%d requests=%d stdout=%s stderr=%s", code, requests.Load(), stdout, stderr())
			}
		})
	}
}

func TestCmdStartHelpHasNoCommandFlags(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	if code := run([]string{"start", "--help"}); code != 0 || !strings.Contains(stdout.String(), "gregale start") || strings.Contains(stdout.String(), "Flags:") || strings.Contains(stdout.String(), "[flags]") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr())
	}
}

func TestCmdStartDeclineDoesNotMutateAPI(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	source := startTestSource(t)
	withCwd(t, source)
	var mutations atomic.Int32
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations.Add(1)
		}
		api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", "No app"))
	})
	pipeStdin(t, "\nn\n")
	if code := run([]string{"start"}); code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr(), stdout)
	}
	if mutations.Load() != 0 || !strings.Contains(stdout.String(), "Nothing deployed") {
		t.Fatalf("mutations=%d stdout=%s", mutations.Load(), stdout)
	}
}

func TestCmdStartLaunchAndPublicRequest(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	nativePath := startTestNativeOutput(t)
	source := startTestSource(t)
	withCwd(t, source)
	var requests, deployments atomic.Int32
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("account credentials sent to public app")
		}
		_, _ = fmt.Fprint(w, "Hello from Gregale!")
	}))
	defer public.Close()
	var created atomic.Bool
	statePath, err := startSessionPath(source)
	if err != nil {
		t.Fatal(err)
	}
	dep := api.DeploymentResponse{ID: "d1", AppID: "app-1", Status: "live", StageState: json.RawMessage(`{"history":[{"name":"readiness","status":"completed"}]}`)}
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/apps/first-app" && r.Method == http.MethodGet && created.Load():
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app", CanonicalURL: public.URL})
		case r.URL.Path == "/v1/apps" && r.Method == http.MethodPost:
			created.Store(true)
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app", CanonicalURL: public.URL})
		case r.URL.Path == "/v1/apps/first-app/deployments" && r.Method == http.MethodPost:
			deployments.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "app-1", Status: "pending"})
		case r.URL.Path == "/v1/deployments/d1/logs":
			saved, found, err := loadStartSession(statePath)
			if err != nil || !found || saved.DeploymentID != "d1" {
				t.Errorf("accepted ID was not saved before waiting: %+v, %t, %v", saved, found, err)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: log\ndata: {\"line\":\"raw compiler output\"}\n\n")
			for _, name := range []string{"image_build", "snapshot_prepare", "readiness"} {
				_, _ = fmt.Fprintf(w, "event: stage\ndata: {\"name\":%q,\"status\":\"in_progress\"}\n\n", name)
			}
			_, _ = fmt.Fprint(w, "event: status\ndata: {\"status\":\"live\"}\n\n")
		case r.URL.Path == "/v1/deployments/d1":
			_ = json.NewEncoder(w).Encode(dep)
		default:
			api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", "No resource"))
		}
	})
	// The healthy path needs only source selection and deployment confirmation.
	pipeStdin(t, "\ny\n")
	if code := run([]string{"start"}); code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr(), stdout)
	}
	if deployments.Load() != 1 || requests.Load() != 1 {
		t.Fatalf("deployments=%d requests=%d", deployments.Load(), requests.Load())
	}
	if !strings.Contains(stdout.String(), "HTTP 200") || !strings.Contains(stdout.String(), "Hello from Gregale!") || strings.Contains(stdout.String(), loadToken()) {
		t.Fatalf("unexpected output: %s", stdout)
	}
	for _, wanted := range []string{"Building app", "Starting app", "Checking readiness", "App ready", "Verified URL  " + public.URL + "/", "elapsed"} {
		if !strings.Contains(stdout.String(), wanted) {
			t.Errorf("missing launch result %q: %s", wanted, stdout)
		}
	}
	native, err := os.ReadFile(nativePath)
	if err != nil || strings.Contains(string(native), "raw compiler output") || strings.Contains(stdout.String(), "Deployed.") {
		t.Fatalf("compact output bypassed: native=%q stdout=%s err=%v", native, stdout, err)
	}
	for _, unwanted := range []string{"App name [", "Who should be able", "GET request path", "What next?", "--resume", "Deployment plan:", "Detected:"} {
		if strings.Contains(stdout.String(), unwanted) {
			t.Errorf("healthy path still contains %q", unwanted)
		}
	}
	saved, _, err := loadStartSession(statePath)
	if err != nil || saved.Status != "live" || saved.DeploymentID != "d1" {
		t.Fatalf("saved result=%+v err=%v", saved, err)
	}
}

func TestCmdStartResumeDoesNotDeploy(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	scope := resolvedTempDir(t)
	withCwd(t, scope)
	var mutations atomic.Int32
	var reads atomic.Int32
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(api.DeploymentIDHeader, "d1")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer public.Close()
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations.Add(1)
		}
		switch r.URL.Path {
		case "/v1/apps/first-app":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app", CanonicalURL: public.URL})
		case "/v1/deployments/d1":
			stage := "completed"
			if reads.Add(1) == 1 {
				stage = "running"
			}
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "app-1", Status: "live", StageState: json.RawMessage(fmt.Sprintf(`{"history":[{"name":"readiness","status":%q}]}`, stage))})
		case "/v1/deployments/d1/logs":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: status\ndata: {\"status\":\"live\"}\n\n")
		default:
			api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", "No resource"))
		}
	})
	path, err := startSessionPath(scope)
	if err != nil {
		t.Fatal(err)
	}
	// Recovery does not require the source files to still exist.
	session := startSession{APIBase: apiBase(), AccountID: "acct-1", ScopePath: scope, SourcePath: filepath.Join(scope, "deleted"), AppSlug: "first-app", AppID: "app-1", DeploymentID: "d1", Status: "pending"}
	if err := saveStartSession(path, session); err != nil {
		t.Fatal(err)
	}
	pipeStdin(t, "\n")
	if code := run([]string{"start"}); code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr(), stdout)
	}
	if mutations.Load() != 0 || reads.Load() < 2 || !strings.Contains(stdout.String(), "Resume · first-app") {
		t.Fatalf("mutations=%d deployment reads=%d stdout=%s", mutations.Load(), reads.Load(), stdout)
	}
}

func TestCmdStartResumeRejectsIdentityMismatch(t *testing.T) {
	for _, mismatch := range []string{"API", "account", "app"} {
		t.Run(mismatch, func(t *testing.T) {
			_, stderr := startTestEnvironment(t)
			scope := resolvedTempDir(t)
			withCwd(t, scope)
			var mutations atomic.Int32
			startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					mutations.Add(1)
				}
				if r.URL.Path == "/v1/apps/first-app" {
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app"})
				} else {
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "different-app", Status: "live"})
				}
			})
			session := startSession{APIBase: apiBase(), AccountID: "acct-1", ScopePath: scope, SourcePath: scope, AppSlug: "first-app", AppID: "app-1", DeploymentID: "d1"}
			if mismatch == "API" {
				session.APIBase = "https://another.example.test"
			} else if mismatch == "account" {
				session.AccountID = "another-account"
			}
			path, err := startSessionPath(scope)
			if err != nil {
				t.Fatal(err)
			}
			if err := saveStartSession(path, session); err != nil {
				t.Fatal(err)
			}
			pipeStdin(t, "\n")
			if code := cmdStart(nil); code == 0 || mutations.Load() != 0 {
				t.Fatalf("code=%d mutations=%d stderr=%s", code, mutations.Load(), stderr())
			}
		})
	}
}

func TestStartInterruptedDeployRetainsAcceptedID(t *testing.T) {
	_, stderr := startTestEnvironment(t)
	source := startTestSource(t)
	startTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", "No app"))
	})
	runner := startTestRunner(t, source, "y\n")
	runner.deploy = func(_ context.Context, _ []string, _ bool, executions ...deployExecution) int {
		executions[0].notifyQueued(api.DeploymentResponse{ID: "d-accepted", AppID: "app-1", Status: "pending"})
		return 130
	}
	if code, submitted := runner.reviewAndDeploy(); code != 130 || submitted {
		t.Fatalf("code=%d submitted=%t stderr=%s", code, submitted, stderr())
	}
	session, found, err := loadStartSession(runner.statePath)
	if err != nil || !found || session.DeploymentID != "d-accepted" {
		t.Fatalf("accepted ID lost: %+v %t %v", session, found, err)
	}
}

func TestStartAcceptanceSaveFailureStopsWaiting(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	source := startTestSource(t)
	startTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", "No app"))
	})
	runner := startTestRunner(t, source, "y\n")
	runner.deploy = func(ctx context.Context, _ []string, _ bool, executions ...deployExecution) int {
		// Fail only after the prepared-state write succeeded.
		if err := os.RemoveAll(filepath.Dir(runner.statePath)); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Dir(runner.statePath), []byte("blocked"), 0o600); err != nil {
			t.Fatal(err)
		}
		executions[0].notifyQueued(api.DeploymentResponse{ID: "d-accepted", AppID: "app-1", Status: "pending"})
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Error("waiting continued after losing recovery persistence")
		}
		return 130
	}
	if code, _ := runner.reviewAndDeploy(); code == 0 || !strings.Contains(stderr(), "could not be saved") || !strings.Contains(stdout.String(), "deployment wait 'd-accepted'") {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr(), stdout)
	}
}

func TestStartIntermediateLiveIsNotSuccess(t *testing.T) {
	stdout, _ := startTestEnvironment(t)
	runner := startTestRunner(t, startTestSource(t), "")
	dep := api.DeploymentResponse{ID: "d1", Status: "live", StageState: json.RawMessage(`{"history":[{"name":"readiness","status":"running"}]}`)}
	if code := runner.terminal(dep); code == 0 || strings.Contains(stdout.String(), "App ready") || strings.Contains(stdout.String(), "Deployed.") {
		t.Fatalf("premature readiness: code=%d stdout=%s", code, stdout)
	}
}

func TestStartPromptEOFNeverConfirmsAndCancellationReturns(t *testing.T) {
	prompt := startPrompt{reader: bufio.NewReader(strings.NewReader("y")), writer: io.Discard}
	if confirmed, err := prompt.confirm(context.Background(), "Deploy"); confirmed || !errors.Is(err, io.EOF) {
		t.Fatalf("partial EOF confirmed: %t, %v", confirmed, err)
	}
	read, write := io.Pipe()
	defer func() { _ = read.Close() }()
	defer func() { _ = write.Close() }()
	prompt.reader = bufio.NewReader(read)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := prompt.text(ctx, "App", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled prompt: %v", err)
	}
}

func TestStartSessionProtectionAndLock(t *testing.T) {
	startTestEnvironment(t)
	scope := resolvedTempDir(t)
	path, err := startSessionPath(scope)
	if err != nil {
		t.Fatal(err)
	}
	session := startSession{APIBase: "https://api.example.test", AccountID: "acct-1", ScopePath: scope, SourcePath: scope, AppSlug: "first-app", DeploymentID: "d1"}
	if err := saveStartSession(path, session); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("session permissions: %v, %v", info, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), loadToken()) || strings.Contains(string(data), "secrets") {
		t.Fatalf("unsafe session metadata: %s, %v", data, err)
	}
	first, err := lockStartSession(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	if second, err := lockStartSession(path); err == nil {
		_ = second.Close()
		t.Fatal("two interactive sessions acquired the same directory")
	}
}

func TestStartStarterRefusesExistingFiles(t *testing.T) {
	startTestEnvironment(t)
	source := resolvedTempDir(t)
	file := filepath.Join(source, "server.js")
	if err := os.WriteFile(file, []byte("customer work"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := startTestRunner(t, source, "2\n\n"+source+"\n")
	if code := runner.selectSource(); code == 0 {
		t.Fatal("starter overwrote a non-empty destination")
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "customer work" {
		t.Fatalf("existing work changed: %s, %v", data, err)
	}
}

func TestStartBrowserLoginKeepsPromptInputAndCredentialSeparate(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	t.Setenv("FAAS_TOKEN", "")
	setFakeKeyring(t)
	launcher := stubBrowser(t, nil)
	fastLoginPoll(t)
	plaintext := testAPIKey('c')
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli-auth/code":
			_ = json.NewEncoder(w).Encode(api.CliAuthCodeResponse{Code: "WXYZ-1234", URL: "https://api.example.test/cli-auth?code=WXYZ-1234", ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339)})
		case "/v1/cli-auth/exchange":
			_ = json.NewEncoder(w).Encode(api.CliAuthExchangeResponse{Plaintext: plaintext, KeyID: "key-1", Account: api.AccountResponse{ID: "acct-1", Email: "person@example.test", Plan: "free"}})
		default:
			api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", "No resource"))
		}
	})
	runner := startTestRunner(t, startTestSource(t), "y\nnext prompt input\n")
	if code := runner.authenticate(); code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr(), stdout)
	}
	if len(launcher.urls) != 1 || loadToken() != plaintext || strings.Contains(stdout.String(), plaintext) {
		t.Fatalf("login result: opened=%v stored=%t stdout=%s", launcher.urls, loadToken() == plaintext, stdout)
	}
	if next, err := runner.prompt.text(runner.ctx, "Next", ""); err != nil || next != "next prompt input" {
		t.Fatalf("login consumed wizard input: %q, %v", next, err)
	}
}

func TestStartSelectsWorkspaceService(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	// The runner reports the resolved working directory (macOS: /private/var).
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"private":true,"workspaces":["packages/*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"api", "web"} {
		path := filepath.Join(root, "packages", name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := templates.Materialize("hello-node", path); err != nil {
			t.Fatal(err)
		}
	}
	withCwd(t, root)
	runner := startTestRunner(t, root, "\n2\n")
	if code := runner.selectSource(); code != 0 || runner.session.SourcePath != filepath.Join(root, "packages", "web") {
		t.Fatalf("code=%d selected=%s stderr=%s stdout=%s", code, runner.session.SourcePath, stderr(), stdout)
	}
}

func TestStartPrivateRequestUsesControlPlane(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	var invocations atomic.Int32
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/first-app":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app", RequireAuthn: true, CanonicalURL: "https://must-not-contact.example.test"})
		case "/v1/apps/first-app/invoke":
			invocations.Add(1)
			var request api.InvokeRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Method != http.MethodGet || request.Path != "/health" || r.Header.Get("Authorization") != "Bearer "+loadToken() {
				t.Errorf("invalid authenticated invocation: %+v %v", request, err)
			}
			_ = json.NewEncoder(w).Encode(api.InvokeResponse{ID: "i1", Status: "completed", Result: json.RawMessage(`{"hello":"world"}`)})
		default:
			http.NotFound(w, r)
		}
	})
	runner := startTestRunner(t, startTestSource(t), "/health\n")
	if code := runner.sendTestRequest("/health", ""); code != 0 || invocations.Load() != 1 || strings.Contains(stdout.String(), loadToken()) || strings.Contains(stdout.String(), "HTTP 200") || strings.Contains(stdout.String(), "Verified URL") || !strings.Contains(stdout.String(), "Private app") {
		t.Fatalf("code=%d invocations=%d stderr=%s stdout=%s", code, invocations.Load(), stderr(), stdout)
	}
}

func TestStartPublicRequestDoesNotFollowRedirect(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	var redirected atomic.Int32
	next := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer next.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, next.URL, http.StatusFound)
	}))
	defer public.Close()
	runner := startTestRunner(t, startTestSource(t), "")
	if code := runner.publicRequest(api.AppResponse{Slug: "first-app", CanonicalURL: public.URL}, "/"); code == 0 || redirected.Load() != 0 || !strings.Contains(stdout.String(), "HTTP 302") {
		t.Fatalf("code=%d redirects=%d stderr=%s stdout=%s", code, redirected.Load(), stderr(), stdout)
	}
}

func TestStartFailureRetryTargetsSameApp(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	source := startTestSource(t)
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/first-app" {
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app"})
		} else {
			http.NotFound(w, r)
		}
	})
	runner := startTestRunner(t, source, "1\ny\n")
	runner.session.DeploymentID, runner.session.AppID, runner.session.Status = "failed-deploy", "app-1", deploymentStatusFailed
	runner.deploy = func(_ context.Context, args []string, _ bool, executions ...deployExecution) int {
		if !strings.Contains(strings.Join(args, " "), "--name first-app") {
			t.Fatalf("retry selected a different app: %v", args)
		}
		for _, arg := range args {
			if arg == "--require-authn" || arg == "--no-require-authn" {
				t.Error("default retry changed existing app access")
			}
		}
		executions[0].notifyQueued(api.DeploymentResponse{ID: "retry-deploy", AppID: "app-1", Status: "pending"})
		return executions[0].onTerminal(api.DeploymentResponse{ID: "retry-deploy", AppID: "app-1", Status: "live"})
	}
	if code, ready := runner.recoverFailure(2); code != 0 || !ready || runner.session.DeploymentID != "retry-deploy" {
		t.Fatalf("code=%d ready=%t deployment=%s stderr=%s stdout=%s", code, ready, runner.session.DeploymentID, stderr(), stdout)
	}
}
