package main

import (
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

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

func TestCmdInitPrivateNodeScaffoldsInternalHTTPProject(t *testing.T) {
	startTestEnvironment(t)
	dir := filepath.Join(resolvedTempDir(t), "private-starter")
	var out, stderr bytes.Buffer
	if code := runCmdInitWithSecrets(templates.PrivateNode, dir, false, "named-node", "", &out, &stderr); code != 0 {
		t.Fatalf("init exit %d: %s", code, stderr.String())
	}
	slug, err := privateNodeStarterName(dir)
	if err != nil || slug != "named-node" {
		t.Fatalf("starter name = %q, %v", slug, err)
	}
	scan, err := reposcan.Scan(os.DirFS(dir))
	if err != nil || len(scan.Workloads) != 1 {
		t.Fatalf("could not scan a single workload: %v", err)
	}
	workload := scan.Workloads[0]
	if workload.Name != slug || workload.Class != "http" || reposcan.NewWorkloadVisibility(workload) != api.AppVisibilityInternal || multiServiceSource(dir) == "" {
		t.Fatalf("expected an internal HTTP project, got %+v", workload)
	}
	guide, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil || !strings.Contains(string(guide), "deploy --project") || !strings.Contains(string(guide), "release verify") {
		t.Fatalf("missing private project instructions: %v", err)
	}
}

func TestStartPrivateNodePlanRejectsPublicOrDifferentApp(t *testing.T) {
	const slug = "named-node"
	plan := api.PlanResponse{ProjectSlug: slug, Workloads: []api.PlanWorkload{{
		Name: slug, AppSlug: slug, Class: "http", Visibility: api.AppVisibilityInternal,
	}}}
	if err := validatePrivateNodePlan(plan, slug, ""); err != nil {
		t.Fatal(err)
	}
	plan.Workloads[0].Visibility = api.AppVisibilityPublic
	if err := validatePrivateNodePlan(plan, slug, ""); err == nil {
		t.Fatal("public starter plan was accepted")
	}
	plan.Workloads[0].Visibility = api.AppVisibilityInternal
	plan.Workloads[0].ExistingAppID = "other-app"
	if err := validatePrivateNodePlan(plan, slug, "original-app"); err == nil {
		t.Fatal("plan to replace another app was accepted")
	}
	plan.Workloads[0].ExistingAppID = "original-app"
	plan.Removed = []string{"other-workload"}
	if err := validatePrivateNodePlan(plan, slug, "original-app"); err == nil {
		t.Fatal("plan removing a different workload was accepted")
	}
}

func TestStartPrivateNodeReceiptRetainsAppAndPriorVerification(t *testing.T) {
	startTestEnvironment(t)
	r := startTestRunner(t, resolvedTempDir(t), "")
	r.selectPrivateNodeSource(r.session.SourcePath, "named-node")
	r.session.ProjectID, r.session.AppID = "project", "app"
	r.session.DeploymentID, r.session.VerifiedDeploymentID = "earlier-release", "earlier-release"
	r.session.ProjectSessionID = strings.Repeat("a", 32)
	if err := saveStartSession(r.statePath, r.session); err != nil {
		t.Fatal(err)
	}
	saved, found, err := loadStartSession(r.statePath)
	if err != nil || !found || saved.ProjectSessionID != r.session.ProjectSessionID {
		t.Fatalf("pending starter attempt was not recoverable: %v", err)
	}
	apply := api.ApplyResponse{
		ProjectID: "project",
		Apps:      []api.ApplyResponseApp{{Slug: "named-node", ID: "app"}},
		Builds:    []api.AppliedBuild{{Slug: "named-node", AppID: "app", DeploymentID: "new-release"}},
	}
	if submitted, err := r.acceptPrivateNodeApply(apply); !submitted || err != nil {
		t.Fatalf("accept = %v, %v", submitted, err)
	}
	saved, _, err = loadStartSession(r.statePath)
	if err != nil || saved.ProjectSessionID != "" || saved.AppID != "app" ||
		saved.DeploymentID != "new-release" || saved.VerifiedDeploymentID != "earlier-release" {
		t.Fatalf("receipt lost identity or prior response proof: %+v, %v", saved, err)
	}
}

func TestStartPrivateNodeOwnsTheCheckoutLink(t *testing.T) {
	startTestEnvironment(t)
	source := resolvedTempDir(t)
	r := startTestRunner(t, source, "")
	r.selectPrivateNodeSource(source, "named-node")
	called := false
	r.deploy = func(_ context.Context, _ []string, _ bool, executions ...deployExecution) int {
		called = true
		if len(executions) != 1 || !executions[0].skipProjectLink {
			t.Fatal("project deploy could change the checkout link before Start verifies and edits the app")
		}
		return 0
	}
	_, _ = r.deployPrivateNode()
	if !called {
		t.Fatal("guided project deploy did not run")
	}
	r.session.ProjectID, r.session.AppID = "project", "app"
	r.session.DeploymentID, r.session.VerifiedDeploymentID = "release", "release"
	if err := r.rememberApp(); err != nil {
		t.Fatal(err)
	}
	link, key, err := startSourceContext(source)
	if err != nil || link.Project != "named-node" || link.App != "named-node" || key == "" || key != r.session.SourceContext {
		t.Fatalf("verified session did not retain its checkout link: %+v, %v", link, err)
	}
}

func TestStartPrivateNodeReviewsAndVerifiesFirstEdit(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	scope := resolvedTempDir(t)
	const slug = "private-node"
	const initialGreeting = "Hello from private Node"
	const editedGreeting = "Hello from the verified edit"
	source := filepath.Join(scope, slug)
	deployments := [...]string{startResponseDeploymentID, startProjectNewDeploymentID}
	greetings := [...]string{initialGreeting, editedGreeting}
	var applies, checks, anonymousRequests atomic.Int32
	anonymous := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anonymousRequests.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("app request carried the Gregale credential")
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(anonymous.Close)

	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects/scan":
			workload := api.PlanWorkload{Name: slug, AppSlug: slug, Class: "http", Visibility: api.AppVisibilityInternal}
			if applies.Load() != 0 {
				workload.ExistingAppID = startResponseAppID
			}
			_ = json.NewEncoder(w).Encode(api.PlanResponse{
				ProjectSlug: slug, CanApply: true, PlanToken: "reviewed-plan", LimitApps: 1,
				Workloads: []api.PlanWorkload{workload},
				Warnings:  []string{"reposcan: compose.yaml: " + slug + ": " + reposcan.ComposeLoopbackPortWarning, "Keep this scan warning"},
			})
		case "/v1/projects":
			index := applies.Add(1) - 1
			if index >= int32(len(deployments)) {
				t.Error("more than two project applies")
				http.Error(w, "unexpected apply", http.StatusBadRequest)
				return
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				http.Error(w, "invalid apply", http.StatusBadRequest)
				return
			}
			defer func() { _ = r.MultipartForm.RemoveAll() }()
			if r.FormValue("project_slug") != slug || r.FormValue("environment") != "" ||
				r.URL.Query().Get("plan_token") != "reviewed-plan" {
				t.Error("project apply did not keep the reviewed project and scope")
			}
			archive, _, err := r.FormFile("source")
			if err != nil {
				t.Error(err)
				http.Error(w, "missing archive", http.StatusBadRequest)
				return
			}
			defer func() { _ = archive.Close() }()
			data, err := io.ReadAll(archive)
			if err != nil || !bytes.Contains(readCapturedDeployArchive(t, data)[slug+"/app/server.js"], []byte(greetings[index])) {
				t.Errorf("release %d did not upload its expected greeting: %v", index, err)
			}
			_ = json.NewEncoder(w).Encode(api.ApplyResponse{
				ProjectID: startResponseProjectID,
				Apps:      []api.ApplyResponseApp{{Slug: slug, ID: startResponseAppID}},
				Builds:    []api.AppliedBuild{{Slug: slug, AppID: startResponseAppID, DeploymentID: deployments[index]}},
			})
		case "/v1/projects/" + slug:
			if applies.Load() == 0 {
				api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "not_found", "Unknown project", "The test project has not been created"))
				return
			}
			_ = json.NewEncoder(w).Encode(api.ProjectResponse{
				ProjectSummaryResponse: api.ProjectSummaryResponse{ID: startResponseProjectID, Slug: slug},
				Workloads:              []api.ProjectWorkloadResponse{{Slug: slug}},
			})
		case "/v1/apps/" + slug:
			_ = json.NewEncoder(w).Encode(api.AppResponse{
				ID: startResponseAppID, Slug: slug, ProjectID: startResponseProjectID,
				Visibility: string(api.AppVisibilityInternal), CanonicalURL: anonymous.URL,
			})
		case "/v1/deployments/" + deployments[0], "/v1/deployments/" + deployments[1]:
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{
				ID: strings.TrimPrefix(r.URL.Path, "/v1/deployments/"), AppID: startResponseAppID, Status: statusLive, Scope: "default",
			})
		case "/v1/apps/" + slug + "/deployments/" + deployments[0] + "/response-check",
			"/v1/apps/" + slug + "/deployments/" + deployments[1] + "/response-check":
			index := checks.Add(1) - 1
			var check api.DeploymentResponseCheckRequest
			if index >= int32(len(deployments)) || applies.Load() != index+1 ||
				!strings.HasSuffix(r.URL.Path, "/"+deployments[index]+"/response-check") ||
				r.Header.Get("Authorization") != "Bearer "+testAPIKey('e') ||
				json.NewDecoder(r.Body).Decode(&check) != nil || check.AppID != startResponseAppID ||
				check.ProjectID != startResponseProjectID || check.Scope != "default" || check.Path != "/" {
				t.Errorf("unexpected owner verification request: %s %+v", r.URL.Path, check)
				http.Error(w, "wrong release check", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(api.DeploymentResponseCheckResult{
				AppID: check.AppID, ProjectID: check.ProjectID, Scope: check.Scope,
				DeploymentID: deployments[index], InstanceID: startResponseInstanceID,
				HTTPStatus: http.StatusOK, Body: fmt.Sprintf(`{"message":%q}`, greetings[index]),
			})
		default:
			http.NotFound(w, r)
		}
	})

	priorInput := osStdin
	t.Cleanup(func() { osStdin = priorInput })
	osStdin = &startProjectInput{
		lines: []string{"2", "4", source, "y", "2", "y", "1"},
		before: func(index int) error {
			if index != 4 && index != 6 {
				return nil
			}
			res, err := anonymous.Client().Get(anonymous.URL)
			if err != nil {
				return err
			}
			_ = res.Body.Close()
			if res.StatusCode != http.StatusNotFound {
				return fmt.Errorf("anonymous request returned %d", res.StatusCode)
			}
			if index == 4 {
				path := filepath.Join(source, "app/server.js")
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				return os.WriteFile(path, bytes.Replace(data, []byte(initialGreeting), []byte(editedGreeting), 1), 0o644)
			}
			return nil
		},
	}
	if code := cmdStart([]string{scope}); code != 0 || applies.Load() != 2 || checks.Load() != 2 || anonymousRequests.Load() != 2 {
		t.Fatalf("start exit=%d applies=%d checks=%d anonymous=%d\n%s\n%s", code, applies.Load(), checks.Load(), anonymousRequests.Load(), stdout, stderr())
	}
	out := stdout.String()
	for _, want := range []string{
		"Access   Internal only", "Keep this scan warning", "Check    Gregale verifies the deployment response through your account.",
		"Project  Reuse " + slug + " (" + startResponseProjectID + ")",
		"Applied project " + startResponseProjectID,
		"Verified deployment " + deployments[1] + " for existing project " + slug + " (" + startResponseProjectID + ").",
		initialGreeting, editedGreeting,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in Start transcript:\n%s", want, out)
		}
	}
	if strings.Count(out, "Created project ") != 1 || strings.Contains(out, "remove the address to publish it") || strings.Contains(out, "On paid plans") {
		t.Fatalf("unexpected Create receipt or review advice:\n%s", out)
	}
	session, found, err := loadStartSession(startTestSessionRecord(t, scope))
	if err != nil || !found || session.AppID != startResponseAppID || session.ProjectID != startResponseProjectID ||
		session.DeploymentID != deployments[1] || session.VerifiedDeploymentID != deployments[1] {
		t.Fatalf("Start did not retain the original project and exact edited release: %+v %v", session, err)
	}
}
