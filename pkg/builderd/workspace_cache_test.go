package builderd

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProcessOneWorkspaceCacheIsolation(t *testing.T) {
	for _, framework := range []struct{ name, marker string }{{"railpack", "package.json"}, {"dockerfile", "Dockerfile"}} {
		t.Run(framework.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			archive := filepath.Join(t.TempDir(), "workspace.tar.gz")
			makeTarballWithName(t, archive, []string{framework.marker, "apps/api/" + framework.marker, "apps/web/" + framework.marker})
			acct, err := store.CreateAccount(ctx, "workspace@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			apps := map[string]state.App{}
			for _, name := range []string{"api", "web", "root"} {
				app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: name, RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5})
				if err != nil {
					t.Fatal(err)
				}
				apps[name] = app
			}
			vm := &fakeVM{}
			b := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			steps := []struct {
				app, root string
				hit       bool
			}{
				{"api", "apps/api", false}, {"web", "apps/web", false}, {"root", "", false},
				{"api", "apps/api", true}, {"web", "apps/web", true}, {"root", ".", true},
			}
			for _, step := range steps {
				t.Run(step.app+map[bool]string{false: "-build", true: "-cached"}[step.hit], func(t *testing.T) {
					dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: apps[step.app].ID, Kind: state.DeploymentKindTarball, SourcePath: archive, SourceRoot: step.root, SourceBytes: 100, LogPath: filepath.Join(t.TempDir(), "build.log")})
					if err != nil {
						t.Fatal(err)
					}
					build, err := store.CreateBuild(ctx, dep.ID, dep.Kind, 100, dep.LogPath)
					if err != nil {
						t.Fatal(err)
					}
					// Give each VM execution a distinct artifact. Reading the committed
					// output catches a wrong hit, not merely a key mismatch.
					layer := filepath.Join(t.TempDir(), "image.tar")
					if err := os.WriteFile(layer, []byte(step.app+" artifact"), 0600); err != nil {
						t.Fatal(err)
					}
					vm.out = BuildOutcome{OCIImage: layer}
					before := vm.spawnCalls
					result, err := b.ProcessOne(ctx, build.ID)
					if err != nil {
						t.Fatal(err)
					}
					if result.CacheHit != step.hit {
						t.Fatalf("cache hit=%v, want %v for source_root=%q", result.CacheHit, step.hit, step.root)
					}
					wantSpawns := before
					if !step.hit {
						wantSpawns++
					}
					if vm.spawnCalls != wantSpawns {
						t.Fatalf("spawns=%d want %d", vm.spawnCalls, wantSpawns)
					}
					got, err := store.DeploymentByID(ctx, dep.ID)
					if err != nil {
						t.Fatal(err)
					}
					data, err := os.ReadFile(got.RootfsPath)
					if err != nil {
						t.Fatal(err)
					}
					if string(data) != step.app+" artifact" {
						t.Fatalf("wrong workspace artifact: %q", data)
					}
					prov, err := store.BuildProvenanceByBuildID(ctx, build.ID)
					if err != nil {
						t.Fatal(err)
					}
					sourceHash, err := hashFile(archive)
					if err != nil {
						t.Fatal(err)
					}
					if prov.SourceSHA256 != sourceHash {
						t.Fatal("recipe key replaced provenance's source digest")
					}
				})
			}
		})
	}
}
