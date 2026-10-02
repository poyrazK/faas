package grace_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/grace"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestRegionalSBOMAppCleanup(t *testing.T) {
	for _, missingRoute := range []bool{false, true} {
		name := "routed"
		if missingRoute {
			name = "missing-route"
		}
		t.Run(name, func(t *testing.T) {
			s := state.NewMemStore()
			ctx := t.Context()
			a, err := s.CreateAccount(ctx, "regional-cleanup@example.com", "pro")
			if err != nil {
				t.Fatal(err)
			}
			id := "eu-sbom"
			if _, err := s.UpsertComputeRegion(ctx, state.ComputeRegion{ID: id, DisplayName: id, AcceptingNewApps: true}); err != nil {
				t.Fatal(err)
			}
			app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "regional-cleanup", ComputeRegion: id})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball})
			if err != nil {
				t.Fatal(err)
			}
			build, err := s.CreateBuild(ctx, dep.ID, dep.Kind, 0, "")
			if err != nil {
				t.Fatal(err)
			}
			key := region.BuildSBOMKey(id, build.ID)
			if err := s.CreateBuildProvenance(ctx, state.BuildProvenance{BuildID: build.ID, SBOMStorageKey: key, StartedAt: time.Now(), FinishedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			legacy, _ := storage.NewLocalStorageBackend(t.TempDir())
			eu, _ := storage.NewLocalStorageBackend(t.TempDir())
			us, _ := storage.NewLocalStorageBackend(t.TempDir())
			relative := region.BuildSBOMKey("", build.ID)
			for _, b := range []storage.StorageBackend{legacy, eu, us} {
				if err := b.Put(ctx, relative, strings.NewReader("SBOM")); err != nil {
					t.Fatal(err)
				}
			}
			routes := map[string]storage.StorageBackend{id: eu, "us-sbom": us}
			if missingRoute {
				delete(routes, id)
			}
			backend, err := storage.NewRegionRouter(routes, legacy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.ScheduleAppDeletion(ctx, app.ID, time.Now().Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			g := grace.New(grace.Params{Store: s, Artifacts: backend})
			if err := g.RunAppsOnce(ctx); err != nil {
				t.Fatal(err)
			} // Missing routes retain the tombstone for retry.
			_, err = s.AppByID(ctx, app.ID)
			if missingRoute {
				if err != nil {
					t.Fatalf("missing route discarded metadata: %v", err)
				}
			} else if !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("app not removed: %v", err)
			}
			r, err := eu.Get(ctx, relative)
			if missingRoute {
				if err != nil {
					t.Fatal(err)
				}
			} else if !storage.IsNotFound(err) {
				t.Fatalf("regional SBOM retained: %v", err)
			}
			if r != nil {
				_ = r.Close()
			}
			for _, other := range []storage.StorageBackend{legacy, us} {
				r, err := other.Get(ctx, relative)
				if err != nil {
					t.Fatalf("cleanup crossed region: %v", err)
				}
				_ = r.Close()
			}
		})
	}
}
