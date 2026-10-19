package state_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperatorReadProjectionsStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			t.Run("empty and canceled reads", func(t *testing.T) {
				profiles, err := store.OperatorCapacityProfiles(ctx)
				if err != nil || profiles == nil || len(profiles) != 0 {
					t.Fatalf("empty profiles=%+v err=%v", profiles, err)
				}
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				if _, err := store.OperatorCapacityProfiles(canceled); !errors.Is(err, context.Canceled) {
					t.Fatalf("capacity cancellation: %v", err)
				}
			})
			account, err := store.CreateAccount(ctx, "obs-projections@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			t.Run("exact tenant history and isolation", func(t *testing.T) {
				apps, err := store.OperatorTenantApps(ctx, account.ID)
				if err != nil || apps == nil || len(apps) != 0 {
					t.Fatalf("empty tenant apps=%+v err=%v", apps, err)
				}
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				if _, err := store.OperatorTenantApps(canceled, account.ID); !errors.Is(err, context.Canceled) {
					t.Fatalf("tenant cancellation: %v", err)
				}
				app := projectionApp(t, ctx, store, account.ID, "long-history", 256, 500)
				empty := projectionApp(t, ctx, store, account.ID, "no-deployments", 128, 250)
				old := projectionDeployment(t, ctx, store, app.ID)
				if err := store.UpdateDeploymentStatus(ctx, old.ID, state.DeployLive, ""); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 1005; i++ {
					projectionDeployment(t, ctx, store, app.ID)
				}
				other, err := store.CreateAccount(ctx, "other-obs-projections@example.com", api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				foreign := projectionApp(t, ctx, store, other.ID, "foreign-tenant", 128, 250)
				foreignDeployment := projectionDeployment(t, ctx, store, foreign.ID)
				if err := store.UpdateDeploymentStatus(ctx, foreignDeployment.ID, state.DeployLive, ""); err != nil {
					t.Fatal(err)
				}
				deleted := projectionApp(t, ctx, store, account.ID, "deleted-tenant-app", 128, 250)
				projectionDeployment(t, ctx, store, deleted.ID)
				if _, err := store.SoftDeleteAppCascade(ctx, deleted.ID); err != nil {
					t.Fatal(err)
				}
				apps, err = store.OperatorTenantApps(ctx, account.ID)
				if err != nil || len(apps) != 2 {
					t.Fatalf("tenant apps=%+v err=%v", apps, err)
				}
				counts := map[string]int{}
				for _, row := range apps {
					counts[row.ID] = row.Deployments
				}
				if !reflect.DeepEqual(counts, map[string]int{app.ID: 1, empty.ID: 0}) {
					t.Fatalf("history cap or tenant boundary changed counts: %+v", apps)
				}
				_, live, err := store.OperatorTenantCounts(ctx, account.ID)
				if err != nil || live != 1 {
					t.Fatalf("tenant summary disagrees: live=%d err=%v", live, err)
				}
			})
		})
	}
}

func TestOperatorCapacityProfilesStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			ctx := context.Background()
			if backend == "postgres" {
				store, ctx = pgStore(t)
			}
			account, err := store.CreateAccount(ctx, "obs-profiles@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			want := []api.ObsCapacityProfile{}
			for i, profile := range api.ResourceProfiles {
				app := projectionApp(t, ctx, store, account.ID, fmt.Sprintf("profile-%d", i), profile.MemoryMB, profile.CPUMillicores)
				deployment := projectionDeployment(t, ctx, store, app.ID)
				for _, status := range []state.State{state.StateRunning, state.StateWaking, state.StateColdBooting, state.StateParked, state.StateDraining, state.StateWarm} {
					if _, err := store.CreateInstance(ctx, app.ID, deployment.ID, string(status), app.RAMMB, node.ID, ""); err != nil {
						t.Fatalf("create %s instance: %v", status, err)
					}
				}
				want = append(want, api.ObsCapacityProfile{ResourceProfile: string(profile.Name), MemoryMB: profile.MemoryMB, CPUMillicores: profile.CPUMillicores,
					Apps: 1, LiveInstances: 3, ReservedMemoryMB: int64(profile.MemoryMB), ReservedCPUMillicores: int64(profile.CPUMillicores)})
			}
			custom := api.ObsCapacityProfile{ResourceProfile: "custom"}
			// Different arbitrary shapes must share one bounded custom bucket.
			for i := 0; i < 24; i++ {
				app := projectionApp(t, ctx, store, account.ID, fmt.Sprintf("custom-%d", i), 384+i, []int{250, 500, 1000}[i%3])
				custom.Apps++
				custom.ReservedMemoryMB += int64(app.RAMMB)
				custom.ReservedCPUMillicores += int64(app.CPUMillicores)
			}
			deleted := projectionApp(t, ctx, store, account.ID, "deleted-profile", 128, 250)
			deployment := projectionDeployment(t, ctx, store, deleted.ID)
			if _, err := store.CreateInstance(ctx, deleted.ID, deployment.ID, string(state.StateRunning), 128, node.ID, ""); err != nil {
				t.Fatal(err)
			}
			if _, err := store.SoftDeleteAppCascade(ctx, deleted.ID); err != nil {
				t.Fatal(err)
			}
			want = append(want, custom)
			got, err := store.OperatorCapacityProfiles(ctx)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("profiles=%+v\nwant=%+v\nerr=%v", got, want, err)
			}
		})
	}
}

func TestOperatorReadProjectionDatabaseFailures(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	pool.Close()
	if _, err := store.OperatorTenantApps(ctx, "11111111-1111-4111-8111-111111111111"); err == nil {
		t.Fatal("tenant projection returned success after its database closed")
	}
	if _, err := store.OperatorCapacityProfiles(ctx); err == nil {
		t.Fatal("capacity projection returned success after its database closed")
	}
}

func projectionApp(t *testing.T, ctx context.Context, store state.Store, accountID, slug string, memory, cpu int) state.App {
	t.Helper()
	app, err := store.CreateApp(ctx, state.App{AccountID: accountID, Slug: slug, Type: state.AppTypeApp, RAMMB: memory, CPUMillicores: cpu, MaxConcurrency: 20})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func projectionDeployment(t *testing.T, ctx context.Context, store state.Store, appID string) state.Deployment {
	t.Helper()
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:obs-projection", Status: state.DeployPending, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	return deployment
}
