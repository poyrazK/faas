// adr: 581
package main

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentHostWaitsForAtomicCloneGraphPublication(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "clone-host@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "clone-host"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "clone-api", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Manifest: state.AppManifest{RevisionPinTTLSeconds: 1800}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:source"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, source.ID, "/source.ext4", "layers/source.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	graph, err := store.PublishProjectReleaseSet(ctx, acct.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: source.ID}})
	if err != nil {
		t.Fatal(err)
	}
	op, err := store.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: acct.ID, ProjectID: project.ID, SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "clone", SourceRevisionHash: strings.Repeat("a", 64), SourceReleaseSetID: graph.ID})
	if err != nil {
		t.Fatal(err)
	}
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, op.Status, state.CloneOperationCapturing, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	views, err := store.CaptureProjectEnvironmentCloneWorkloads(ctx, acct.ID, project.ID, op.ID, op.Revision)
	if err != nil {
		t.Fatal(err)
	}
	resources := []state.ProjectEnvironmentCloneResource{{Kind: "source_revision", Name: "production", SourceVersion: op.SourceRevisionHash, Status: "ready"},
		{Kind: "workload", Name: app.Slug, SourceID: source.ID, SourceVersion: views[0].SourceHash, Status: "captured"},
		{Kind: "project_config", Name: "production", SourceVersion: views[0].SourceProjectConfigHash, Status: "ready"}}
	for _, kind := range []string{"variables", "secrets"} {
		resources = append(resources, state.ProjectEnvironmentCloneResource{Kind: kind, Name: views[0].WorkloadSlug, SourceID: app.ID, TargetID: app.ID, SourceVersion: views[0].SourceValuesHash, Status: "ready"})
	}
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	env, _, err := store.CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{AccountID: acct.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "stage", CloneOperationID: op.ID, CloneOperationRevision: op.Revision}, api.MustLimitsFor(acct.Plan))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := store.ProjectEnvironmentWorkloadSpec(ctx, acct.ID, project.ID, "stage", app.ID)
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateDeploymentForEnvironmentClone(ctx, acct.ID, project.ID, op.ID, op.Revision, app.ID, spec.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLiveDark(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	router := pgRouter{store: store, appsSuffix: ".gregale.dev", deploySuffix: ".gregale.dev"}
	host := gateway.BuildEnvironmentHost(".gregale.dev", env.ID, app.ID)
	before, found, err := router.ResolveHost(ctx, host)
	if err != nil || !found || !before.EnvironmentNotReady || before.PinnedDeploymentID != "" {
		t.Fatalf("partial stage route = %+v, %v, %v", before, found, err)
	}
	resources[1].TargetID, resources[1].Status = target.ID, "ready"
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, op.Status, state.CloneOperationPublishing, op.Revision, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishProjectEnvironmentCloneReleaseSet(ctx, acct.ID, project.ID, op.ID, op.Revision, 1800); err != nil {
		t.Fatal(err)
	}
	after, found, err := router.ResolveHost(ctx, host)
	if err != nil || !found || after.EnvironmentNotReady || after.PinnedDeploymentID != target.ID || after.PinnedDeploymentScope != "stage" {
		t.Fatalf("published stage route = %+v, %v, %v", after, found, err)
	}
}
