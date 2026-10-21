package sched_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261017122043000 — Read-only prime handoff evidence and independent collection health.
func TestSnapshotPrimeObservationUnknownAndHeldPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, "prime-unknown@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, d := sched.PrimeObservationCandidate(t, s, account.ID, "unknown", nil)
	for _, raw := range []string{`{"current":"snapshot_prepare"}`, `{"current":"snapshot_prepare","current_started_at":"yesterday"}`, `{"current":"snapshot_prepare","current_started_at":"2026-02-30T12:00:00Z"}`, `{"current":"snapshot_prepare","current_started_at":"1970-01-01T00:00:00Z"}`, `{"current":null}`, `[]`,
		`{"current":"snapshot_prepare","current_started_at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano) + `"}`} {
		if _, err := pool.Exec(ctx, "UPDATE deployments SET stage_state=$2::jsonb WHERE id=$1", d.ID, raw); err != nil {
			t.Fatal(err)
		}
		got, err := s.SnapshotPrimeSnapshot(ctx, "")
		if err != nil || got[state.SnapshotPrimeUnknown].Pending != 1 || got[state.SnapshotPrimeUnknown].UnknownAge != 1 || !got[state.SnapshotPrimeUnknown].OldestAt.IsZero() {
			t.Fatalf("raw=%s snapshot=%+v err=%v", raw, got, err)
		}
	}
	if _, err := pool.Exec(ctx, "UPDATE deployments SET stage_state=$2::jsonb WHERE id=$1", d.ID, d.StageState); err != nil {
		t.Fatal(err)
	}
	instance := sched.PrimeObservationInstance(t, s, app, d, state.StateColdBooting)
	if _, err := pool.Exec(ctx, "UPDATE instances SET started_at=NULL WHERE id=$1", instance.ID); err != nil {
		t.Fatal(err)
	}
	missing, err := s.SnapshotPrimeSnapshot(ctx, "")
	if err != nil || missing[state.SnapshotPrimeUnknown].Pending != 1 {
		t.Fatalf("missing instance start fabricated eligible work: %+v %v", missing, err)
	}
	if err := s.UpdateDeploymentStatus(ctx, d.ID, state.DeployCancelled, ""); err != nil {
		t.Fatal(err)
	}
	held := primeObservationHeldCandidate(t, s, account.ID)
	if err := s.SetDeploymentRootfs(ctx, held.ID, "/layers/held.ext4", "layers/held.ext4", 1024); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateDeploymentStatus(ctx, held.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	held, err = s.AppendDeploymentStage(ctx, held.ID, state.StageSourceDownload, state.StageSnapshotPrepare, time.Now().Add(-time.Hour), "")
	if err != nil || !held.EnvironmentWorkloadHeld() {
		t.Fatalf("candidate lost intentional hold: %+v %v", held, err)
	}
	got, err := s.SnapshotPrimeSnapshot(ctx, "")
	if err != nil || got != (state.SnapshotPrimeSnapshot{}) {
		t.Fatalf("intentional hold counted as waiting: %+v %v", got, err)
	}
}

// Prepare a genuinely held candidate through its reviewed transaction. Frozen
// inputs cannot be attached to an ordinary deployment after creation.
func primeObservationHeldCandidate(t *testing.T, s *state.PgStore, account string) state.Deployment {
	t.Helper()
	ctx := t.Context()
	project, err := s.CreateProject(ctx, state.Project{AccountID: account, Slug: "held-project", RepoFullName: "example/held", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account, ProjectID: project.ID, Slug: "held-app", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.CreateEnvironmentGitSource(ctx, account, project.ID, "production", state.EnvironmentGitSourceSpec{RepositoryID: 123, InstallationID: 42, Repository: "example/held", Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: "enforce", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: project.Slug, Environment: "production", Workloads: map[string]api.EnvironmentWorkload{"held": {App: app.Slug, Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/held@sha256:" + strings.Repeat("d", 64)}}}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = s.ApproveEnvironmentDesiredRevision(ctx, state.ApproveEnvironmentRevision{AccountID: account, SourceID: source.ID, ExpectedGeneration: source.Generation, CommitSHA: strings.Repeat("a", 40), Desired: desired, ApprovedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewEnvironmentGitOpsAdoption(ctx, account, source.ID)
	if err != nil || !preview.CanApply() {
		t.Fatalf("adoption: %+v %v", preview, err)
	}
	if err := s.AdoptEnvironmentGitOps(ctx, account, source.ID, preview.Hash); err != nil {
		t.Fatal(err)
	}
	lease, err := s.ClaimEnvironmentGitOps(ctx, "observation-test", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var plan environmentsync.Plan
	for round := range 2 {
		observed, err := s.ObserveEnvironmentGitOps(ctx, lease, desired)
		if err != nil {
			t.Fatal(err)
		}
		plan, err = environmentsync.BuildPlan(desired, observed.State, observed.Owners, environmentsync.PlanOptions{Manager: source.ID, Revision: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA, Generation: source.Generation, Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if round == 0 {
			if _, err := s.ApplyEnvironmentGitOps(ctx, lease, plan); err != nil {
				t.Fatal(err)
			}
		}
	}
	candidates, err := s.PrepareEnvironmentGitOpsImageCandidates(ctx, lease, plan)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("prepare held candidate: %+v %v", candidates, err)
	}
	held, err := s.DeploymentByID(ctx, candidates[0].DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	return held
}
