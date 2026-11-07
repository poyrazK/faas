// adr: 20261017190804, 20261017190803 — source imaging retains reviewed runtime inputs.
package imaged

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGitSourceBuildPreservesReviewedRuntime(t *testing.T) {
	for _, commandMissing := range []bool{false, true} {
		name := "image command"
		if commandMissing {
			name = "reviewed command supplies missing image entrypoint"
		}
		t.Run(name, func(t *testing.T) { testGitSourceBuildReviewedRuntime(t, commandMissing) })
	}
}

func testGitSourceBuildReviewedRuntime(t *testing.T, commandMissing bool) {
	ctx := t.Context()
	store := state.NewMemStore()
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
	app, err = state.AppForDeploymentRuntime(app, dep)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployImaging, ""); err != nil {
		t.Fatal(err)
	}
	archive := sourceBuildArchive(t, nil)
	if commandMissing {
		layer := gzipBytes(t, []byte("source-build-layer"))
		config := minimalConfigBytes([]byte("source-build-layer"))
		var document map[string]any
		if err := json.Unmarshal(config, &document); err != nil {
			t.Fatal(err)
		}
		document["config"] = map[string]any{}
		config, err = json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		manifest := minimalManifestBytes(config, layer)
		archive = buildLocalOCIArchive(t, map[string][]byte{"index.json": minimalIndexBytes(manifest),
			blobPath(digestFor(t, manifest)): manifest, blobPath(digestFor(t, config)): config, blobPath(digestFor(t, layer)): layer})
	}
	if err := store.SetDeploymentRootfs(ctx, dep.ID, archive, "builder/reviewed", 512); err != nil {
		t.Fatal(err)
	}
	dep, err = store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dep.ScopedWorkloadRuntime(); err != nil {
		t.Fatalf("invalid fixture before conversion: %v", err)
	}
	builder := &fakeBuilder{bytesOut: 4096}
	h := New(store, &fakeNotifier{}, nil, builder, "", t.TempDir(), silentLogger())
	if err := h.buildLocalOCIAppLayer(ctx, app, &dep, account, nil); err != nil {
		t.Fatal(err)
	}
	stored, loadErr := store.DeploymentByID(ctx, dep.ID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}

	frozen, err := stored.ScopedWorkloadRuntime()
	if err != nil || frozen == nil || stored.ImageDigest == "" {
		t.Fatalf("built source lost its reviewed inputs: digest=%q frozen=%+v err=%v", stored.ImageDigest, frozen, err)
	}
	if len(builder.calls) != 1 {
		t.Fatalf("layer builds=%d; want 1", len(builder.calls))
	}
	guest := builder.calls[0].Manifest
	if guest.Port != 9090 || guest.Env["PORT"] != "9090" || guest.StopGracePeriod != 1500*time.Millisecond || sched.DeploymentRuntimePort(stored) != 9090 {
		t.Fatalf("guest/scheduler lost reviewed contract: guest=%+v scheduler port=%d", guest, sched.DeploymentRuntimePort(stored))
	}
	if len(guest.Entrypoint) != 1 || guest.Entrypoint[0] != "/app/reviewed-server" {
		t.Fatalf("guest lost reviewed entrypoint: %q", guest.Entrypoint)
	}
	if _, err := state.AppForDeploymentRuntime(app, stored); err != nil {
		t.Fatalf("built artifact cannot advance to qualification: %v", err)
	}
}
