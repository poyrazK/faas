// adr: 597
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const smokeTargetID = "d6e281f3-f5b2-436c-b4ad-8529a956609c"

func smokeTestTask(t *testing.T, command []string) api.AppTaskResponse {
	t.Helper()
	report := api.ServiceBindingSmokeReport{Service: "billing", TargetDeploymentID: smokeTargetID, URL: "https://billing.internal", Path: "/ready", ExpectedStatus: "200", HTTPStatus: 200, Passed: true}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return api.AppTaskResponse{ID: "smoke-task", AppID: "app-1", DeploymentID: pinnedBindingDeployment, DeploymentScope: "staging", Kind: api.AppTaskKindManual,
		Command: command, Status: api.AppTaskStatusSucceeded, ExitCode: new(int), StdoutTail: string(body)}
}

func smokeTestApp() api.AppResponse {
	return api.AppResponse{ID: "app-1", Slug: "api", ServiceBindings: api.ServiceBindingsForTargets([]string{"billing"})}
}

func smokeTestDeployment() api.DeploymentResponse {
	return api.DeploymentResponse{ID: pinnedBindingDeployment, AppID: "app-1", Scope: "staging", Status: "live", ImageDigest: "sha256:materialized"}
}

func smokeTestArgs() []string {
	return []string{"--json", "bindings", "smoke", "api", "billing", "--caller-deployment", pinnedBindingDeployment, "--target-deployment", smokeTargetID, "--path", "/ready?token=do-not-report", "--expect-status", "200", "--poll-interval", "1ms", "--wait-timeout", "1s"}
}

func TestCmdBindingsSmokePinsCallerByIDOrRevision(t *testing.T) {
	for _, ref := range []string{pinnedBindingDeployment, "v12", strings.ReplaceAll(pinnedBindingDeployment, "-", "")} {
		t.Run(ref, func(t *testing.T) {
			var admitted api.CreateAppTaskRequest
			polls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/apps/api":
					_ = json.NewEncoder(w).Encode(smokeTestApp())
				case "/v1/apps/api/deployments":
					_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: pinnedBindingDeployment, Revision: 12}}})
				case "/v1/deployments/" + pinnedBindingDeployment:
					if len(ref) == 32 {
						api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Deployment not found", "No such deployment."))
						return
					}
					_ = json.NewEncoder(w).Encode(smokeTestDeployment())
				case "/v1/deployments/" + strings.ReplaceAll(pinnedBindingDeployment, "-", ""):
					deployment := smokeTestDeployment()
					deployment.ID = ref
					_ = json.NewEncoder(w).Encode(deployment)
				case "/v1/apps/api/tasks":
					if err := json.NewDecoder(r.Body).Decode(&admitted); err != nil {
						t.Error(err)
					}
					task := smokeTestTask(t, admitted.Command)
					if len(ref) == 32 {
						task.DeploymentID = ref
					}
					task.Status = api.AppTaskStatusQueued
					_ = json.NewEncoder(w).Encode(task)
				case "/v1/apps/api/tasks/smoke-task":
					polls++
					task := smokeTestTask(t, admitted.Command)
					if len(ref) == 32 {
						task.DeploymentID = ref
					}
					_ = json.NewEncoder(w).Encode(task)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			args := smokeTestArgs()
			args[6] = ref
			code, out, errOut := captureBindingCLI(t, srv.URL, args...)
			if code != 0 || admitted.SmokeDeploymentID != pinnedBindingDeployment || admitted.VerificationDeploymentID != "" || polls != 1 || strings.Contains(out+errOut, "do-not-report") {
				t.Fatalf("exit=%d admitted=%+v polls=%d output=%s %s", code, admitted, polls, out, errOut)
			}
			var report api.ServiceBindingSmokeReport
			if err := json.Unmarshal([]byte(out), &report); err != nil || !report.Passed || !sameBindingDeployment(report.CallerDeploymentID, pinnedBindingDeployment) || report.TargetDeploymentID != smokeTargetID {
				t.Fatalf("report=%+v err=%v", report, err)
			}
		})
	}
}

func TestCmdBindingsSmokeRejectsUntrustedResults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*api.AppTaskResponse, *api.ServiceBindingSmokeReport)
	}{
		{"truncated", func(t *api.AppTaskResponse, _ *api.ServiceBindingSmokeReport) { t.OutputTruncated = true }},
		{"failed task", func(t *api.AppTaskResponse, _ *api.ServiceBindingSmokeReport) { t.Status = api.AppTaskStatusFailed }},
		{"exit error", func(t *api.AppTaskResponse, _ *api.ServiceBindingSmokeReport) { n := 1; t.ExitCode = &n }},
		{"missing exit", func(t *api.AppTaskResponse, _ *api.ServiceBindingSmokeReport) { t.ExitCode = nil }},
		{"task failure", func(t *api.AppTaskResponse, _ *api.ServiceBindingSmokeReport) {
			t.Failure = &api.AppTaskFailure{Code: "failure", Message: "do-not-report"}
		}},
		{"wrong service", func(_ *api.AppTaskResponse, r *api.ServiceBindingSmokeReport) { r.Service = "other" }},
		{"wrong target", func(_ *api.AppTaskResponse, r *api.ServiceBindingSmokeReport) {
			r.TargetDeploymentID = pinnedBindingDeployment
		}},
		{"wrong caller", func(_ *api.AppTaskResponse, r *api.ServiceBindingSmokeReport) { r.CallerDeploymentID = smokeTargetID }},
		{"wrong app", func(_ *api.AppTaskResponse, r *api.ServiceBindingSmokeReport) { r.App = "other" }},
		{"wrong task", func(_ *api.AppTaskResponse, r *api.ServiceBindingSmokeReport) { r.TaskID = "other" }},
		{"wrong URL", func(_ *api.AppTaskResponse, r *api.ServiceBindingSmokeReport) { r.URL = "https://outside.example" }},
		{"wrong path", func(_ *api.AppTaskResponse, r *api.ServiceBindingSmokeReport) { r.Path = "/ready?token=do-not-report" }},
		{"wrong policy", func(_ *api.AppTaskResponse, r *api.ServiceBindingSmokeReport) { r.ExpectedStatus = "2xx" }},
		{"wrong HTTP status", func(_ *api.AppTaskResponse, r *api.ServiceBindingSmokeReport) { r.HTTPStatus = 500 }},
		{"missing HTTP status", func(_ *api.AppTaskResponse, r *api.ServiceBindingSmokeReport) { r.HTTPStatus = 0 }},
		{"guest failure", func(_ *api.AppTaskResponse, r *api.ServiceBindingSmokeReport) { r.Passed = false }},
		{"guest error", func(_ *api.AppTaskResponse, r *api.ServiceBindingSmokeReport) { r.Error = "do-not-report" }},
		{"invalid JSON", func(t *api.AppTaskResponse, _ *api.ServiceBindingSmokeReport) { t.StdoutTail = `{"passed":true,` }},
		{"missing JSON", func(t *api.AppTaskResponse, _ *api.ServiceBindingSmokeReport) { t.StdoutTail = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/apps/api":
					_ = json.NewEncoder(w).Encode(smokeTestApp())
				case "/v1/deployments/" + pinnedBindingDeployment:
					_ = json.NewEncoder(w).Encode(smokeTestDeployment())
				case "/v1/apps/api/tasks":
					var request api.CreateAppTaskRequest
					_ = json.NewDecoder(r.Body).Decode(&request)
					task := smokeTestTask(t, request.Command)
					var report api.ServiceBindingSmokeReport
					_ = json.Unmarshal([]byte(task.StdoutTail), &report)
					original := task.StdoutTail
					tc.mutate(&task, &report)
					if task.StdoutTail == original {
						body, _ := json.Marshal(report)
						task.StdoutTail = string(body)
					}
					_ = json.NewEncoder(w).Encode(task)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			code, out, errOut := captureBindingCLI(t, srv.URL, smokeTestArgs()...)
			var report api.ServiceBindingSmokeReport
			if err := json.Unmarshal([]byte(out), &report); err != nil || code != 1 || report.Passed || report.Error == "" || strings.Contains(out+errOut, "do-not-report") {
				t.Fatalf("exit=%d report=%+v decode=%v output=%s %s", code, report, err, out, errOut)
			}
		})
	}
}

func TestCmdBindingsSmokeRejectsChangedTaskSelection(t *testing.T) {
	for _, when := range []string{"admission", "poll"} {
		for _, field := range []string{"app", "caller", "scope", "command", "task ID"} {
			t.Run(when+" "+field, func(t *testing.T) {
				cancels := 0
				var command []string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/v1/apps/api":
						_ = json.NewEncoder(w).Encode(smokeTestApp())
						return
					case "/v1/deployments/" + pinnedBindingDeployment:
						_ = json.NewEncoder(w).Encode(smokeTestDeployment())
						return
					}
					if r.Method == http.MethodDelete {
						if r.URL.Path != "/v1/apps/api/tasks/smoke-task" {
							t.Errorf("cancelled another task: %s", r.URL)
						}
						cancels++
						_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: "smoke-task", Status: api.AppTaskStatusCancelled})
						return
					}
					if r.Method == http.MethodPost {
						var request api.CreateAppTaskRequest
						_ = json.NewDecoder(r.Body).Decode(&request)
						command = request.Command
					}
					task := smokeTestTask(t, slices.Clone(command))
					task.Status = api.AppTaskStatusQueued
					if when == "admission" && r.Method == http.MethodPost || when == "poll" && r.Method == http.MethodGet {
						switch field {
						case "app":
							task.AppID = "other"
						case "caller":
							task.DeploymentID = smokeTargetID
						case "scope":
							task.DeploymentScope = "production"
						case "command":
							task.Command[3] = "/other"
						case "task ID":
							if when == "poll" {
								task.ID = "other"
							} else {
								task.ID = ""
							}
						}
					}
					_ = json.NewEncoder(w).Encode(task)
				}))
				defer srv.Close()
				code, out, errOut := captureBindingCLI(t, srv.URL, smokeTestArgs()...)
				wantCancels := 1
				if when == "admission" && field == "task ID" {
					wantCancels = 0
				}
				if code != 1 || cancels != wantCancels || strings.Contains(out, `"passed":true`) {
					t.Fatalf("exit=%d cancels=%d output=%s %s", code, cancels, out, errOut)
				}
			})
		}
	}
}

func TestCancelSmokeTaskSurvivesCancelledWaitContext(t *testing.T) {
	cancels := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/apps/api/tasks/smoke-task" {
			t.Errorf("unexpected cancellation: %s %s", r.Method, r.URL)
		}
		cancels++
		_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: "smoke-task", Status: api.AppTaskStatusCancelled})
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelSmokeTask(ctx, api.NewClient(srv.URL, "test"), "api", api.AppTaskResponse{ID: "smoke-task", Status: api.AppTaskStatusRunning})
	if cancels != 1 {
		t.Fatalf("cancellation requests=%d", cancels)
	}
}

func TestCmdBindingsSmokeRejectsUnconfirmedCallerBeforeAdmission(t *testing.T) {
	for _, change := range []string{"deployment", "app", "status", "image"} {
		t.Run(change, func(t *testing.T) {
			posts := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/apps/api":
					_ = json.NewEncoder(w).Encode(smokeTestApp())
				case "/v1/deployments/" + pinnedBindingDeployment:
					deployment := smokeTestDeployment()
					switch change {
					case "deployment":
						deployment.ID = smokeTargetID
					case "app":
						deployment.AppID = "other"
					case "status":
						deployment.Status = "superseded"
					case "image":
						deployment.ImageDigest = ""
					}
					_ = json.NewEncoder(w).Encode(deployment)
				default:
					posts++
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			code, out, errOut := captureBindingCLI(t, srv.URL, smokeTestArgs()...)
			if code != 1 || posts != 0 {
				t.Fatalf("exit=%d posts=%d output=%s %s", code, posts, out, errOut)
			}
		})
	}
}

func TestCmdBindingsSmokeWaitTimeoutBoundsReads(t *testing.T) {
	for _, slowRead := range []string{"app", "task"} {
		t.Run(slowRead, func(t *testing.T) {
			posts, cancels := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if slowRead == "app" && r.URL.Path == "/v1/apps/api" || slowRead == "task" && r.URL.Path == "/v1/apps/api/tasks/smoke-task" && r.Method == http.MethodGet {
					<-r.Context().Done()
					return
				}
				switch r.URL.Path {
				case "/v1/apps/api":
					_ = json.NewEncoder(w).Encode(smokeTestApp())
				case "/v1/deployments/" + pinnedBindingDeployment:
					_ = json.NewEncoder(w).Encode(smokeTestDeployment())
				case "/v1/apps/api/tasks":
					posts++
					var request api.CreateAppTaskRequest
					_ = json.NewDecoder(r.Body).Decode(&request)
					task := smokeTestTask(t, request.Command)
					task.Status = api.AppTaskStatusRunning
					_ = json.NewEncoder(w).Encode(task)
				default:
					if r.Method == http.MethodDelete {
						cancels++
					}
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			args := smokeTestArgs()
			// Allow setup requests to finish before testing the blocked read.
			args[len(args)-1] = "1s"
			start := time.Now()
			code, out, errOut := captureBindingCLI(t, srv.URL, args...)
			wantPosts := 0
			if slowRead == "task" {
				wantPosts = 1
			}
			if code == 0 || posts != wantPosts || cancels != 0 || time.Since(start) > 5*time.Second || strings.Contains(out, `"passed":true`) {
				t.Fatalf("exit=%d posts=%d cancels=%d output=%s %s", code, posts, cancels, out, errOut)
			}
		})
	}
}

func TestCmdBindingsSmokeRejectsConflictingTargetAliasesLocally(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "unexpected request", 500)
	}))
	defer srv.Close()
	args := append(smokeTestArgs(), "--deployment", pinnedBindingDeployment)
	code, out, errOut := captureBindingCLI(t, srv.URL, args...)
	if code != 1 || calls != 0 {
		t.Fatalf("exit=%d calls=%d output=%s %s", code, calls, out, errOut)
	}
}

func TestCompleteSmokeReportChecksDefault2xxIndependently(t *testing.T) {
	for _, status := range []int{200, 204, 299, 301, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			report := api.ServiceBindingSmokeReport{Service: "billing", TargetDeploymentID: smokeTargetID, URL: "https://billing.internal", Path: "/ready", ExpectedStatus: "2xx"}
			guest := report
			guest.Passed, guest.HTTPStatus = true, status
			body, _ := json.Marshal(guest)
			completeSmokeReport(&report, api.AppTaskResponse{Status: api.AppTaskStatusSucceeded, ExitCode: new(int), StdoutTail: string(body)}, 0)
			if report.Passed != (status >= 200 && status < 300) {
				t.Fatalf("HTTP %d report=%+v", status, report)
			}
		})
	}
}

func TestCmdBindingsSmokeInterruptDuringTaskReadCancelsAdmittedTask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process interruption is unsupported on Windows")
	}
	cancels := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/api":
			_ = json.NewEncoder(w).Encode(smokeTestApp())
		case "/v1/deployments/" + pinnedBindingDeployment:
			_ = json.NewEncoder(w).Encode(smokeTestDeployment())
		case "/v1/apps/api/tasks":
			var request api.CreateAppTaskRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			task := smokeTestTask(t, request.Command)
			task.Status = api.AppTaskStatusRunning
			_ = json.NewEncoder(w).Encode(task)
		case "/v1/apps/api/tasks/smoke-task":
			if r.Method == http.MethodDelete {
				cancels++
				_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: "smoke-task", Status: api.AppTaskStatusCancelled})
				return
			}
			process, err := os.FindProcess(os.Getpid())
			if err == nil {
				err = process.Signal(os.Interrupt)
			}
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	code, out, errOut := captureBindingCLI(t, srv.URL, smokeTestArgs()...)
	if code != 130 || cancels != 1 || strings.Contains(out, `"passed":true`) {
		t.Fatalf("exit=%d cancels=%d output=%s %s", code, cancels, out, errOut)
	}
}
