package builderd

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrationBuildWaitPrecedesSourceCacheAndSlotAdmission(t *testing.T) {
	store := state.NewMemStore()
	account, _ := store.CreateAccount(t.Context(), "wait@example.com", api.PlanPro)
	project, _ := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "wait"})
	target, _ := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, WorkloadName: "api", Slug: "wait-api", Type: state.AppTypeApp})
	web, _ := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, WorkloadName: "web", Slug: "wait-web", Type: state.AppTypeApp})
	apiDep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: target.ID, Kind: state.DeploymentKindTarball})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: web.ID, Kind: state.DeploymentKindTarball, SourcePath: "/missing/unread-source.tar.gz", BuildDependencies: []state.DeploymentDependencyPin{{Service: "api", AppID: target.ID, DeploymentID: apiDep.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	build, err := store.CreateBuild(t.Context(), dep.ID, state.DeploymentKindTarball, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	vm := &fakeVM{}
	builder := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := builder.ProcessOne(t.Context(), build.ID); err != nil {
		t.Fatal(err)
	}
	queued, _ := store.BuildByID(t.Context(), build.ID)
	if queued.Status != state.BuildQueued || vm.spawnCalls != 0 {
		t.Fatal("waiting build started")
	}
	// The polling path skips the deferred frontend and can run another service.
	src := filepath.Join(t.TempDir(), "source.tar.gz")
	makeTarballWithName(t, src, []string{"package.json", "index.js"})
	id, _, _ := seedDeployment(t, store, src)
	artifact := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(artifact, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	vm.out = BuildOutcome{OCIImage: artifact}
	result, err := builder.ProcessNext(t.Context())
	if err != nil || result.BuildID != id || vm.spawnCalls != 1 {
		t.Fatalf("waiting build blocked unrelated work: %+v %v", result, err)
	}
}

func TestBuildDependencyDigestPartitionsArtifacts(t *testing.T) {
	r := testBuildCacheRecipe("source", FrameworkNode, api.PlanPro, "base")
	baseline, _ := r.KeySHA256()
	r.BuildDependencyDigest = "api-deployment-one"
	first, _ := r.KeySHA256()
	r.BuildDependencyDigest = "api-deployment-two"
	second, _ := r.KeySHA256()
	if baseline == first || first == second {
		t.Fatal("API deployment identity did not partition cache")
	}
	r.BuildDependencyDigest = ""
	empty, _ := r.KeySHA256()
	if empty != baseline {
		t.Fatal("legacy cache key changed")
	}
}

func TestBuildDependenciesRequireGuestAcknowledgement(t *testing.T) {
	for _, ack := range []bool{false, true} {
		t.Run(map[bool]string{false: "old guest refused", true: "current guest accepted"}[ack], func(t *testing.T) {
			store := state.NewMemStore()
			account, _ := store.CreateAccount(t.Context(), "ready-wait@example.com", api.PlanPro)
			project, _ := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "ready"})
			target, _ := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, WorkloadName: "api", Slug: "wait-api", Type: state.AppTypeApp})
			web, _ := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, WorkloadName: "web", Slug: "wait-web", Type: state.AppTypeApp})
			apiDep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: target.ID, Kind: state.DeploymentKindTarball})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDeploymentLive(t.Context(), apiDep.ID); err != nil {
				t.Fatal(err)
			}
			src := filepath.Join(t.TempDir(), "source.tar.gz")
			makeTarballWithName(t, src, []string{"package.json", "index.js"})
			pins := []state.DeploymentDependencyPin{{Service: "api", AppID: target.ID, DeploymentID: apiDep.ID}}
			dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: web.ID, Kind: state.DeploymentKindTarball, SourcePath: src, BuildDependencies: pins})
			if err != nil {
				t.Fatal(err)
			}
			build, err := store.CreateBuild(t.Context(), dep.ID, state.DeploymentKindTarball, 0, "")
			if err != nil {
				t.Fatal(err)
			}
			artifact := filepath.Join(t.TempDir(), "image.tar")
			if err := os.WriteFile(artifact, []byte("frontend"), 0o600); err != nil {
				t.Fatal(err)
			}
			digest := state.BuildDependencyDigest(&state.BuildDependencyGate{Dependencies: pins})
			vm := &fakeVM{out: BuildOutcome{OCIImage: artifact}}
			if ack {
				vm.out.BuildDependencyDigest = digest
			}
			builder := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			_, err = builder.ProcessOne(t.Context(), build.ID)
			if (err == nil) != ack {
				t.Fatalf("acknowledgement: %v", err)
			}
			if vm.lastRequest.BuildDependencyDigest != digest {
				t.Fatal("guest omitted selected dependency inputs")
			}
			after, _ := store.BuildByID(t.Context(), build.ID)
			if ack && after.Status != state.BuildSucceeded || !ack && after.Status != state.BuildFailed {
				t.Fatalf("incorrect completion: %+v", after)
			}
		})
	}
}
