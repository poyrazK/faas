//go:build !no_pg

// adr: 20261017190804, 20261024100000 — derived source metadata survives PostgreSQL fences.
package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresGitSourceImagingMetadata(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "image-preparation@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop", RepoFullName: "example/shop", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "held-api", Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 1, StartCommand: "./reviewed", Manifest: state.AppManifest{Port: 8079}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(ctx, account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{
		RepositoryID: 123, InstallationID: 42, Repository: "example/shop", Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: "enforce", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: project.Slug, Environment: "production",
		Workloads: map[string]api.EnvironmentWorkload{"api": {App: app.Slug, Source: &api.EnvironmentWorkloadSource{Kind: "dockerfile", Directory: "app", Dockerfile: "Dockerfile"},
			Runtime: json.RawMessage(`{"port":9090,"stop_grace_period":1500000000,"entrypoint":["/app/reviewed-server"]}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(ctx, state.ApproveEnvironmentRevision{AccountID: account.ID, SourceID: source.ID,
		ExpectedGeneration: source.Generation, CommitSHA: strings.Repeat("a", 40), Desired: desired, ApprovedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	adoption, err := store.PreviewEnvironmentGitOpsAdoption(ctx, account.ID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptEnvironmentGitOps(ctx, account.ID, source.ID, adoption.Hash); err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimEnvironmentGitOps(ctx, "image-preparer", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	plan := func() environmentsync.Plan {
		observed, err := store.ObserveEnvironmentGitOps(ctx, lease, desired)
		if err != nil {
			t.Fatal(err)
		}
		result, err := environmentsync.BuildPlan(desired, observed.State, observed.Owners, environmentsync.PlanOptions{
			Manager: source.ID, Revision: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA, Generation: source.Generation, Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if _, err := store.ApplyEnvironmentGitOps(ctx, lease, plan()); err != nil {
		t.Fatal(err)
	}
	requests, err := store.EnvironmentGitOpsSourceRequests(ctx, lease, plan())
	if err != nil || len(requests) != 1 {
		t.Fatalf("source requests: %+v %v", requests, err)
	}
	artifacts := map[string]state.EnvironmentWorkloadSourceArtifact{}
	for _, req := range requests {
		artifacts[req.Resource] = state.EnvironmentWorkloadSourceArtifact{RevisionID: req.RevisionID, CommitSHA: req.CommitSHA, DefinitionDigest: req.DefinitionDigest, BuildID: req.BuildID, Path: t.TempDir() + "/source.tgz", SHA256: strings.Repeat("b", 64), Bytes: 512, LogPath: t.TempDir() + "/build.log"}
	}
	candidates, err := store.PrepareEnvironmentGitOpsCandidates(ctx, lease, plan(), artifacts)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("prepare: %+v %v", candidates, err)
	}
	dep, err := store.DeploymentByID(ctx, candidates[0].DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dep.ScopedWorkloadRuntime(); err != nil {
		t.Fatalf("invalid fixture before conversion: %v", err)
	}

	if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployImaging, ""); err != nil {
		t.Fatal(err)
	}
	const export = "/builder/reviewed.tar"
	if err := store.SetDeploymentRootfs(ctx, dep.ID, export, "builder/reviewed", 512); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("c", 64)
	for i := 0; i < 2; i++ {
		if err := store.PinDeploymentBuildDigest(ctx, dep.ID, export, digest); err != nil {
			t.Fatalf("pin derived digest: %v", err)
		}
	}
	profile := []byte(`{"version":"v1","framework":"unknown","port":9090,"image_healthcheck_required":true}`)
	if err := store.SetDeploymentRuntimeProfile(ctx, dep.ID, profile); err != nil {
		t.Fatalf("persist runtime facts: %v", err)
	}
	profile = []byte(`{"version":"v1","framework":"unknown","port":9091,"image_healthcheck_required":false}`)
	if err := store.SetDeploymentRuntimeProfile(ctx, dep.ID, profile); err != nil {
		t.Fatalf("refresh runtime facts: %v", err)
	}
	for _, tc := range []struct {
		name, query string
		value       any
	}{
		{"source hash", `UPDATE deployments SET source_sha256=$2 WHERE id=$1`, strings.Repeat("f", 64)},
		{"commit", `UPDATE deployments SET commit_sha=$2 WHERE id=$1`, strings.Repeat("f", 40)},
		{"archive bytes", `UPDATE deployments SET source_bytes=$2 WHERE id=$1`, 513},
		{"archive path", `UPDATE deployments SET source_path=$2 WHERE id=$1`, "/other/source.tgz"},
		{"build", `UPDATE deployments SET build_id=$2::uuid WHERE id=$1`, source.ID},
		{"digest repin", `UPDATE deployments SET image_digest=$2 WHERE id=$1`, "sha256:" + strings.Repeat("f", 64)},
		{"registry authority", `UPDATE deployments SET image_digest=$2 WHERE id=$1`, "example/api@" + digest},
		{"source envelope", `UPDATE deployments SET environment_workload_runtime=environment_workload_runtime || $2::jsonb WHERE id=$1`, `{"source_archive":{}}`},
		{"unknown profile input", `UPDATE deployments SET inferred_profile=inferred_profile || $2::jsonb WHERE id=$1`, `{"entrypoint":["/other"]}`},
		{"profile version", `UPDATE deployments SET inferred_profile=inferred_profile || $2::jsonb WHERE id=$1`, `{"version":"v2"}`},
		{"invalid port", `UPDATE deployments SET inferred_profile=inferred_profile || $2::jsonb WHERE id=$1`, `{"port":65536}`},
		{"invalid health verdict", `UPDATE deployments SET inferred_profile=inferred_profile || $2::jsonb WHERE id=$1`, `{"image_healthcheck_required":"yes"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, tc.query, dep.ID, tc.value)
			var constraint *pgconn.PgError
			if !errors.As(err, &constraint) || constraint.Code != "23514" {
				t.Fatalf("immutable input update: %v", err)
			}
		})
	}
	stored, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if frozen, err := stored.ScopedWorkloadRuntime(); err != nil || frozen == nil || frozen.SourceArchive == nil || stored.ImageDigest != digest {
		t.Fatalf("built source cannot advance: deployment=%+v runtime=%+v err=%v", stored, frozen, err)
	}
	var actual map[string]json.RawMessage
	if err := json.Unmarshal(stored.InferredProfile, &actual); err != nil || string(actual["port"]) != "9091" {
		t.Fatalf("runtime profile %s: %v", stored.InferredProfile, err)
	}
	if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.PinDeploymentBuildDigest(ctx, dep.ID, export, digest); err != nil {
		t.Fatalf("idempotent pin before publication: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE deployments SET inferred_profile=inferred_profile || '{"port":9191}'::jsonb WHERE id=$1`, dep.ID); err == nil {
		t.Fatal("profile changed after imaging")
	}
}
