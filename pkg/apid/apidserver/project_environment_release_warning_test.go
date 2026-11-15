package apidserver_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// An environment deploy into an environment with an active release set is
// warned that its deployments stay outside the graph, so their service calls
// are refused (ADR-20261019120024 amendment 2026-11-13).
func TestEnvironmentDeployWarnsAboutActiveReleaseSet(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv, store, acct := *e.S(), *e.Store(), *e.Acct()
	ctx := t.Context()
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "warned"})
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range []string{"staging", "qa"} {
		if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: env}); err != nil {
			t.Fatal(err)
		}
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "warned-api", WorkloadName: "api",
		WorkloadClass: state.WorkloadClassHTTP, Type: state.AppTypeApp, Status: state.AppActive,
		Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, acct.ID, project.ID, "staging", app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindTarball, SourceSHA256: strings.Repeat("c", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/artifacts/warned", "/artifacts/warned", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	if got := srv.EnvironmentReleaseSetWarning(ctx, acct.ID, project.Slug, "staging"); got != "" {
		t.Fatalf("warning before any release set = %q; want none", got)
	}
	if _, err := store.PublishProjectReleaseSet(ctx, acct.ID, project.ID, "staging", 1800,
		[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: deployment.ID}}); err != nil {
		t.Fatalf("publish release set: %v", err)
	}
	for _, tc := range []struct {
		name, project, environment string
		warned                     bool
	}{
		{"active release set", project.Slug, "staging", true},
		{"environment without one", project.Slug, "qa", false},
		{"unknown project", "missing", "staging", false},
	} {
		got := srv.EnvironmentReleaseSetWarning(ctx, acct.ID, tc.project, tc.environment)
		if (got != "") != tc.warned || tc.warned && !strings.Contains(got, "active release set") {
			t.Errorf("%s: warning = %q; want warned=%t", tc.name, got, tc.warned)
		}
	}
}
