package state_test

// adr: 20261103131742 — a billing subject's source builds count toward its
// plan's monthly build minutes: each build that started in the month counts
// from its start to its finish, or to the end of the window while it runs.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// buildMinutesTestStore is the surface the build-minutes contract uses; the
// MemStore and PgStore both implement it.
type buildMinutesTestStore interface {
	state.BuildMinutesStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateApp(context.Context, state.App) (state.App, error)
	CreateDeployment(context.Context, state.Deployment) (state.Deployment, error)
	CreateBuild(context.Context, string, state.DeploymentKind, int64, string) (state.Build, error)
}

// setBuildTimes stamps a build's start and finish; a zero finish leaves it
// running.
type setBuildTimes func(id string, started, finished time.Time) error

func TestBillingSubjectBuildSeconds(t *testing.T) {
	t.Run("memstore", func(t *testing.T) {
		store := state.NewMemStore()
		exerciseBillingSubjectBuildSeconds(t, context.Background(), store, store.SetBuildTimesForTest)
	})
	t.Run("pgstore", func(t *testing.T) {
		s, pool, ctx := pgStoreWithPool(t)
		exerciseBillingSubjectBuildSeconds(t, ctx, s, func(id string, started, finished time.Time) error {
			status, end := string(state.BuildSucceeded), any(finished)
			if finished.IsZero() {
				status, end = string(state.BuildRunning), nil
			}
			_, err := pool.Exec(ctx, `UPDATE builds SET status = $2, started_at = $3, finished_at = $4 WHERE id = $1`, id, status, started, end)
			return err
		})
	})
}

func exerciseBillingSubjectBuildSeconds(t *testing.T, ctx context.Context, store buildMinutesTestStore, stamp setBuildTimes) {
	t.Helper()
	deployment := func() (string, string) {
		t.Helper()
		acct, err := store.CreateAccount(ctx, "build-minutes-"+uuid.NewString()+"@example.com", api.PlanFree)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{
			AccountID: acct.ID, Slug: "build-minutes-" + uuid.NewString()[:8], Type: state.AppTypeApp,
			RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60,
		})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		return acct.ID, dep.ID
	}
	build := func(depID string, started, finished time.Time) {
		t.Helper()
		b, err := store.CreateBuild(ctx, depID, state.DeploymentKindTarball, 1, "")
		if err != nil {
			t.Fatal(err)
		}
		if started.IsZero() {
			return
		}
		if err := stamp(b.ID, started, finished); err != nil {
			t.Fatal(err)
		}
	}
	month := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := month.AddDate(0, 1, 0)
	at := func(day, hour, minute int) time.Time {
		return time.Date(2026, time.January, day, hour, minute, 0, 0, time.UTC)
	}

	subject, dep := deployment()
	build(dep, at(2, 10, 0), at(2, 11, 40))                 // 100 minutes
	build(dep, month.Add(-time.Hour), month.Add(time.Hour)) // started last month
	build(dep, at(31, 23, 50), time.Time{})                 // running: 10 minutes to the window's end
	build(dep, time.Time{}, time.Time{})                    // queued, never started
	build(dep, end, end.Add(time.Hour))                     // started next month
	other, otherDep := deployment()
	build(otherDep, at(3, 0, 0), at(3, 1, 0))

	for _, tc := range []struct {
		name    string
		subject string
		want    int64
	}{
		{"own builds in the month", subject, 110 * 60},
		{"another subject's builds", other, 60 * 60},
		{"subject without builds", uuid.NewString(), 0},
	} {
		got, err := store.BillingSubjectBuildSeconds(ctx, tc.subject, month, end)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: build seconds = %d, want %d", tc.name, got, tc.want)
		}
	}
}
