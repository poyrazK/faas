package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
)

func startLoopbackSource(t *testing.T) string {
	t.Helper()
	source := startTestSource(t)
	path := filepath.Join(source, "handler.js")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "app.listen(port, () => {", `app.listen(port, "127.0.0.1", () => {`, 1) + "\nconst databaseHost = '127.0.0.1';\n")
	if err := os.WriteFile(path, data, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestStartGuidedLoopbackRepairRechecksAndPreservesOtherAddresses(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	source := startLoopbackSource(t)
	path := filepath.Join(source, "handler.js")
	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	runner := startTestRunner(t, source, "4\ny\n")
	report, ready, err := runner.sourcePreflight()
	if err != nil || !ready || report.HasErrors() {
		t.Fatalf("ready=%t report=%+v err=%v stderr=%s stdout=%s", ready, report, err, stderr(), stdout)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(data, []byte(`app.listen(port, "0.0.0.0"`)) || !bytes.Contains(data, []byte("databaseHost = '127.0.0.1'")) {
		t.Fatalf("incorrect reviewed repair: %s, %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != beforeInfo.Mode().Perm() {
		t.Fatalf("permissions changed: %v, %v", info, err)
	}
	if !strings.Contains(stdout.String(), "Proposed local changes") {
		t.Fatal("source was changed without displaying a preview")
	}
}

func TestStartDeclinedRepairKeepsSourceAndSessionOpen(t *testing.T) {
	startTestEnvironment(t)
	source := startLoopbackSource(t)
	path := filepath.Join(source, "handler.js")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	runner := startTestRunner(t, source, "4\nn\n3\n")
	if _, ready, err := runner.sourcePreflight(); err != nil || ready {
		t.Fatalf("declined repair: ready=%t err=%v", ready, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("declined repair changed customer source")
	}
}

func TestStartInvalidDirectoryCanChooseSourceWithoutRestart(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	source := startTestSource(t)
	runner := startTestRunner(t, filepath.Join(t.TempDir(), "missing"), "1\n"+source+"\n")
	if _, ready, err := runner.sourcePreflight(); err != nil || !ready || runner.session.SourcePath != source {
		t.Fatalf("ready=%t selected=%s err=%v stderr=%s stdout=%s", ready, runner.session.SourcePath, err, stderr(), stdout)
	}
}

func TestStartSuppliedSecretsResolveOnlyTheirKeys(t *testing.T) {
	stdout, _ := startTestEnvironment(t)
	source := startTestSource(t)
	writeFile(t, source, "needs-env.js", "console.log(process.env.FAAS_TEST_ALPHA, process.env.FAAS_TEST_BETA);\n")
	file := filepath.Join(t.TempDir(), "credentials.txt")
	writeFile(t, filepath.Dir(file), filepath.Base(file), "FAAS_TEST_ALPHA=a-value-that-must-not-be-printed\n")
	runner := startTestRunner(t, source, "3\n")
	runner.secretsFile = file
	report, ready, err := runner.sourcePreflight()
	if err != nil || ready {
		t.Fatalf("partial secrets unexpectedly resolved everything: ready=%t err=%v", ready, err)
	}
	for _, check := range report.Checks {
		if check.Name == "env-required" && (len(check.Sources) != 1 || check.Sources[0] != "FAAS_TEST_BETA") {
			t.Fatalf("remaining keys=%v", check.Sources)
		}
	}
	if strings.Contains(stdout.String(), "a-value-that-must-not-be-printed") {
		t.Fatal("secret value printed during recovery")
	}
}

func TestStartGuidedSecretsStayOutOfSourceUploadAndSession(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	source := startTestSource(t)
	withCwd(t, source)
	writeFile(t, source, "needs-env.js", "console.log(process.env.FAAS_TEST_ALPHA);\n")
	file := filepath.Join(source, "credentials.txt")
	value := "credential-only-for-the-sealed-endpoint"
	writeFile(t, source, "credentials.txt", "FAAS_TEST_ALPHA="+value+"\n")
	var created atomic.Bool
	var secrets, deployments atomic.Int32
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer public.Close()
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/apps/first-app" && created.Load():
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app", CanonicalURL: public.URL})
		case r.URL.Path == "/v1/apps" && r.Method == http.MethodPost:
			created.Store(true)
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app"})
		case r.URL.Path == "/v1/apps/first-app/secrets/FAAS_TEST_ALPHA" && r.Method == http.MethodPut:
			secrets.Add(1)
			data, err := io.ReadAll(r.Body)
			if err != nil || !bytes.Contains(data, []byte(value)) {
				t.Errorf("secrets endpoint did not receive the value: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/v1/apps/first-app/deployments" && r.Method == http.MethodPost:
			deployments.Add(1)
			reader, err := r.MultipartReader()
			if err != nil {
				t.Errorf("multipart request: %v", err)
				w.WriteHeader(400)
				return
			}
			for {
				part, err := reader.NextPart()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Errorf("multipart part: %v", err)
					break
				}
				data, err := io.ReadAll(part)
				_ = part.Close()
				if err != nil {
					t.Errorf("multipart bytes: %v", err)
				}
				if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
					entries := readCapturedDeployArchive(t, data)
					for name, content := range entries {
						if strings.HasSuffix(name, "credentials.txt") || bytes.Contains(content, []byte(value)) {
							t.Error("credentials file leaked into source archive")
						}
					}
				}
			}
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "app-1", Status: "pending"})
		case r.URL.Path == "/v1/deployments/d1/logs":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: status\ndata: {\"status\":\"live\"}\n\n")
		case r.URL.Path == "/v1/deployments/d1":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "app-1", Status: "live"})
		default:
			api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", "No resource"))
		}
	})
	pipeStdin(t, "\n4\n"+file+"\ny\n")
	if code := cmdStart(nil); code != 0 || secrets.Load() != 1 || deployments.Load() != 1 {
		t.Fatalf("code=%d secrets=%d deployments=%d stderr=%s stdout=%s", code, secrets.Load(), deployments.Load(), stderr(), stdout)
	}
	path, err := startSessionPath(source)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || bytes.Contains(data, []byte(value)) || bytes.Contains(data, []byte(file)) || strings.Contains(stdout.String()+stderr(), value) || strings.Contains(stderr(), "env-required") {
		t.Fatalf("secret metadata/output leak or duplicate doctor failure: %s, %v, stderr=%s", data, err, stderr())
	}
}

func TestStartReviewedEditRefusesChangedOrExternalFiles(t *testing.T) {
	startTestEnvironment(t)
	source := startLoopbackSource(t)
	edits := startLoopbackEdits(source, runDoctorChecks(source))
	if len(edits) != 1 {
		t.Fatalf("edits=%v", len(edits))
	}
	writeFile(t, source, "handler.js", "a newer customer edit")
	if err := applyStartFileEdit(source, edits[0]); err == nil {
		t.Fatal("overwrote a file changed after the preview")
	}
	outside := filepath.Join(t.TempDir(), "external.js")
	writeFile(t, filepath.Dir(outside), filepath.Base(outside), "outside customer work")
	if _, _, err := readStartEditableFile(source, outside); err == nil {
		t.Fatal("allowed edit outside source directory")
	}
	symlink := filepath.Join(source, "linked.js")
	if err := os.Symlink(outside, symlink); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readStartEditableFile(source, symlink); err == nil {
		t.Fatal("allowed edit through a symlink")
	}
}

func TestStartGreetingEditsAllHTTPStartersWithQuotedUnicode(t *testing.T) {
	for _, template := range []string{"hello-node", "hello-python", "hello-go"} {
		t.Run(template, func(t *testing.T) {
			startTestEnvironment(t)
			source := t.TempDir()
			if err := templates.Materialize(template, source); err != nil {
				t.Fatal(err)
			}
			greeting := "Hello \"Gregale\" — İstanbul 👋"
			edit, err := startGreetingEdit(source, template, greeting)
			if err != nil {
				t.Fatal(err)
			}
			if err := applyStartFileEdit(source, edit); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(greeting)
			if err != nil || !bytes.Contains(edit.after, encoded) || bytes.Contains(edit.after, []byte(`"hello from gregale"`)) {
				t.Fatalf("greeting wasn't quoted correctly: %s, %v", edit.after, err)
			}
			if template == "hello-go" {
				if _, err := parser.ParseFile(token.NewFileSet(), edit.path, edit.after, parser.AllErrors); err != nil {
					t.Fatalf("greeting edit produced invalid Go syntax: %v", err)
				}
			}
		})
	}
}

func TestStartRequestUsesVerifiedHealthAndChecksServingDeployment(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	var requests atomic.Int32
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/ready" || r.Header.Get("Authorization") != "" {
			t.Errorf("incorrect health request: %s", r.URL)
		}
		w.Header().Set(api.DeploymentIDHeader, "d1")
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer public.Close()
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/first-app" {
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app", CanonicalURL: public.URL})
		} else {
			http.NotFound(w, r)
		}
	})
	runner := startTestRunner(t, startTestSource(t), "\n")
	runner.session.DeploymentID, runner.session.AppID = "d1", "app-1"
	receipt := apihostingreceipt.Receipt{SchemaVersion: 1, DeploymentID: "d1", AppID: "app-1", Profile: frameworkprofile.Profile{Version: "v1", HealthPath: "/healthz"}, Smoke: apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeVerified, Path: "/ready"}}
	encoded, err := apihostingreceipt.Encode(receipt)
	if err != nil {
		t.Fatal(err)
	}
	runner.captureDeployment(api.DeploymentResponse{ID: "d1", AppID: "app-1", Revision: 7, APIHostingReceipt: encoded})
	if code := runner.sendTestRequest(runner.suggestedRequestPath(), ""); code != 0 || requests.Load() != 1 || runner.lastRequest == nil || runner.lastRequest.servedDeployment != "d1" || runner.session.Revision != 7 {
		t.Fatalf("code=%d requests=%d stderr=%s stdout=%s", code, requests.Load(), stderr(), stdout)
	}
	if runner.lastRequest.appURL != public.URL+"/ready" || !strings.Contains(stdout.String(), "Verified URL  "+public.URL+"/ready") {
		t.Fatalf("health URL was not reported accurately: result=%+v stdout=%s", runner.lastRequest, stdout)
	}
}

func TestStartRequestCannotVerifyAnotherRevisionOrOldGreeting(t *testing.T) {
	for _, failure := range []string{"revision", "greeting"} {
		t.Run(failure, func(t *testing.T) {
			stdout, _ := startTestEnvironment(t)
			runner := startTestRunner(t, startTestSource(t), "")
			runner.session.DeploymentID = "new-deploy"
			result := startRequestResult{path: "/", httpStatus: 200, body: `{"message":"new greeting"}`, servedDeployment: "new-deploy"}
			if failure == "revision" {
				result.servedDeployment = "old-deploy"
			} else {
				result.body = `{"message":"old greeting"}`
			}
			if code := runner.verifyRequest(result, "new greeting"); code == 0 || runner.lastRequest != nil || strings.Contains(stdout.String(), "Your app answered.") || strings.Contains(stdout.String(), "Verified URL") {
				t.Fatalf("false success: code=%d stdout=%s", code, stdout)
			}
		})
	}
}

func TestStartRequestUsesAcceptedPreviewInsteadOfCurrentTraffic(t *testing.T) {
	stdout, _ := startTestEnvironment(t)
	var requests atomic.Int32
	preview := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/" || r.Header.Get("Authorization") != "" {
			t.Errorf("incorrect preview request: %s", r.URL)
		}
		w.Header().Set(api.RevisionHeader, "accepted-deploy")
		_, _ = fmt.Fprint(w, `{"message":"new greeting"}`)
	}))
	defer preview.Close()
	current := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("onboarding request went to the current traffic revision instead of its accepted deployment")
		w.WriteHeader(http.StatusConflict)
	}))
	defer current.Close()
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/first-app":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app", CanonicalURL: current.URL})
		case "/v1/deployments/accepted-deploy/url":
			_ = json.NewEncoder(w).Encode(api.DeploymentPreviewURL{URL: preview.URL, Alive: true})
		default:
			http.NotFound(w, r)
		}
	})
	runner := startTestRunner(t, startTestSource(t), "")
	runner.session.DeploymentID, runner.session.AppID = "accepted-deploy", "app-1"
	if code := runner.sendTestRequest("/", "new greeting"); code != 0 || requests.Load() != 1 || runner.lastRequest == nil {
		t.Fatalf("code=%d preview requests=%d result=%+v", code, requests.Load(), runner.lastRequest)
	}
	if runner.lastRequest.appURL != preview.URL+"/" || !strings.Contains(stdout.String(), "Verified URL  "+preview.URL+"/") || strings.Contains(stdout.String(), current.URL) {
		t.Fatalf("reported URL does not match the tested preview: result=%+v stdout=%s", runner.lastRequest, stdout)
	}
}

func TestStartFailedDeploymentCanSupplySecretsBeforeRetry(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	source := startTestSource(t)
	writeFile(t, source, "needs-env.js", "console.log(process.env.FAAS_TEST_ALPHA);\n")
	file := filepath.Join(t.TempDir(), "retry-secrets.env")
	writeFile(t, filepath.Dir(file), filepath.Base(file), "FAAS_TEST_ALPHA=private-retry-value\n")
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/first-app" {
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app"})
		} else {
			http.NotFound(w, r)
		}
	})
	runner := startTestRunner(t, source, "4\n"+file+"\ny\n")
	runner.session.DeploymentID, runner.session.AppID, runner.session.Status = "failed-deploy", "app-1", deploymentStatusFailed
	deployments := 0
	runner.deploy = func(_ context.Context, args []string, _ bool, executions ...deployExecution) int {
		deployments++
		if !strings.Contains(strings.Join(args, " "), "--name first-app") || !strings.Contains(strings.Join(args, " "), "--secrets-file "+file) {
			t.Errorf("retry did not preserve app and selected secrets: %v", args)
		}
		executions[0].notifyQueued(api.DeploymentResponse{ID: "retry-deploy", AppID: "app-1", Status: "pending"})
		return executions[0].onTerminal(api.DeploymentResponse{ID: "retry-deploy", AppID: "app-1", Status: "live"})
	}
	if code, ready := runner.recoverFailure(2); code != 0 || !ready || deployments != 1 || runner.session.DeploymentID != "retry-deploy" {
		t.Fatalf("code=%d ready=%t deployments=%d stderr=%s stdout=%s", code, ready, deployments, stderr(), stdout)
	}
	if strings.Contains(stdout.String()+stderr(), "private-retry-value") {
		t.Fatal("retry printed a secret value")
	}
}

func TestStartStarterWalkthroughEditsRedeploysAndChecksGreeting(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	source := startTestSource(t)
	greeting := "My first Gregale deployment!"
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(api.DeploymentIDHeader, "new-deploy")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": greeting})
	}))
	defer public.Close()
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/first-app" {
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app", CanonicalURL: public.URL})
		} else {
			http.NotFound(w, r)
		}
	})
	runner := startTestRunner(t, source, greeting+"\ny\n")
	runner.session.Template, runner.session.DeploymentID, runner.session.AppID = "hello-node", "old-deploy", "app-1"
	deployments := 0
	runner.deploy = func(_ context.Context, args []string, _ bool, executions ...deployExecution) int {
		deployments++
		if !strings.Contains(strings.Join(args, " "), "--name first-app") {
			t.Errorf("walkthrough changed app: %v", args)
		}
		data, err := os.ReadFile(filepath.Join(source, "handler.js"))
		if err != nil || !bytes.Contains(data, []byte(greeting)) {
			t.Errorf("deployment ran before reviewed edit: %v", err)
		}
		executions[0].notifyQueued(api.DeploymentResponse{ID: "new-deploy", AppID: "app-1", Status: "pending"})
		return executions[0].onTerminal(api.DeploymentResponse{ID: "new-deploy", AppID: "app-1", Status: "live", Revision: 2})
	}
	if code := runner.starterWalkthrough(); code != 0 || deployments != 1 || runner.lastRequest == nil || !strings.Contains(stdout.String(), "saw the new greeting live") {
		t.Fatalf("code=%d deployments=%d stderr=%s stdout=%s", code, deployments, stderr(), stdout)
	}
	if strings.Count(stdout.String(), "(y/N)") != 1 || !strings.Contains(stdout.String(), "Apply this greeting and deploy a new revision") || !strings.Contains(stdout.String(), `Before: "hello from gregale"`) {
		t.Fatalf("combined preview and confirmation were not shown: %s", stdout)
	}
	path, err := startSessionPath(source)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile(path)
	if err != nil || bytes.Contains(metadata, []byte(greeting)) || bytes.Contains(metadata, []byte(`"message"`)) {
		t.Fatalf("response/greeting leaked into session: %s, %v", metadata, err)
	}
}

func TestStartDeclinedGreetingEditDoesNotDeploy(t *testing.T) {
	startTestEnvironment(t)
	source := startTestSource(t)
	startTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app"})
	})
	runner := startTestRunner(t, source, "new greeting\nn\n")
	runner.session.Template = "hello-node"
	runner.deploy = func(context.Context, []string, bool, ...deployExecution) int {
		t.Fatal("declined greeting triggered deployment")
		return 1
	}
	if code := runner.starterWalkthrough(); code != 0 {
		t.Fatalf("declining edit returned %d", code)
	}
	data, err := os.ReadFile(filepath.Join(source, "handler.js"))
	if err != nil || !bytes.Contains(data, []byte(`"hello from gregale"`)) {
		t.Fatal("declined edit changed the starter")
	}
}

func TestStartRecoveryRecheckObservesManualEdits(t *testing.T) {
	startTestEnvironment(t)
	source := startLoopbackSource(t)
	runner := startTestRunner(t, source, "")
	// Apply the customer's manual edit when the first recovery prompt reads.
	runner.prompt.reader = bufio.NewReader(&startManualEditInput{root: source})
	if _, ready, err := runner.sourcePreflight(); err != nil || !ready {
		t.Fatalf("manual recheck: ready=%t err=%v", ready, err)
	}
}

type startManualEditInput struct {
	root string
	done bool
}

func TestStartSecretQueryCannotBecomeSessionHealthMetadata(t *testing.T) {
	startTestEnvironment(t)
	runner := startTestRunner(t, startTestSource(t), "")
	receipt := apihostingreceipt.Receipt{SchemaVersion: 1, DeploymentID: "d1", AppID: "app-1", Profile: frameworkprofile.Profile{Version: "v1", HealthPath: "/health?token=must-not-persist"}, Smoke: apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeVerified, Path: "/health?token=must-not-persist"}}
	encoded, err := apihostingreceipt.Encode(receipt)
	if err != nil {
		t.Fatal(err)
	}
	runner.captureDeployment(api.DeploymentResponse{ID: "d1", AppID: "app-1", APIHostingReceipt: encoded})
	if runner.session.HealthPath != "" || safeStartHealthPath(receipt.Profile.HealthPath) {
		t.Fatal("query values became durable onboarding metadata")
	}
}

func TestStartWalkthroughEOFBeforeConfirmationKeepsOriginalSource(t *testing.T) {
	startTestEnvironment(t)
	source := startTestSource(t)
	startTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "first-app"})
	})
	runner := startTestRunner(t, source, "local greeting\n")
	runner.session.Template, runner.session.DeploymentID = "hello-node", "old-deploy"
	runner.deploy = func(context.Context, []string, bool, ...deployExecution) int {
		t.Fatal("declined deployment was submitted")
		return 1
	}
	if code := runner.starterWalkthrough(); code != 0 || runner.lastRequest != nil || runner.session.DeploymentID != "old-deploy" {
		t.Fatalf("code=%d request=%+v deployment=%s", code, runner.lastRequest, runner.session.DeploymentID)
	}
	data, err := os.ReadFile(filepath.Join(source, "handler.js"))
	if err != nil || !bytes.Contains(data, []byte(`"hello from gregale"`)) || bytes.Contains(data, []byte("local greeting")) {
		t.Fatal("closing before combined confirmation changed the starter")
	}
}

func (r *startManualEditInput) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	path := filepath.Join(r.root, "handler.js")
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, bytes.ReplaceAll(data, []byte(`"127.0.0.1"`), []byte(`"0.0.0.0"`)), 0o640); err != nil {
		return 0, err
	}
	return copy(p, "1\n"), nil
}
