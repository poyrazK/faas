package grace_test

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/grace"
	"github.com/onebox-faas/faas/pkg/logarchive"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
	"github.com/onebox-faas/faas/pkg/storage"
	"strings"
	"testing"
	"time"
)

func TestArchiveCleanupIncludesPendingAndAccountTombstones(t *testing.T) {
	for _, accountDeletion := range []bool{false, true} {
		for _, missingRoute := range []bool{false, true} {
			name := "app"
			if accountDeletion {
				name = "account"
			}
			if missingRoute {
				name += "-route-missing"
			}
			t.Run(name, func(t *testing.T) {
				s := state.NewMemStore()
				ctx := t.Context()
				fx := conformance.Seed(t, s)
				id := "eu-grace-logs"
				if _, err := s.UpsertComputeRegion(ctx, state.ComputeRegion{ID: id, DisplayName: id, AcceptingNewApps: true}); err != nil {
					t.Fatal(err)
				}
				app := fx.App
				app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "logs-cleanup", "", id
				app, err := s.CreateApp(ctx, app)
				if err != nil {
					t.Fatal(err)
				}
				dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage})
				if err != nil {
					t.Fatal(err)
				}
				node := fx.Node
				node.ID, node.Name, node.Region = "", "logs-cleanup", &id
				node, err = s.CreateComputeNode(ctx, node)
				if err != nil {
					t.Fatal(err)
				}
				instance, err := s.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, node.ID, "")
				if err != nil {
					t.Fatal(err)
				}
				binding, err := s.CaptureRuntimeLogArchiveBinding(ctx, instance.ID, node.ID)
				if err != nil {
					t.Fatal(err)
				}
				ref := state.RuntimeLogArchive{RuntimeLogArchiveBinding: binding, Day: "2026-10-02", StorageKey: region.RuntimeLogArchiveKey(id, instance.ID, "2026-10-02")}
				if err := s.ReserveRuntimeLogArchive(ctx, ref); err != nil {
					t.Fatal(err)
				} // Simulate a PUT accepted before the publisher timed out.
				legacy, _ := storage.NewLocalStorageBackend(t.TempDir())
				eu, _ := storage.NewLocalStorageBackend(t.TempDir())
				other, _ := storage.NewLocalStorageBackend(t.TempDir())
				relative := region.RuntimeLogArchiveKey("", instance.ID, ref.Day)
				for _, be := range []storage.StorageBackend{legacy, eu, other} {
					if err := be.Put(ctx, relative, strings.NewReader("archive")); err != nil {
						t.Fatal(err)
					}
				}
				routes := map[string]storage.StorageBackend{id: eu, "us-grace-logs": other}
				if missingRoute {
					delete(routes, id)
				}
				router, _ := storage.NewRegionRouter(routes, legacy)
				if err := s.DeleteInstance(ctx, instance.ID); err != nil {
					t.Fatal(err)
				}
				// Account cleanup must also include apps hidden by the ordinary app list.
				if _, err := s.ScheduleAppDeletion(ctx, app.ID, time.Now().Add(-time.Minute)); err != nil {
					t.Fatal(err)
				}
				now := time.Now()
				if accountDeletion {
					if err := s.MarkAccountDeletionPending(ctx, fx.Account.ID); err != nil {
						t.Fatal(err)
					}
					now = now.Add(31 * 24 * time.Hour)
				}
				g := grace.New(grace.Params{Store: s, LogArchives: &logarchive.RoutedClient{Regional: router}, Now: func() time.Time { return now }})
				if accountDeletion {
					err = g.RunOnce(ctx)
				} else {
					err = g.RunAppsOnce(ctx)
				}
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.AppByID(ctx, app.ID)
				if missingRoute {
					if err != nil {
						t.Fatalf("unrouted archive lost metadata: %v", err)
					}
				} else if !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("archive app retained: %v", err)
				}
				r, err := eu.Get(ctx, relative)
				if r != nil {
					_ = r.Close()
				}
				if missingRoute && err != nil {
					t.Fatal(err)
				}
				if !missingRoute && !storage.IsNotFound(err) {
					t.Fatalf("archive retained: %v", err)
				}
				for _, be := range []storage.StorageBackend{legacy, other} {
					r, err := be.Get(ctx, relative)
					if err != nil {
						t.Fatalf("cleanup crossed placement: %v", err)
					}
					_ = r.Close()
				}
			})
		}
	}
}
