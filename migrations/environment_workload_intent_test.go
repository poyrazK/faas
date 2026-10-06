//go:build !no_pg

package migrations_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentWorkloadIntentPopulatedReplay(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "workload-replay@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "workload-replay", RepoFullName: "example/shop", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "replay-api", Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 1, Status: state.AppActive, Manifest: state.AppManifest{Port: 8079}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(ctx, account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{
		RepositoryID: 123, InstallationID: 42, Repository: "example/shop", Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: "enforce", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: project.Slug, Environment: "production",
		Workloads: map[string]api.EnvironmentWorkload{"api": {App: app.Slug, Runtime: json.RawMessage(`{"port":8080}`),
			Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/shop@sha256:" + strings.Repeat("d", 64)}}}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(ctx, state.ApproveEnvironmentRevision{AccountID: account.ID, SourceID: source.ID,
		ExpectedGeneration: source.Generation, CommitSHA: strings.Repeat("a", 40), Desired: desired, ApprovedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := store.PreviewEnvironmentGitOpsAdoption(ctx, account.ID, source.ID)
	if err != nil || !preview.CanApply() {
		t.Fatalf("adoption: %+v %v", preview, err)
	}
	if err := store.AdoptEnvironmentGitOps(ctx, account.ID, source.ID, preview.Hash); err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimEnvironmentGitOps(ctx, "workload-replay-worker", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := store.ObserveEnvironmentGitOps(ctx, lease, desired)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := environmentsync.BuildPlan(desired, observed.State, observed.Owners, environmentsync.PlanOptions{
		Manager: source.ID, Revision: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA, Generation: source.Generation, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyEnvironmentGitOps(ctx, lease, plan); err != nil {
		t.Fatal(err)
	}
	observed, err = store.ObserveEnvironmentGitOps(ctx, lease, desired)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = environmentsync.BuildPlan(desired, observed.State, observed.Owners, environmentsync.PlanOptions{
		Manager: source.ID, Revision: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA, Generation: source.Generation, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := store.PrepareEnvironmentGitOpsImageCandidates(ctx, lease, plan)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("prepare candidate before replay: %+v %v", candidates, err)
	}
	prepared, err := store.DeploymentByID(ctx, candidates[0].DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.EnvironmentWorkloadIntent(ctx, account.ID, app.ID, source.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id=any($1::bigint[])`, environmentGitOpsReplayVersions()); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	after, err := store.EnvironmentWorkloadIntent(ctx, account.ID, app.ID, source.EnvironmentID)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("populated replay replaced scoped intent or identity: %+v %v", after, err)
	}
	if err := store.RenewEnvironmentGitOps(ctx, lease, time.Now(), time.Minute); err != nil {
		t.Fatalf("replay fenced an issued lease: %v", err)
	}
	replayed, err := store.DeploymentByID(ctx, prepared.ID)
	if err != nil || replayed.EnvironmentWorkloadRuntime != prepared.EnvironmentWorkloadRuntime || replayed.Status != prepared.Status {
		t.Fatalf("replay changed immutable candidate inputs: %+v %v", replayed, err)
	}
	if err := store.MarkDeploymentLive(ctx, prepared.ID); err == nil {
		t.Fatal("replay released an unqualified candidate")
	}
	after.Runtime["port"] = json.RawMessage(`9999`)
	if _, err := store.PutEnvironmentWorkloadIntent(ctx, after); !errors.Is(err, state.ErrEnvironmentGitManaged) {
		t.Fatalf("replay released runtime ownership: %v", err)
	}
	targets, err := store.ObserveEnvironmentGitOpsRuntime(ctx, lease)
	if err != nil || len(targets) != 1 || targets[0].UnqualifiedWorkloads != 1 || targets[0].Ready() {
		t.Fatalf("replay qualified an unprepared graph: %+v %v", targets, err)
	}
	current, err := store.AppByID(ctx, app.ID)
	if err != nil || current.Manifest.Port != 8079 {
		t.Fatalf("replay changed inherited application intent: %+v %v", current.Manifest, err)
	}
}
