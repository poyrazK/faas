package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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
