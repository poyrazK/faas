package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const startProjectNewDeploymentID = "018f0000-0000-7000-8000-000000000006"

// Return one answer per read so editor writes happen at real prompt boundaries,
// even when bufio could otherwise prefetch all of the scripted input at once.
type startProjectInput struct {
	lines   []string
	index   int
	pending string
	before  func(int) error
}

func (r *startProjectInput) Read(p []byte) (int, error) {
	if r.pending == "" {
		if r.index == len(r.lines) {
			return 0, io.EOF
		}
		if r.before != nil {
			if err := r.before(r.index); err != nil {
				return 0, err
			}
		}
		r.pending, r.index = r.lines[r.index]+"\n", r.index+1
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

type startProjectFixture struct {
	runner                *startRunner
	stdout                *bytes.Buffer
	stderr                func() string
	current               string
	served                string
	status                int
	deployments           int
	checks                int
	configReads           int
	private               bool
	appID                 string
	environmentID         string
	deploymentStatus      string
	resuming              bool
	realUpload            bool
	sourceUploaded        bool
	ownerCheckUnsupported bool
}

func newStartProjectFixture(t *testing.T, private bool) *startProjectFixture {
	t.Helper()
	stdout, stderr := startTestEnvironment(t)
	source := startTestSource(t)
	f := &startProjectFixture{stdout: stdout, stderr: stderr, current: startResponseDeploymentID, status: http.StatusOK, private: private}
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.checks++
		if r.Header.Get("Authorization") != "" || !f.resuming && r.URL.Path != "/ready" {
			t.Error("project check changed the path or forwarded credentials")
		}
		if served := f.served; served != "missing" {
			if served == "" {
				served = f.current
			}
			w.Header().Set(api.DeploymentIDHeader, served)
		}
		w.WriteHeader(f.status)
		_, _ = fmt.Fprint(w, "project ready")
	}))
	t.Cleanup(public.Close)
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		scope := "default"
		if private {
			scope = "staging"
		}
		switch r.URL.Path {
		case "/v1/uploads":
			http.NotFound(w, r) // Exercise the normal multipart compatibility path.
		case "/v1/apps/first-app/deployments":
			if !f.realUpload || r.Method != http.MethodPost {
				t.Error("unexpected project source upload")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			parts, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			for part, err := parts.NextPart(); err == nil; part, err = parts.NextPart() {
				if part.FileName() != "" {
					data, err := io.ReadAll(part)
					if err != nil {
						t.Error(err)
						return
					}
					for name, data := range readCapturedDeployArchive(t, data) {
						if filepath.Base(name) == "new.js" && bytes.Contains(data, []byte("archive-content-marker")) {
							f.sourceUploaded = true
						}
					}
				}
			}
			f.current, f.deployments = startProjectNewDeploymentID, f.deployments+1
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: f.current, AppID: startResponseAppID, Status: "pending", Scope: scope})
		case "/v1/deployments/" + startProjectNewDeploymentID + "/logs":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: status\ndata: {\"status\":\"live\"}\n\n")
		case "/v1/projects/shop":
			_ = json.NewEncoder(w).Encode(api.ProjectResponse{ProjectSummaryResponse: api.ProjectSummaryResponse{ID: startResponseProjectID, Slug: "shop"}, Workloads: []api.ProjectWorkloadResponse{{Slug: "first-app"}}})
		case "/v1/projects/shop/environments/staging":
			id := startResponseEnvironmentID
			if f.environmentID != "" {
				id = f.environmentID
			}
			_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentResponse{ID: id, ProjectID: startResponseProjectID, Slug: "staging"})
		case "/v1/apps/first-app":
			if private && r.URL.Query().Get("environment") != scope {
				t.Error("project update used the wrong environment")
			}
			id := startResponseAppID
			if f.appID != "" {
				id = f.appID
			}
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id, Slug: "first-app", CanonicalURL: public.URL, RequireAuthn: private})
		case "/v1/apps/first-app/secrets":
			f.configReads++
			if r.URL.Query().Get("scope") != scope {
				t.Error("project update read configuration from another environment")
			}
			_ = json.NewEncoder(w).Encode(api.AppSecretListResponse{Secrets: []api.AppSecretResponse{{Key: "FAAS_TEST_ALPHA", Scope: scope}}})
		case "/v1/apps/first-app/env":
			_ = json.NewEncoder(w).Encode(api.AppEnvListResponse{})
		case "/v1/deployments/" + startProjectNewDeploymentID:
			status := statusLive
			if f.deploymentStatus != "" {
				status = f.deploymentStatus
			}
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: f.current, AppID: startResponseAppID, Status: status, Scope: scope})
		case "/v1/apps/first-app/deployments/" + startResponseDeploymentID + "/response-check", "/v1/apps/first-app/deployments/" + startProjectNewDeploymentID + "/response-check":
			f.checks++
			if f.ownerCheckUnsupported {
				http.NotFound(w, r)
				return
			}
			var request api.DeploymentResponseCheckRequest
			path := "/ready"
			if f.resuming {
				path = f.runner.suggestedRequestPath()
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.AppID != startResponseAppID || request.Scope != scope || request.EnvironmentID != f.runner.session.EnvironmentID || request.Path != path || !strings.Contains(r.URL.Path, "/"+f.current+"/") {
				t.Errorf("update checked another target: %+v %s", request, r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(api.DeploymentResponseCheckResult{AppID: request.AppID, Scope: request.Scope, ProjectID: request.ProjectID, EnvironmentID: request.EnvironmentID, DeploymentID: f.current, InstanceID: startResponseInstanceID, HTTPStatus: f.status, Body: "project ready"})
		case "/v1/apps/first-app/logs":
			w.Header().Set("Content-Type", "text/event-stream")
		default:
			if r.Method != http.MethodGet {
				t.Errorf("unexpected mutation %s %s", r.Method, r.URL.Path)
			}
			http.NotFound(w, r)
		}
	})
	runner := startTestRunner(t, source, "")
	runner.account.Plan = string(api.PlanPro)
	runner.session.AppID, runner.session.DeploymentID, runner.session.Status, runner.session.HealthPath = startResponseAppID, f.current, statusLive, "/ready"
	if private {
		startTestLink(t, source, "shop", "first-app", "staging")
		_, key, err := startSourceContext(source)
		if err != nil {
			t.Fatal(err)
		}
		runner.session.SourceContext, runner.session.Project, runner.session.ProjectID = key, "shop", startResponseProjectID
		runner.session.Environment, runner.session.EnvironmentID = "staging", startResponseEnvironmentID
	}
	baseline, err := runner.snapshotSource(runner.ctx)
	if err != nil {
		t.Fatal(err)
	}
	runner.sourceBaseline, f.runner = baseline, runner
	runner.deploy = func(ctx context.Context, args []string, _ bool, executions ...deployExecution) int {
		f.deployments++
		if _, err := executions[0].resolveApp(ctx, runner.client, api.CreateAppRequest{Slug: runner.session.AppSlug}); err != nil {
			return printErr("Could not verify update target", err)
		}
		if !strings.Contains(strings.Join(args, " "), "--environment="+runner.session.Environment) || !strings.Contains(strings.Join(args, " "), "--name first-app") {
			t.Error("update changed the reviewed environment or app")
		}
		saved, _, err := loadStartSession(runner.statePath)
		if err != nil || saved.DeploymentID != startResponseDeploymentID || saved.VerifiedDeploymentID != startResponseDeploymentID {
			t.Errorf("update lost the previous successful deployment before acceptance: %+v %v", saved, err)
		}
		f.current = startProjectNewDeploymentID
		executions[0].notifyQueued(api.DeploymentResponse{ID: f.current, AppID: startResponseAppID, Status: "pending", Scope: scopeOrDefault(runner.session.Environment)})
		return executions[0].onTerminal(api.DeploymentResponse{ID: f.current, AppID: startResponseAppID, Status: statusLive, Scope: scopeOrDefault(runner.session.Environment)})
	}
	return f
}

func TestStartProjectWalkthroughReviewsRedeploysAndVerifies(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "public default", true: "private staging"}[private], func(t *testing.T) {
			f := newStartProjectFixture(t, private)
			runner := f.runner
			runner.prompt.reader = bufio.NewReader(&startProjectInput{lines: []string{"2", "y", "1"}, before: func(index int) error {
				if index == 0 {
					return os.WriteFile(filepath.Join(runner.session.SourcePath, "new.js"), []byte("console.log(process.env.FAAS_TEST_ALPHA); // local-preview-content\n"), 0o644)
				}
				return nil
			}})
			if code := runner.completeLaunch(); code != 0 || f.deployments != 1 || f.checks != 2 || f.configReads != 1 {
				t.Fatalf("code=%d deployments=%d checks=%d config=%d stderr=%s stdout=%s", code, f.deployments, f.checks, f.configReads, f.stderr(), f.stdout)
			}
			if !strings.Contains(f.stdout.String(), `added    "new.js"`) || !strings.Contains(f.stdout.String(), "updated project answered") || strings.Contains(f.stdout.String()+f.stderr(), "local-preview-content") {
				t.Fatalf("missing file review or source content exposed: %s", f.stdout)
			}
			saved, _, err := loadStartSession(runner.statePath)
			if err != nil || saved.VerifiedDeploymentID != startProjectNewDeploymentID || saved.DeploymentID != startProjectNewDeploymentID {
				t.Fatalf("new verification not saved: %+v %v", saved, err)
			}
			data, _ := os.ReadFile(runner.statePath)
			if bytes.Contains(data, []byte("local-preview-content")) || bytes.Contains(data, []byte("new.js")) || bytes.Contains(data, []byte("digest")) {
				t.Fatal("source review became durable session data")
			}
		})
	}
}

func TestStartProjectWalkthroughUploadsTheReviewedWorkingFiles(t *testing.T) {
	f := newStartProjectFixture(t, false)
	_ = startTestNativeOutput(t)
	f.realUpload = true
	f.runner.deploy = cmdDeployTarballToExisting
	f.runner.prompt.reader = bufio.NewReader(&startProjectInput{lines: []string{"2", "y", "1"}, before: func(index int) error {
		if index == 0 {
			return os.WriteFile(filepath.Join(f.runner.session.SourcePath, "new.js"), []byte("// archive-content-marker\n"), 0o644)
		}
		return nil
	}})
	if code := f.runner.completeLaunch(); code != 0 || f.deployments != 1 || f.checks != 2 || !f.sourceUploaded {
		t.Fatalf("code=%d deployments=%d checks=%d uploaded=%t stderr=%s stdout=%s", code, f.deployments, f.checks, f.sourceUploaded, f.stderr(), f.stdout)
	}
}

func TestStartProjectTargetResolverCannotFanOutFunctions(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	_ = startTestNativeOutput(t)
	source := writeFunctionProject(t, "functions:\n  - name: first\n    root: first\n  - name: second\n    root: second\n", "first", "second")
	withCwd(t, source)
	var mutations atomic.Int32
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations.Add(1)
		}
		http.NotFound(w, r)
	})
	resolved := 0
	code := cmdDeployTarballToExisting(t.Context(), []string{"--path", source, "--name", "first-app", "--source=worktree", "--yes"}, false, deployExecution{
		resolveApp: func(context.Context, *Client, api.CreateAppRequest) (api.AppResponse, error) {
			resolved++
			return api.AppResponse{ID: startResponseAppID, Slug: "first-app"}, nil
		},
	})
	if code == 0 || mutations.Load() != 0 || resolved != 0 || !strings.Contains(stderr(), "guided launch targets one app") {
		t.Fatalf("code=%d mutations=%d resolved=%d stderr=%s stdout=%s", code, mutations.Load(), resolved, stderr(), stdout)
	}
}

func TestStartProjectWalkthroughDeclinesAndChangedTargetsDoNotDeploy(t *testing.T) {
	for _, outcome := range []string{"finish", "EOF", "decline", "confirmation EOF", "late source change", "changed app", "changed link", "recreated environment"} {
		t.Run(outcome, func(t *testing.T) {
			f := newStartProjectFixture(t, outcome == "recreated environment")
			lines := []string{"2", "y"}
			switch outcome {
			case "finish":
				lines = []string{"1"}
			case "EOF":
				lines = nil
			case "decline":
				lines[1] = "n"
			case "confirmation EOF":
				lines = lines[:1]
			}
			f.runner.prompt.reader = bufio.NewReader(&startProjectInput{lines: lines, before: func(index int) error {
				if index == 1 && outcome == "late source change" {
					return os.WriteFile(filepath.Join(f.runner.session.SourcePath, "late.txt"), []byte("changed after preview"), 0o644)
				}
				if index != 0 {
					return nil
				}
				switch outcome {
				case "changed app":
					f.appID = startResponseInstanceID
				case "recreated environment":
					f.environmentID = startResponseInstanceID
				case "changed link":
					_, err := saveProjectContext(f.runner.session.SourcePath, localProjectContext{Version: projectContextVersion, Project: "shop", App: "first-app", Environment: "staging"})
					return err
				}
				return nil
			}})
			code := f.runner.completeLaunch()
			failed := outcome == "late source change" || outcome == "changed app" || outcome == "changed link" || outcome == "recreated environment"
			if (code != 0) != failed || f.deployments != 0 || f.checks != 1 {
				t.Fatalf("code=%d deployments=%d checks=%d stderr=%s stdout=%s", code, f.deployments, f.checks, f.stderr(), f.stdout)
			}
			saved, _, err := loadStartSession(f.runner.statePath)
			if err != nil || saved.DeploymentID != startResponseDeploymentID || saved.VerifiedDeploymentID != startResponseDeploymentID {
				t.Fatalf("declined update replaced successful deployment: %+v %v", saved, err)
			}
		})
	}
}

func TestStartProjectWalkthroughFailureKeepsAcceptedAndVerifiedDeployments(t *testing.T) {
	for _, outcome := range []string{"submission failed", "build failed", "build recovery EOF", "interrupted", "interrupted missing evidence", "HTTP failure", "stale response", "missing deployment evidence"} {
		t.Run(outcome, func(t *testing.T) {
			f := newStartProjectFixture(t, false)
			original := f.runner.deploy
			f.runner.deploy = func(ctx context.Context, args []string, existing bool, executions ...deployExecution) int {
				switch outcome {
				case "submission failed":
					f.deployments++
					return 2
				case "build failed", "build recovery EOF", "interrupted", "interrupted missing evidence":
					f.deployments++
					f.current = startProjectNewDeploymentID
					executions[0].notifyQueued(api.DeploymentResponse{ID: f.current, AppID: startResponseAppID, Status: "pending"})
					if strings.HasPrefix(outcome, "interrupted") {
						return 130
					}
					f.deploymentStatus = deploymentStatusFailed
					return executions[0].onTerminal(api.DeploymentResponse{ID: f.current, AppID: startResponseAppID, Status: deploymentStatusFailed})
				default:
					return original(ctx, args, existing, executions...)
				}
			}
			lines := []string{"2", "y", "4"} // Finish a failed response check.
			if outcome == "build failed" {
				lines[2] = "3" // Finish build recovery.
			} else if outcome == "build recovery EOF" {
				lines = lines[:2]
			}
			f.runner.prompt.reader = bufio.NewReader(&startProjectInput{lines: lines, before: func(index int) error {
				if index == 1 {
					switch outcome {
					case "HTTP failure":
						f.status = http.StatusServiceUnavailable
					case "stale response":
						f.served = startResponseDeploymentID
					case "missing deployment evidence":
						f.served = "missing"
						f.ownerCheckUnsupported = true
					}
				}
				return nil
			}})
			code := f.runner.completeLaunch()
			if code == 0 || strings.HasPrefix(outcome, "interrupted") && code != 130 || f.deployments != 1 {
				t.Fatalf("code=%d deployments=%d stderr=%s stdout=%s", code, f.deployments, f.stderr(), f.stdout)
			}
			saved, _, err := loadStartSession(f.runner.statePath)
			want := startProjectNewDeploymentID
			if outcome == "submission failed" {
				want = startResponseDeploymentID
			}
			if err != nil || saved.DeploymentID != want || saved.VerifiedDeploymentID != startResponseDeploymentID {
				t.Fatalf("failed update lost accepted ID or previous verification: %+v %v", saved, err)
			}
			if want != startResponseDeploymentID && !strings.Contains(f.stdout.String(), "Previous successful check:") {
				t.Fatalf("missing recovery command for previous verification: %s", f.stdout)
			}
			if strings.HasPrefix(outcome, "interrupted") {
				if err := os.RemoveAll(saved.SourcePath); err != nil {
					t.Fatal(err)
				}
				resumed := startTestRunner(t, saved.SourcePath, "")
				resumed.session, resumed.statePath = saved, f.runner.statePath
				resumed.deploy = func(context.Context, []string, bool, ...deployExecution) int {
					t.Error("resuming submitted another deployment")
					return 1
				}
				if code := resumed.resume(); code != 0 {
					t.Fatalf("resume code=%d stderr=%s", code, f.stderr())
				}
				f.resuming = true
				verified, wantSuccess := want, true
				if outcome == "interrupted missing evidence" {
					f.served, verified, wantSuccess = "missing", startResponseDeploymentID, false
					f.ownerCheckUnsupported = true
				}
				if code := resumed.completeLaunch(); (code == 0) != wantSuccess || resumed.session.VerifiedDeploymentID != verified || f.deployments != 1 {
					t.Fatalf("resumed update not verified: code=%d session=%+v", code, resumed.session)
				}
			}
		})
	}
}
