package builderd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type lostReviewedSourceHandoff struct{ fakeNotifier }

func (n *lostReviewedSourceHandoff) Notify(ctx context.Context, channel, payload string) error {
	_ = n.fakeNotifier.Notify(ctx, channel, payload)
	return errors.New("notification connection lost")
}

func TestEnvironmentWorkloadSourceBuildCompletionRetainsRecoverableHold(t *testing.T) {
	for _, kind := range []string{"source", "dockerfile"} {
		t.Run(kind, func(t *testing.T) {
			store, dep, _ := heldSourceBuild(t, kind)
			t.Setenv("FAAS_DEPLOY_BASE_REF_NODE22", "registry.example/node@sha256:"+strings.Repeat("1", 64))
			artifactPath := filepath.Join(t.TempDir(), "reviewed-image.tar")
			if err := os.WriteFile(artifactPath, []byte("completed build artifact"), 0o640); err != nil {
				t.Fatal(err)
			}
			vm := &fakeVM{out: BuildOutcome{OCIImage: artifactPath, ExitCode: 0}}
			notifier := &lostReviewedSourceHandoff{}
			builder := New(store, notifier, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{BuildTimeoutSeconds: 30, SourceSpoolDir: filepath.Dir(dep.SourcePath)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			result, err := builder.ProcessOne(t.Context(), dep.BuildID)
			if err != nil || result.BuildID != dep.BuildID || vm.spawnCalls != 1 {
				t.Fatalf("build completion: %+v %v", result, err)
			}
			build, err := store.BuildByID(t.Context(), dep.BuildID)
			if err != nil || build.Status != state.BuildSucceeded {
				t.Fatalf("lost notification lost the build: %+v %v", build, err)
			}
			provenance, err := store.BuildProvenanceByBuildID(t.Context(), dep.BuildID)
			if err != nil || provenance.CommitSHA != dep.CommitSHA || provenance.SourceSHA256 != dep.SourceSHA256 || provenance.SourceURL != dep.SourceURL {
				t.Fatalf("reviewed provenance changed: %+v %v", provenance, err)
			}
			work, err := store.ListBuildsAwaitingImage(t.Context(), "", 10)
			if err != nil || len(work) != 1 || work[0].DeploymentID != dep.ID {
				t.Fatalf("durable image handoff: %+v %v", work, err)
			}
			stored, err := store.DeploymentByID(t.Context(), dep.ID)
			if err != nil || !stored.EnvironmentWorkloadHeld() || stored.Status == state.DeployLive || stored.RootfsPath == "" {
				t.Fatalf("artifact escaped graph hold: %+v %v", stored, err)
			}
			handoffs := 0
			for _, call := range notifier.calls {
				switch call.channel {
				case db.NotifySnapshotBoot:
					handoffs++
				case db.NotifyBuildLog:
				default:
					t.Fatalf("build emitted unexpected work on %s", call.channel)
				}
			}
			if handoffs != 1 {
				t.Fatalf("image handoffs = %d, want one", handoffs)
			}
		})
	}
}

func heldSourceBuild(t *testing.T, kind string) (*state.MemStore, state.Deployment, state.App) {
	t.Helper()
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "frozen-build@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "frozen-build", RepoFullName: "example/build", InstallID: 42, ProductionBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "frozen-api", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512, MaxConcurrency: 1,
		Runtime: "node22", Manifest: state.AppManifest{Port: 8080, BuildDockerfile: "shared/Dockerfile"}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{RepositoryID: 123, InstallationID: 42, Repository: "example/build", Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: "enforce", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	buildSource := &api.EnvironmentWorkloadSource{Kind: kind, Directory: "."}
	if kind == "dockerfile" {
		buildSource.Dockerfile = "deploy/Dockerfile"
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: project.Slug, Environment: "production", Workloads: map[string]api.EnvironmentWorkload{"api": {App: app.Slug, Source: buildSource, Runtime: json.RawMessage(`{"port":8081}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), state.ApproveEnvironmentRevision{AccountID: account.ID, SourceID: source.ID, ExpectedGeneration: source.Generation, CommitSHA: strings.Repeat("a", 40), Desired: desired, ApprovedBy: account.ID})
	if err != nil {
		t.Fatal(err)
	}
	adoption, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), account.ID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptEnvironmentGitOps(t.Context(), account.ID, source.ID, adoption.Hash); err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimEnvironmentGitOps(t.Context(), "frozen-builder", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	readPlan := func() environmentsync.Plan {
		observation, err := store.ObserveEnvironmentGitOps(t.Context(), lease, desired)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := environmentsync.BuildPlan(desired, observation.State, observation.Owners, environmentsync.PlanOptions{Manager: source.ID, Revision: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA, Generation: source.Generation, Now: time.Now(), Overrides: observation.Overrides})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, readPlan()); err != nil {
		t.Fatal(err)
	}
	plan := readPlan()
	requests, err := store.EnvironmentGitOpsSourceRequests(t.Context(), lease, plan)
	if err != nil || len(requests) != 1 {
		t.Fatalf("requests: %+v %v", requests, err)
	}
	path := filepath.Join(t.TempDir(), "source.tar.gz")
	makeTarballWithName(t, path, []string{"package.json", "deploy/Dockerfile"})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	artifact := state.EnvironmentWorkloadSourceArtifact{RevisionID: requests[0].RevisionID, CommitSHA: requests[0].CommitSHA, DefinitionDigest: requests[0].DefinitionDigest, BuildID: requests[0].BuildID, Path: path, SHA256: hex.EncodeToString(hash[:]), Bytes: int64(len(data)), LogPath: filepath.Join(filepath.Dir(path), "build.log")}
	candidates, err := store.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan, map[string]state.EnvironmentWorkloadSourceArtifact{requests[0].Resource: artifact})
	if err != nil || len(candidates) != 1 {
		t.Fatalf("prepare: %+v %v", candidates, err)
	}
	dep, err := store.DeploymentByID(t.Context(), candidates[0].DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	return store, dep, app
}

func TestEnvironmentWorkloadSourceBuilderUsesFrozenSelection(t *testing.T) {
	for _, kind := range []string{"source", "dockerfile"} {
		t.Run(kind, func(t *testing.T) {
			store, dep, app := heldSourceBuild(t, kind)
			manifest := app.Manifest
			manifest.BuildDockerfile = "console/Dockerfile"
			if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("FAAS_DEPLOY_BASE_REF_NODE22", "registry.example/node@sha256:"+strings.Repeat("1", 64))
			spawnFailure := errors.New("stop after observing builder inputs")
			vm := &fakeVM{spawnErr: spawnFailure}
			b := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{BuildTimeoutSeconds: 30, SourceSpoolDir: filepath.Dir(dep.SourcePath)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if _, err := b.ProcessOne(t.Context(), dep.BuildID); !errors.Is(err, spawnFailure) {
				t.Fatalf("process: %v", err)
			}
			request := vm.lastRequest
			wantFramework, wantDockerfile := FrameworkNode, ""
			if kind == "dockerfile" {
				wantFramework, wantDockerfile = FrameworkDocker, "deploy/Dockerfile"
			}
			if vm.spawnCalls != 1 || request.Framework != wantFramework || request.DockerfilePath != wantDockerfile || request.Runtime != "node22" || request.SourceRoot != "." || request.SourceSHA256 != dep.SourceSHA256 || request.SourcePath != dep.SourcePath || request.LogPath != dep.LogPath {
				t.Fatalf("builder read unreviewed selection: %+v", request)
			}
			stored, err := store.DeploymentByID(t.Context(), dep.ID)
			if err != nil || !stored.EnvironmentWorkloadHeld() || stored.Status == state.DeployLive {
				t.Fatalf("build failure escaped graph hold: %+v %v", stored, err)
			}
		})
	}
}
