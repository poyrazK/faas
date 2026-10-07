//go:build !no_pg

// adr: 641
package state_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func imagePromotionBackends(t *testing.T, check func(*testing.T, state.Store)) {
	t.Helper()
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				store, _ = pgStore(t)
			}
			check(t, store)
		})
	}
}

func imagePromotionApp(t *testing.T, store state.Store, slug string) state.App {
	t.Helper()
	account, err := store.CreateAccount(t.Context(), slug+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func imagePromotionDeployment(t *testing.T, store state.Store, appID, scope string) state.Deployment {
	t.Helper()
	d, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: appID, Kind: state.DeploymentKindImage,
		Scope: scope, ImageDigest: "example.com/api@sha256:" + strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestImagePromotionNewerIntentBlocksOlderCandidate(t *testing.T) {
	imagePromotionBackends(t, func(t *testing.T, store state.Store) {
		for _, status := range []state.DeploymentStatus{state.DeployPending, state.DeployFailed, state.DeployCancelled} {
			t.Run(string(status), func(t *testing.T) {
				app := imagePromotionApp(t, store, "image-order-"+string(status))
				stable := imagePromotionDeployment(t, store, app.ID, "default")
				if err := store.MarkDeploymentLive(t.Context(), stable.ID); err != nil {
					t.Fatal(err)
				}
				older := imagePromotionDeployment(t, store, app.ID, "default")
				if err := store.UpdateDeploymentStatus(t.Context(), older.ID, state.DeployImaging, ""); err != nil {
					t.Fatal(err)
				}
				newer := imagePromotionDeployment(t, store, app.ID, "default")
				if status != state.DeployPending {
					if err := store.UpdateDeploymentStatus(t.Context(), newer.ID, status, "newer intent outcome"); err != nil {
						t.Fatal(err)
					}
				}
				if err := store.MarkDeploymentLiveIfLatest(t.Context(), older.ID); !errors.Is(err, state.ErrDeploymentSuperseded) {
					t.Fatalf("older promotion after newer %s = %v", status, err)
				}
				old, err := store.DeploymentByID(t.Context(), older.ID)
				if err != nil || old.Status != state.DeploySuperseded || old.TrafficPercent != 0 {
					t.Fatalf("stale image still eligible: %+v, %v", old, err)
				}
				serving, err := store.LiveDeploymentForScope(t.Context(), app.ID, "default")
				if err != nil || serving.ID != stable.ID || serving.TrafficPercent != 100 {
					t.Fatalf("serving release changed: %+v, %v", serving, err)
				}
			})
		}
		t.Run("newer source intent", func(t *testing.T) {
			app := imagePromotionApp(t, store, "image-order-source")
			older := imagePromotionDeployment(t, store, app.ID, "default")
			if err := store.UpdateDeploymentStatus(t.Context(), older.ID, state.DeployImaging, ""); err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball}); err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDeploymentLiveIfLatest(t.Context(), older.ID); !errors.Is(err, state.ErrDeploymentSuperseded) {
				t.Fatalf("image ignored newer source intent: %v", err)
			}
		})
	})
}

func TestImagePromotionScopeAndLiveReplay(t *testing.T) {
	imagePromotionBackends(t, func(t *testing.T, store state.Store) {
		for _, scope := range []string{"", "production"} {
			t.Run("scope-"+scope, func(t *testing.T) {
				label := scope
				if label == "" {
					label = "default"
				}
				app := imagePromotionApp(t, store, "image-order-scope-"+label)
				production := imagePromotionDeployment(t, store, app.ID, scope)
				if err := store.UpdateDeploymentStatus(t.Context(), production.ID, state.DeployImaging, ""); err != nil {
					t.Fatal(err)
				}
				imagePromotionDeployment(t, store, app.ID, "staging")
				if err := store.MarkDeploymentLiveIfLatest(t.Context(), production.ID); err != nil {
					t.Fatalf("staging intent blocked independent scope: %v", err)
				}
			})
		}
		for _, canary := range []bool{false, true} {
			t.Run(fmt.Sprintf("live-replay-canary-%t", canary), func(t *testing.T) {
				app := imagePromotionApp(t, store, fmt.Sprintf("image-live-replay-%t", canary))
				prior := imagePromotionDeployment(t, store, app.ID, "default")
				if err := store.MarkDeploymentLive(t.Context(), prior.ID); err != nil {
					t.Fatal(err)
				}
				input := state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:canary",
					TrafficPercent: 10, TrafficPercentExplicit: true}
				if canary {
					input.CanaryPreset, input.CanaryTotalSteps = "balanced", 4
					input.TrafficPercent = 1
				}
				candidate, err := store.CreateDeployment(t.Context(), input)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLiveIfLatest(t.Context(), candidate.ID); err != nil {
					t.Fatal(err)
				}
				before, err := store.DeploymentByID(t.Context(), candidate.ID)
				if err != nil {
					t.Fatal(err)
				}
				imagePromotionDeployment(t, store, app.ID, "default")
				if err := store.MarkDeploymentLiveIfLatest(t.Context(), candidate.ID); err != nil {
					t.Fatalf("already-live replay rejected: %v", err)
				}
				after, err := store.DeploymentByID(t.Context(), candidate.ID)
				if err != nil || after.Status != state.DeployLive || after.TrafficPercent != before.TrafficPercent || after.CanaryStep != before.CanaryStep || after.RolloutState != before.RolloutState {
					t.Fatalf("live rollout changed: before=%+v after=%+v err=%v", before, after, err)
				}
				residual, err := store.DeploymentByID(t.Context(), prior.ID)
				if err != nil || residual.Status != state.DeployLive || residual.TrafficPercent+after.TrafficPercent != 100 {
					t.Fatalf("traffic split changed: %+v, %v", residual, err)
				}
			})
		}
	})
}

func TestImagePromotionConcurrentCandidates(t *testing.T) {
	imagePromotionBackends(t, func(t *testing.T, store state.Store) {
		for round := 0; round < 3; round++ {
			app := imagePromotionApp(t, store, fmt.Sprintf("image-order-race-%d", round))
			older := imagePromotionDeployment(t, store, app.ID, "default")
			if err := store.UpdateDeploymentStatus(t.Context(), older.ID, state.DeployImaging, ""); err != nil {
				t.Fatal(err)
			}
			newer := imagePromotionDeployment(t, store, app.ID, "default")
			start := make(chan struct{})
			results := make(chan struct {
				id  string
				err error
			}, 2)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			for _, id := range []string{older.ID, newer.ID} {
				go func() {
					<-start
					results <- struct {
						id  string
						err error
					}{id, store.MarkDeploymentLiveIfLatest(ctx, id)}
				}()
			}
			close(start)
			for range 2 {
				select {
				case result := <-results:
					if result.id == older.ID && !errors.Is(result.err, state.ErrDeploymentSuperseded) || result.id == newer.ID && result.err != nil {
						cancel()
						t.Fatalf("concurrent promotion %s: %v", result.id, result.err)
					}
				case <-ctx.Done():
					cancel()
					t.Fatal("concurrent promotion did not complete")
				}
			}
			cancel()
			live, err := store.LiveDeploymentForScope(t.Context(), app.ID, "default")
			if err != nil || live.ID != newer.ID {
				t.Fatalf("older candidate won concurrent cutover: %+v, %v", live, err)
			}
		}
	})
}

func TestPgImagePromotionWaitsForAdmissionCommit(t *testing.T) {
	store, pool, _ := pgStoreWithPool(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	app := imagePromotionApp(t, store, "image-order-admission-lock")
	stable := imagePromotionDeployment(t, store, app.ID, "default")
	if err := store.MarkDeploymentLive(ctx, stable.ID); err != nil {
		t.Fatal(err)
	}
	older := imagePromotionDeployment(t, store, app.ID, "default")
	if err := store.UpdateDeploymentStatus(ctx, older.ID, state.DeployImaging, ""); err != nil {
		t.Fatal(err)
	}
	// Hold admission's app lock with an uncommitted newer deployment. The
	// promotion must observe it after commit, instead of checking too early.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `select id from apps where id = $1 for update`, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `insert into deployments (app_id, kind, image_digest, status, scope, revision)
		values ($1, 'image', 'sha256:newer', 'pending', 'default', $2)`, app.ID, older.Revision+1); err != nil {
		t.Fatal(err)
	}
	config := pool.Config()
	// The rollback lookup needs a second connection while promotion holds
	// its transaction. Only one connection exists before the lock wait.
	config.MinConns, config.MaxConns = 0, 2
	promotionPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		promotionPool.Close()
	}()
	conn, err := promotionPool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pid := int64(conn.Conn().PgConn().PID())
	conn.Release()
	promoter := state.NewPgStore(promotionPool)
	result := make(chan error, 1)
	go func() { result <- promoter.MarkDeploymentLiveIfLatest(ctx, older.ID) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `select coalesce(wait_event_type = 'Lock', false) from pg_stat_activity where pid = $1::bigint`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("promotion bypassed uncommitted admission: %v", err)
		case <-ctx.Done():
			t.Fatal("promotion did not wait on the admission lock")
		case <-ticker.C:
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, state.ErrDeploymentSuperseded) {
		t.Fatalf("promotion missed committed newer intent: %v", err)
	}
	live, err := store.LiveDeploymentForScope(ctx, app.ID, "default")
	if err != nil || live.ID != stable.ID {
		t.Fatalf("concurrent admission changed serving release: %+v, %v", live, err)
	}
}
