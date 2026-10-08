package state_test

// adr: 732
// Production fork intent: both stores share validation, the live-deployment
// requirement, atomic active-fork limits and the cancellation rules.

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type appForkFixture struct {
	store                     state.Store
	accountID, appID, liveDep string
	// nodeID is a compute node valid for instance rows: the default-local
	// node on Postgres, empty on MemStore.
	nodeID string
}

// appForkStores returns the MemStore fixture and, when DATABASE_URL is set,
// the PgStore one, each seeded with an account, an app and a live deployment.
func appForkStores(t *testing.T) map[string]appForkFixture {
	t.Helper()
	out := map[string]appForkFixture{"mem": seedAppForkFixture(t, state.NewMemStore())}
	if testing.Short() || os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		return out
	}
	s, ctx := pgStore(t)
	pg := seedAppForkFixture(t, s)
	pg.nodeID = resolveDefaultLocal(t, ctx, s)
	out["pg"] = pg
	return out
}

func seedAppForkFixture(t *testing.T, s state.Store) appForkFixture {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	acct, err := s.CreateAccount(ctx, "fork-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "fork-" + suffix, Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:fork", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	return appForkFixture{store: s, accountID: acct.ID, appID: app.ID, liveDep: dep.ID}
}

func (f appForkFixture) params(at time.Time) state.CreateAppForkParams {
	return state.CreateAppForkParams{
		AccountID: f.accountID, AppID: f.appID, DeploymentID: f.liveDep,
		RequestedBy: "user:test", TTLSeconds: 3600, MaxPerApp: 1, MaxPerAccount: 2, CreatedAt: at,
	}
}

var forkT0 = time.Date(2026, 10, 8, 12, 0, 0, 123456789, time.UTC)

func TestAppFork_CreateQueuesWithExactExpiry(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			fork, err := f.store.CreateAppFork(context.Background(), f.params(forkT0))
			if err != nil {
				t.Fatalf("CreateAppFork: %v", err)
			}
			wantCreated := forkT0.Truncate(time.Microsecond)
			if fork.Status != state.AppForkQueued || fork.DeploymentID != f.liveDep || fork.RequestedBy != "user:test" {
				t.Fatalf("fork = %+v, want queued on the live deployment", fork)
			}
			if !fork.CreatedAt.Equal(wantCreated) || !fork.ExpiresAt.Equal(wantCreated.Add(time.Hour)) {
				t.Fatalf("created/expires = %v/%v, want %v/+1h", fork.CreatedAt, fork.ExpiresAt, wantCreated)
			}
			got, err := f.store.AppForkByID(context.Background(), f.accountID, f.appID, fork.ID)
			if err != nil || got.ID != fork.ID {
				t.Fatalf("AppForkByID = %+v, %v", got, err)
			}
		})
	}
}

func TestAppFork_CreateRejectsInvalidIntent(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			for label, mutate := range map[string]func(*state.CreateAppForkParams){
				"ttl too short":   func(p *state.CreateAppForkParams) { p.TTLSeconds = 59 },
				"ttl too long":    func(p *state.CreateAppForkParams) { p.TTLSeconds = 86401 },
				"no requester":    func(p *state.CreateAppForkParams) { p.RequestedBy = "  " },
				"zero app limit":  func(p *state.CreateAppForkParams) { p.MaxPerApp = 0 },
				"no created time": func(p *state.CreateAppForkParams) { p.CreatedAt = time.Time{} },
			} {
				p := f.params(forkT0)
				mutate(&p)
				if _, err := f.store.CreateAppFork(context.Background(), p); !errors.Is(err, state.ErrAppForkInvalid) {
					t.Errorf("%s: err = %v, want ErrAppForkInvalid", label, err)
				}
			}
		})
	}
}

func TestAppFork_CreateRequiresTheLiveDeployment(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			other, err := f.store.CreateDeployment(ctx, state.Deployment{
				AppID: f.appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:other", Status: state.DeployPending,
			})
			if err != nil {
				t.Fatalf("CreateDeployment: %v", err)
			}
			p := f.params(forkT0)
			p.DeploymentID = other.ID
			if _, err := f.store.CreateAppFork(ctx, p); !errors.Is(err, state.ErrAppForkDeploymentUnavailable) {
				t.Fatalf("non-live deployment err = %v, want ErrAppForkDeploymentUnavailable", err)
			}
			p = f.params(forkT0)
			p.AccountID = uuid.NewString()
			if _, err := f.store.CreateAppFork(ctx, p); !errors.Is(err, state.ErrAppForkDeploymentUnavailable) {
				t.Fatalf("foreign account err = %v, want ErrAppForkDeploymentUnavailable", err)
			}
		})
	}
}

func TestAppFork_ActiveLimits(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if _, err := f.store.CreateAppFork(ctx, f.params(forkT0)); err != nil {
				t.Fatalf("first fork: %v", err)
			}
			_, err := f.store.CreateAppFork(ctx, f.params(forkT0.Add(time.Minute)))
			var limitErr *state.AppForkLimitError
			if !errors.As(err, &limitErr) || limitErr.Scope != "app" || limitErr.Limit != 1 || limitErr.Observed != 1 {
				t.Fatalf("second fork err = %v, want app limit 1 of 1", err)
			}
			// An expired-but-unswept fork no longer counts.
			if _, err := f.store.CreateAppFork(ctx, f.params(forkT0.Add(2*time.Hour))); err != nil {
				t.Fatalf("fork after expiry: %v", err)
			}
		})
	}
}

// TestAppFork_ConcurrentCreatesHonourTheLimit pins the advisory lock: of
// eight concurrent requests against a one-per-app limit, exactly one wins.
func TestAppFork_ConcurrentCreatesHonourTheLimit(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			var wg sync.WaitGroup
			var mu sync.Mutex
			created, limited := 0, 0
			for range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := f.store.CreateAppFork(context.Background(), f.params(forkT0))
					var limitErr *state.AppForkLimitError
					mu.Lock()
					defer mu.Unlock()
					switch {
					case err == nil:
						created++
					case errors.As(err, &limitErr):
						limited++
					default:
						t.Errorf("unexpected err: %v", err)
					}
				}()
			}
			wg.Wait()
			if created != 1 || limited != 7 {
				t.Fatalf("created=%d limited=%d, want 1/7", created, limited)
			}
		})
	}
}

func TestAppFork_CancelQueuedIsTerminalAndIdempotent(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			fork, err := f.store.CreateAppFork(ctx, f.params(forkT0))
			if err != nil {
				t.Fatalf("CreateAppFork: %v", err)
			}
			at := forkT0.Add(time.Minute)
			cancelled, err := f.store.RequestAppForkCancellation(ctx, f.accountID, f.appID, fork.ID, at)
			if err != nil {
				t.Fatalf("cancel: %v", err)
			}
			if cancelled.Status != state.AppForkCancelled || cancelled.FinishedAt == nil || cancelled.CancelRequested == nil {
				t.Fatalf("cancelled = %+v, want terminal with timestamps", cancelled)
			}
			again, err := f.store.RequestAppForkCancellation(ctx, f.accountID, f.appID, fork.ID, at.Add(time.Minute))
			if err != nil || again.Status != state.AppForkCancelled || !again.FinishedAt.Equal(*cancelled.FinishedAt) {
				t.Fatalf("second cancel = %+v, %v; want unchanged terminal row", again, err)
			}
			if _, err := f.store.RequestAppForkCancellation(ctx, f.accountID, f.appID, uuid.NewString(), at); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("unknown fork err = %v, want ErrNotFound", err)
			}
			// A cancelled fork frees the per-app slot.
			if _, err := f.store.CreateAppFork(ctx, f.params(at)); err != nil {
				t.Fatalf("fork after cancel: %v", err)
			}
		})
	}
}

func TestAppFork_ListIsScopedAndNewestFirst(t *testing.T) {
	for name, f := range appForkStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			var ids []string
			for i := range 3 {
				p := f.params(forkT0.Add(time.Duration(i) * time.Minute))
				p.MaxPerApp, p.MaxPerAccount = 5, 5
				fork, err := f.store.CreateAppFork(ctx, p)
				if err != nil {
					t.Fatalf("CreateAppFork %d: %v", i, err)
				}
				ids = append(ids, fork.ID)
			}
			got, err := f.store.ListAppForks(ctx, f.accountID, f.appID, 2)
			if err != nil {
				t.Fatalf("ListAppForks: %v", err)
			}
			if len(got) != 2 || got[0].ID != ids[2] || got[1].ID != ids[1] {
				t.Fatalf("list = %v, want newest two of %v", got, ids)
			}
			if foreign, err := f.store.ListAppForks(ctx, uuid.NewString(), f.appID, 10); err != nil || len(foreign) != 0 {
				t.Fatalf("foreign list = %v, %v; want empty", foreign, err)
			}
		})
	}
}
