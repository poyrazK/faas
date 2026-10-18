package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func startTestLink(t *testing.T, source, project, app, environment string) {
	t.Helper()
	if _, err := saveProjectContext(source, localProjectContext{Version: projectContextVersion, Project: project, App: app, Environment: environment}); err != nil {
		t.Fatal(err)
	}
}

func startTestProjectResponse(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/v1/projects/shop":
		_ = json.NewEncoder(w).Encode(api.ProjectResponse{ProjectSummaryResponse: api.ProjectSummaryResponse{ID: "project-1", Slug: "shop"}, Workloads: []api.ProjectWorkloadResponse{{Slug: "shop-api"}, {Slug: "shop-worker"}}})
	case "/v1/projects/shop/environments/staging":
		_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentResponse{ID: "env-1", ProjectID: "project-1", Slug: "staging"})
	default:
		return false
	}
	return true
}

func TestStartSelectedSourcePinsDeploymentAndConfigurationScope(t *testing.T) {
	for _, linked := range []bool{true, false} {
		t.Run(map[bool]string{true: "linked staging", false: "unlinked default"}[linked], func(t *testing.T) {
			stdout, stderr := startTestEnvironment(t)
			source := startTestSource(t)
			origin := resolvedTempDir(t)
			startTestLink(t, origin, "other-project", "other-app", "production")
			withCwd(t, origin)
			slug, environment, scope := "first-app", "", "default"
			if linked {
				startTestLink(t, source, "shop", "shop-api", "staging")
				slug, environment, scope = "shop-api", "staging", "staging"
				writeFile(t, source, "needs-env.js", "console.log(process.env.FAAS_TEST_ALPHA, process.env.FAAS_TEST_BETA);\n")
			}
			file := filepath.Join(t.TempDir(), "selected.env")
			writeFile(t, filepath.Dir(file), filepath.Base(file), "FAAS_TEST_GAMMA=private-start-value\n")
			requests, creates, uploads, seals := 0, 0, 0, 0
			public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Header.Get("Authorization") != "" || r.URL.Path != "/ready" {
					t.Error("first-response check forwarded account credentials or changed the path")
				}
				w.Header().Set(api.DeploymentIDHeader, "d-linked")
				_, _ = io.WriteString(w, "ready")
			}))
			t.Cleanup(public.Close)
			startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if startTestProjectResponse(w, r) {
					return
				}
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/v1/uploads":
					// Older servers use the normal multipart fallback.
					http.NotFound(w, r)
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/"+slug:
					if r.URL.Query().Get("environment") != environment {
						t.Errorf("app lookup environment=%q, want %q", r.URL.Query().Get("environment"), environment)
					}
					if !linked && creates == 0 {
						api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", "No app"))
						return
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: slug, CanonicalURL: "https://production.example.test"})
				case r.URL.Path == "/v1/apps/"+slug+"/secrets":
					if r.URL.Query().Get("scope") != scope {
						t.Error("secret metadata used another scope")
					}
					_ = json.NewEncoder(w).Encode(api.AppSecretListResponse{Secrets: []api.AppSecretResponse{{Key: "FAAS_TEST_BETA", Scope: scope}}})
				case r.URL.Path == "/v1/apps/"+slug+"/env":
					if r.URL.Query().Get("scope") != scope {
						t.Error("variable metadata used another scope")
					}
					_ = json.NewEncoder(w).Encode(api.AppEnvListResponse{Env: []api.AppEnvResponse{{Key: "FAAS_TEST_ALPHA"}}})
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps":
					creates++
					if linked {
						t.Error("linked launch tried to create an app")
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: slug})
				case r.Method == http.MethodPut && r.URL.Path == "/v1/apps/"+slug+"/secrets/FAAS_TEST_GAMMA":
					seals++
					if scopeOrDefault(r.URL.Query().Get("scope")) != scope {
						t.Error("secret upload used another environment")
					}
					w.WriteHeader(http.StatusNoContent)
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/"+slug+"/deployments":
					uploads++
					if err := r.ParseMultipartForm(4 << 20); err != nil {
						t.Error(err)
						return
					}
					defer func() { _ = r.MultipartForm.RemoveAll() }()
					if seals != 1 || r.FormValue("environment") != environment {
						t.Errorf("deployment target or sealing order changed: env=%q seals=%d", r.FormValue("environment"), seals)
					}
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d-linked", AppID: "app-1", Scope: scope, Status: "pending"})
				case r.URL.Path == "/v1/deployments/d-linked/logs":
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "event: status\ndata: {\"status\":\"live\"}\n\n")
				case r.URL.Path == "/v1/deployments/d-linked":
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d-linked", AppID: "app-1", Scope: scope, Status: "live"})
				case r.URL.Path == "/v1/deployments/d-linked/url":
					_ = json.NewEncoder(w).Encode(api.DeploymentPreviewURL{DeploymentID: "d-linked", AppID: "app-1", URL: public.URL, Alive: true})
				default:
					if r.Method != http.MethodGet {
						t.Errorf("unexpected mutation: %s %s", r.Method, r.URL)
					}
					http.NotFound(w, r)
				}
			})
			runner := startTestRunner(t, source, "y\n")
			runner.secretsFile, runner.deploy = file, cmdDeployTarballToExisting
			code, submitted := runner.reviewAndDeploy()
			if code != 0 || !submitted || uploads != 1 || runner.session.AppSlug != slug || runner.session.Environment != environment {
				t.Fatalf("code=%d submitted=%t uploads=%d target=%+v stderr=%s stdout=%s", code, submitted, uploads, runner.session, stderr(), stdout)
			}
			if code := runner.sendTestRequest("/ready", ""); code != 0 || requests != 1 {
				t.Fatalf("accepted deployment was not tested: code=%d requests=%d stderr=%s", code, requests, stderr())
			}
			if code := runner.resume(); code != 0 || uploads != 1 {
				t.Fatalf("scoped recovery resubmitted or failed: code=%d uploads=%d stderr=%s", code, uploads, stderr())
			}
			runner.finish()
			state, _ := os.ReadFile(runner.statePath)
			if strings.Contains(stdout.String()+stderr()+string(state), "private-start-value") {
				t.Fatal("scope handling exposed a secret")
			}
			if !strings.Contains(stdout.String(), "--environment="+recoveryArgument(environment)) || !strings.Contains(stdout.String(), "--deployment d-linked") {
				t.Fatal("follow-up commands lost the reviewed target")
			}
			if linked && (!strings.Contains(stdout.String(), "Project  shop (linked)") || strings.Contains(stdout.String(), "already exists in this account") || strings.Contains(stdout.String(), "gregale open shop-api")) {
				t.Fatal("linked review or follow-up could send the user to the default app")
			}
		})
	}
}

func TestStartLinkedWorkloadSelection(t *testing.T) {
	for _, input := range []string{"2\n", "\n", ""} {
		t.Run(input, func(t *testing.T) {
			startTestEnvironment(t)
			source := startTestSource(t)
			startTestLink(t, source, "shop", "", "staging")
			before, _ := os.ReadFile(projectContextPath(source))
			startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("workload selection mutated remote state")
				}
				if !startTestProjectResponse(w, r) {
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "worker-1", Slug: "shop-worker"})
				}
			})
			runner := startTestRunner(t, source, input)
			err := runner.resolveLaunchTarget()
			if input == "2\n" {
				if err != nil || runner.session.AppSlug != "shop-worker" || runner.session.Environment != "staging" {
					t.Fatalf("wrong workload selected: session=%+v err=%v", runner.session, err)
				}
			} else if !errors.Is(err, io.EOF) {
				t.Fatalf("finish/EOF selected a workload: %v", err)
			}
			after, _ := os.ReadFile(projectContextPath(source))
			if !bytes.Equal(before, after) {
				t.Fatal("one launch changed the checkout's linked defaults")
			}
		})
	}
}

func TestStartLinkedTargetRejectsInvalidContext(t *testing.T) {
	for _, failure := range []string{"malformed link", "missing workload", "wrong project", "wrong environment"} {
		t.Run(failure, func(t *testing.T) {
			startTestEnvironment(t)
			source := startTestSource(t)
			startTestLink(t, source, "shop", "shop-api", "staging")
			if failure == "malformed link" {
				writeFile(t, source, ".gregale/project.json", "{")
			}
			startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || strings.HasPrefix(r.URL.Path, "/v1/apps") {
					t.Error("invalid linked context reached app resolution or a mutation")
				}
				if r.URL.Path == "/v1/projects/shop" {
					slug, workloads := "shop", []api.ProjectWorkloadResponse{{Slug: "shop-api"}}
					if failure == "wrong project" {
						slug = "other"
					}
					if failure == "missing workload" {
						workloads = nil
					}
					_ = json.NewEncoder(w).Encode(api.ProjectResponse{ProjectSummaryResponse: api.ProjectSummaryResponse{ID: "project-1", Slug: slug}, Workloads: workloads})
				} else {
					_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentResponse{ID: "env-1", ProjectID: "other-project", Slug: "staging"})
				}
			})
			runner := startTestRunner(t, source, "")
			if err := runner.resolveLaunchTarget(); err == nil {
				t.Fatal("invalid linked context was accepted")
			}
		})
	}
}

func TestStartLinkedTargetChangeAfterReviewStopsMutation(t *testing.T) {
	startTestEnvironment(t)
	source := startTestSource(t)
	startTestLink(t, source, "shop", "shop-api", "staging")
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("changed target caused a remote mutation")
		}
		if !startTestProjectResponse(w, r) {
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "shop-api"})
		}
	})
	runner := startTestRunner(t, source, "")
	if err := runner.resolveLaunchTarget(); err != nil {
		t.Fatal(err)
	}
	startTestLink(t, source, "shop", "shop-api", "production")
	if _, err := runner.resolveDeployTarget(context.Background(), runner.client, api.CreateAppRequest{Slug: "shop-api"}); err == nil {
		t.Fatal("link change after review did not stop deployment")
	}
}

func TestStartEnvironmentRequestNeverFallsBackToDefault(t *testing.T) {
	for _, failure := range []string{"missing preview", "wrong app URL", "wrong deployment URL"} {
		t.Run(failure, func(t *testing.T) {
			stdout, stderr := startTestEnvironment(t)
			requests := 0
			production := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
			t.Cleanup(production.Close)
			startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("environment check invoked the default environment")
				}
				if r.URL.Path == "/v1/apps/shop-api" {
					if r.URL.Query().Get("environment") != "staging" {
						t.Error("environment access check was unscoped")
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "shop-api", RequireAuthn: failure == "private environment", CanonicalURL: production.URL})
				} else if failure == "wrong app URL" || failure == "wrong deployment URL" {
					preview := api.DeploymentPreviewURL{DeploymentID: "d1", AppID: "app-1", URL: production.URL, Alive: true}
					if failure == "wrong app URL" {
						preview.AppID = "other-app"
					} else {
						preview.DeploymentID = "other-deployment"
					}
					_ = json.NewEncoder(w).Encode(preview)
				} else {
					http.NotFound(w, r)
				}
			})
			runner := startTestRunner(t, startTestSource(t), "")
			runner.session.AppSlug, runner.session.AppID, runner.session.Environment, runner.session.DeploymentID = "shop-api", "app-1", "staging", "d1"
			if code := runner.sendTestRequest("/", ""); code == 0 || requests != 0 || !strings.Contains(stdout.String()+stderr(), "environment") {
				t.Fatalf("default environment was checked: code=%d requests=%d stderr=%s", code, requests, stderr())
			}
		})
	}
}

func TestStartAcceptedDeploymentMustMatchReviewedTarget(t *testing.T) {
	for _, field := range []string{"app", "scope"} {
		t.Run(field, func(t *testing.T) {
			stdout, stderr := startTestEnvironment(t)
			source := startTestSource(t)
			startTestLink(t, source, "shop", "shop-api", "staging")
			startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if !startTestProjectResponse(w, r) {
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "shop-api"})
				}
			})
			runner := startTestRunner(t, source, "y\n")
			runner.deploy = func(ctx context.Context, _ []string, _ bool, execution ...deployExecution) int {
				dep := api.DeploymentResponse{ID: "accepted-id", AppID: "app-1", Scope: "staging", Status: "pending"}
				if field == "app" {
					dep.AppID = "other-app"
				} else {
					dep.Scope = "production"
				}
				execution[0].notifyQueued(dep)
				if ctx.Err() == nil {
					t.Error("waiting continued for an unexpected deployment target")
				}
				return 130
			}
			if code, submitted := runner.reviewAndDeploy(); code == 0 || submitted || !strings.Contains(stderr(), "unexpected target") || strings.Contains(stdout.String(), "App ready") {
				t.Fatalf("unexpected target accepted: code=%d submitted=%t stderr=%s", code, submitted, stderr())
			}
			saved, found, err := loadStartSession(runner.statePath)
			if err != nil || !found || saved.DeploymentID != "accepted-id" || saved.Environment != "staging" {
				t.Fatalf("accepted ID or reviewed environment was lost: saved=%+v err=%v", saved, err)
			}
		})
	}
}

func TestStartNamedEnvironmentCannotUseDefaultConfiguration(t *testing.T) {
	startTestEnvironment(t)
	source := startTestSource(t)
	writeFile(t, source, "needs-env.js", "console.log(process.env.FAAS_TEST_ALPHA);\n")
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("scope") != "staging" || r.Method != http.MethodGet {
			t.Error("environment configuration lookup used the wrong scope or mutated state")
		}
		if strings.HasSuffix(r.URL.Path, "/secrets") {
			_ = json.NewEncoder(w).Encode(api.AppSecretListResponse{Secrets: []api.AppSecretResponse{{Key: "FAAS_TEST_ALPHA", Scope: "default"}}})
		} else {
			_ = json.NewEncoder(w).Encode(api.AppEnvListResponse{})
		}
	})
	runner := startTestRunner(t, source, "")
	runner.session.Environment = "staging"
	runner.target = &startTarget{exists: true}
	report := doctorReport{Checks: []doctorCheck{{Name: "env-required", Status: "ok"}}}
	if err := runner.checkStartConfiguration(&report, nil); err != nil || !report.HasErrors() || len(runner.configuration.Missing) != 1 {
		t.Fatalf("default key satisfied a staging launch: configuration=%+v err=%v", runner.configuration, err)
	}
}

func TestStartRecoveryRejectsDeploymentFromAnotherEnvironment(t *testing.T) {
	startTestEnvironment(t)
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("recovery mutated remote state")
		}
		if r.URL.Path == "/v1/apps/shop-api" {
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "shop-api"})
		} else {
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "app-1", Scope: "production", Status: "live"})
		}
	})
	runner := startTestRunner(t, startTestSource(t), "")
	runner.session.AppSlug, runner.session.AppID, runner.session.Environment, runner.session.DeploymentID = "shop-api", "app-1", "staging", "d1"
	if code := runner.resume(); code == 0 {
		t.Fatal("another environment's deployment was resumed")
	}
}
