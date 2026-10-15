//go:build !no_pg

package state

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// Fixtures need explicit timestamps and soft-deletion states which the public
// mutation methods intentionally do not accept.
type overviewFixture struct {
	store Store
	mem   *MemStore
	pool  *pgxpool.Pool
	ctx   context.Context
}

func newOverviewFixture(t *testing.T, backend string) overviewFixture {
	t.Helper()
	f := overviewFixture{ctx: context.Background()}
	if backend == "memory" {
		f.mem = NewMemStore()
		f.store = f.mem
	} else {
		f.pool = pgtest.OpenMigrated(t)
		if err := db.MigrateUp(f.ctx, f.pool); err != nil {
			t.Fatal(err)
		}
		f.store = NewPgStore(f.pool)
	}
	return f
}

func (f overviewFixture) account(t *testing.T, name string, created time.Time, verified *time.Time, status AccountStatus) Account {
	t.Helper()
	createdAccount, err := f.store.CreateAccountWithPersonalOrg(f.ctx, CreateAccountWithPersonalOrgParams{Email: name + "@overview.example", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	a := createdAccount.Account
	a.CreatedAt, a.EmailVerifiedAt, a.Status = created, verified, status
	if f.mem != nil {
		f.mem.accounts[a.ID] = a
	} else if _, err := f.pool.Exec(f.ctx, "UPDATE accounts SET created_at=$2, email_verified_at=$3, status=$4 WHERE id=$1", a.ID, created, verified, string(status)); err != nil {
		t.Fatal(err)
	}
	return a
}

func (f overviewFixture) workload(t *testing.T, name string, account Account, live bool) (App, Deployment) {
	t.Helper()
	a, err := f.store.CreateApp(f.ctx, App{AccountID: account.ID, Slug: name, Type: AppTypeApp, RAMMB: 128, CPUMillicores: 250, MaxConcurrency: 20})
	if err != nil {
		t.Fatal(err)
	}
	d, err := f.store.CreateDeployment(f.ctx, Deployment{AppID: a.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:overview", Status: DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if live {
		if err := f.store.UpdateDeploymentStatus(f.ctx, d.ID, DeployLive, ""); err != nil {
			t.Fatal(err)
		}
	}
	return a, d
}

func (f overviewFixture) request(t *testing.T, app App, deployment Deployment, at time.Time, status State) {
	t.Helper()
	node, err := f.store.ComputeNodeByName(f.ctx, DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	i, err := f.store.CreateInstance(f.ctx, app.ID, deployment.ID, string(status), 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !at.IsZero() {
		if _, err := f.store.TouchInstancesLastSeen(f.ctx, []InstanceTouch{{InstanceID: i.ID, LastRequest: at}}); err != nil {
			t.Fatal(err)
		}
	}
}

func (f overviewFixture) deleteApp(t *testing.T, app App) {
	t.Helper()
	if f.mem != nil {
		app.Status = AppDeleted
		f.mem.apps[app.ID] = app
	} else if _, err := f.pool.Exec(f.ctx, "UPDATE apps SET status='deleted' WHERE id=$1", app.ID); err != nil {
		t.Fatal(err)
	}
}

func TestOperatorOverviewStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newOverviewFixture(t, backend)
			now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
			got, err := f.store.OperatorOverview(f.ctx, now)
			if err != nil || got.Totals.AccountsActive != 0 || got.BetaFunnel14d != (api.ObsBetaFunnel{WindowStartedAt: now.Add(-api.ObsOverviewCohortWindow)}) || got.RecentFailures1h == nil || len(got.RecentFailures1h) != 0 || got.TopRateLimitedAccounts24h == nil || len(got.TopRateLimitedAccounts24h) != 0 || got.NodeHealth == nil {
				t.Fatalf("empty overview=%+v err=%v", got, err)
			}
			canceled, cancel := context.WithCancel(f.ctx)
			cancel()
			if _, err := f.store.OperatorOverview(canceled, now); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled overview: %v", err)
			}

			recent, verified, future := now.Add(-72*time.Hour), now.Add(-48*time.Hour), now.Add(time.Microsecond)
			unverified := f.account(t, "unverified", recent, nil, AccountActive)
			appOnly := f.account(t, "app", recent, &verified, AccountActive)
			live := f.account(t, "live", recent, &verified, AccountActive)
			fast := f.account(t, "fast", recent, &verified, AccountActive)
			slow := f.account(t, "slow", recent, &verified, AccountActive)
			old := f.account(t, "old", now.Add(-api.ObsOverviewCohortWindow-time.Microsecond), &verified, AccountPastDue)
			upcoming := f.account(t, "future", future, &verified, AccountSuspended)
			start := f.account(t, "start", now.Add(-api.ObsOverviewCohortWindow), &verified, AccountActive)
			end := f.account(t, "end", now, &now, AccountActive)
			invalid := f.account(t, "invalid", recent, &verified, AccountActive)
			deleted := f.account(t, "deleted-app", recent, &verified, AccountActive)
			deletedDeploy := f.account(t, "deleted-deployment", recent, &verified, AccountActive)
			futureVerified := f.account(t, "future-verified", recent, &future, AccountActive)
			historical := f.account(t, "historical", recent, &verified, AccountActive)
			for _, a := range []Account{unverified, live, old, upcoming, futureVerified} {
				app, dep := f.workload(t, "app-"+strings.Split(a.Email, "@")[0], a, true)
				at := a.CreatedAt.Add(time.Second)
				if a.ID == live.ID {
					at = time.Time{}
				}
				f.request(t, app, dep, at, StateParked)
				if a.ID == live.ID {
					f.request(t, app, dep, time.Time{}, StateWaking)
					f.request(t, app, dep, time.Time{}, StateColdBooting)
					f.request(t, app, dep, time.Time{}, StateDraining)
				}
			}
			a, d := f.workload(t, "app-no-live", appOnly, false)
			f.request(t, a, d, recent.Add(5*time.Second), StateParked)
			for _, sample := range []struct {
				account Account
				latency time.Duration
				status  State
			}{
				{fast, 10*time.Second + 900*time.Millisecond, StateRunning}, {slow, 100*time.Second + 900*time.Millisecond, StateParked},
				{start, 1900 * time.Millisecond, StateParked}, {end, 0, StateParked},
			} {
				a, d := f.workload(t, "sample-"+strings.Split(sample.account.Email, "@")[0], sample.account, true)
				f.request(t, a, d, sample.account.CreatedAt.Add(sample.latency), sample.status)
				if sample.account.ID == fast.ID {
					f.request(t, a, d, recent.Add(20*time.Second), StateParked)
					// An invalid earlier observation must not mask a valid sample.
					f.request(t, a, d, recent.Add(-time.Microsecond), StateParked)
				}
			}
			f.workload(t, "duplicate-fast", fast, true)
			a, d = f.workload(t, "invalid-times", invalid, true)
			f.request(t, a, d, recent.Add(-time.Microsecond), StateParked)
			f.request(t, a, d, future, StateParked)
			a, d = f.workload(t, "deleted-only", deleted, true)
			f.request(t, a, d, recent.Add(time.Second), StateRunning)
			f.deleteApp(t, a)
			_, d = f.workload(t, "deleted-live-deployment", deletedDeploy, true)
			if f.mem != nil {
				d = f.mem.deployments[d.ID]
				d.DeletedAt = &verified
				f.mem.deployments[d.ID] = d
			} else if _, err := f.pool.Exec(f.ctx, "UPDATE deployments SET deleted_at=$2 WHERE id=$1", d.ID, verified); err != nil {
				t.Fatal(err)
			}
			f.workload(t, "current-historical", historical, true)
			a, d = f.workload(t, "retained-historical", historical, false)
			f.request(t, a, d, recent.Add(5*time.Second), StateParked)
			f.deleteApp(t, a)

			if _, err := f.store.CreateOrg(f.ctx, Org{Name: "Overview", Slug: "overview"}); err != nil {
				t.Fatal(err)
			}
			seedOverviewNodes(t, f, now)
			seedOverviewEvents(t, f, now)
			got, err = f.store.OperatorOverview(f.ctx, now)
			if err != nil {
				t.Fatal(err)
			}
			wantFunnel := api.ObsBetaFunnel{WindowStartedAt: now.Add(-api.ObsOverviewCohortWindow), AccountsCreated: 12, EmailVerified: 10, WithApp: 9, WithLiveDeployment: 7, WithSuccessfulRequest: 5, SignupToFirstSuccessSamples: 5, SignupToFirstSuccessP50Seconds: 5, SignupToFirstSuccessP95Seconds: 100}
			if got.BetaFunnel14d != wantFunnel {
				t.Fatalf("funnel=%+v want=%+v", got.BetaFunnel14d, wantFunnel)
			}
			wantTotals := api.ObsOverviewTotals{AccountsActive: 12, AccountsPastDue: 1, AccountsSuspended: 1, AppsTotal: 14, InstancesLive: 2, InstancesWaking: 2, OrgsTotal: 15, NodesActive: 3, NodesInactive: 1, AuditEvents24h: 2}
			if got.Totals != wantTotals {
				t.Fatalf("totals=%+v want=%+v", got.Totals, wantTotals)
			}
			if !got.GeneratedAt.Equal(now) {
				t.Fatalf("generated_at=%s", got.GeneratedAt)
			}
			wantNodes := []api.ObsOverviewNodeHealth{
				{Name: DefaultLocalNodeName, Active: true, LastHeartbeatAt: now.Add(-api.ObsOverviewNodeStaleAfter)},
				{Name: "future", Active: true, LastHeartbeatAt: future},
				{Name: "stale", Active: false, LastHeartbeatAt: now.Add(-api.ObsOverviewNodeStaleAfter - time.Microsecond), Stale: true},
				{Name: "zero", Active: true},
			}
			if !reflect.DeepEqual(got.NodeHealth, wantNodes) {
				t.Fatalf("nodes=%+v want=%+v", got.NodeHealth, wantNodes)
			}
			wantFailures := []api.ObsOverviewFailureKind{{Kind: "a.failed", Count: 7}, {Kind: "b.failure", Count: 6}, {Kind: "c.error", Count: 5}, {Kind: "d.failed", Count: 4}, {Kind: "e.failed", Count: 3}, {Kind: "other", Count: 4}}
			if !reflect.DeepEqual(got.RecentFailures1h, wantFailures) {
				t.Fatalf("failures=%+v", got.RecentFailures1h)
			}
			wantRate := []api.ObsOverviewRateLimited{{AccountID: uuid.Nil.String(), Hits: 3}}
			for i := 1; i <= 4; i++ {
				wantRate = append(wantRate, api.ObsOverviewRateLimited{AccountID: fmt.Sprintf("00000000-0000-0000-0000-%012d", i), Hits: 2})
			}
			if !reflect.DeepEqual(got.TopRateLimitedAccounts24h, wantRate) {
				t.Fatalf("rate limits=%+v want=%+v", got.TopRateLimitedAccounts24h, wantRate)
			}
		})
	}
}

func seedOverviewNodes(t *testing.T, f overviewFixture, now time.Time) {
	t.Helper()
	node, err := f.store.ComputeNodeByName(f.ctx, DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		name   string
		at     time.Time
		active bool
	}{
		{DefaultLocalNodeName, now.Add(-api.ObsOverviewNodeStaleAfter), true}, {"stale", now.Add(-api.ObsOverviewNodeStaleAfter - time.Microsecond), false}, {"future", now.Add(time.Microsecond), true}, {"zero", time.Time{}, true},
	} {
		n := node
		n.ID = uuid.NewString()
		n.Name = row.name
		if row.name == DefaultLocalNodeName {
			n.ID = node.ID
		}
		n.Lifecycle = NodeLifecycleActive
		if !row.active {
			n.Lifecycle = NodeLifecycleUnavailable
		}
		n.LastHeartbeatAt = row.at
		saved, err := f.store.UpsertComputeNodeFromOperator(f.ctx, n)
		if err != nil {
			t.Fatal(err)
		}
		// Operator upsert does not own heartbeat writes in PostgreSQL.
		if f.mem != nil {
			saved.LastHeartbeatAt = row.at
			f.mem.computeNodes[saved.ID] = saved
		} else if _, err := f.pool.Exec(f.ctx, "UPDATE compute_nodes SET last_heartbeat_at=$2 WHERE id=$1", saved.ID, row.at); err != nil {
			t.Fatal(err)
		}
	}
}

func seedOverviewEvents(t *testing.T, f overviewFixture, now time.Time) {
	t.Helper()
	appendEvent := func(kind string, subject *string, at time.Time) {
		t.Helper()
		if err := f.store.(interface {
			AppendEventAt(context.Context, string, string, *string, []byte, time.Time) error
		}).AppendEventAt(f.ctx, "operator", kind, subject, nil, at); err != nil {
			t.Fatal(err)
		}
	}
	for i, kind := range []string{"a.failed", "b.failure", "c.error", "d.failed", "e.failed", "f.failed", "g.failed"} {
		for j := 0; j < []int{7, 6, 5, 4, 3, 3, 1}[i]; j++ {
			appendEvent(kind, nil, now.Add(-api.ObsOverviewFailureWindow))
		}
	}
	appendEvent("outside.failed", nil, now.Add(-api.ObsOverviewFailureWindow-time.Microsecond))
	appendEvent("future.failed", nil, now.Add(time.Microsecond))
	appendEvent("not.failure.suffix", nil, now)
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
		appendEvent("auth.rate_limited", &id, now.Add(-api.ObsOverviewAuditWindow))
		appendEvent("auth.rate_limited", &id, now)
	}
	for i := 0; i < 3; i++ {
		appendEvent("auth.rate_limited", nil, now)
	}
	appendEvent("auth.rate_limited", nil, now.Add(-api.ObsOverviewAuditWindow-time.Microsecond))
	appendEvent("auth.rate_limited", nil, now.Add(time.Microsecond))
	for _, at := range []time.Time{now.Add(-api.ObsOverviewAuditWindow), now, now.Add(-api.ObsOverviewAuditWindow - time.Microsecond), now.Add(time.Microsecond)} {
		if err := f.store.InsertAuditLog(f.ctx, AuditLog{ID: uuid.New(), Kind: "operator.overview", ReceivedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
}

// Pause after totals acquired a snapshot, then let another connection commit.
// No sleeps or production hooks are needed to force the interleaving.
type overviewSnapshotTracer struct {
	once        sync.Once
	afterTotals func(context.Context)
}
type overviewTotalsQueryKey struct{}

func (tr *overviewSnapshotTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, overviewTotalsQueryKey{}, strings.Contains(data.SQL, "-- name: OperatorOverviewTotals"))
}
func (tr *overviewSnapshotTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if totals, _ := ctx.Value(overviewTotalsQueryKey{}).(bool); totals {
		tr.once.Do(func() { tr.afterTotals(ctx) })
	}
}

func TestOperatorOverviewPostgresSnapshot(t *testing.T) {
	f := newOverviewFixture(t, "postgres")
	now := time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour)
	verified := now.Add(-time.Hour)
	account := f.account(t, "snapshot", verified, &verified, AccountActive)
	reached, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	tr := &overviewSnapshotTracer{afterTotals: func(ctx context.Context) {
		close(reached)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}}
	cfg := f.pool.Config()
	cfg.ConnConfig.Tracer = tr
	reader, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	type result struct {
		response api.ObsOverviewResponse
		err      error
	}
	done := make(chan result, 1)
	go func() { response, err := NewPgStore(reader).OperatorOverview(ctx, now); done <- result{response, err} }()
	select {
	case <-reached:
	case <-ctx.Done():
		t.Fatal("overview did not reach totals", ctx.Err())
	}
	// A committed app and live deployment must not leak into the existing read.
	f.workload(t, "snapshot-app", account, true)
	// Unblock the reader; the deferred close also releases it on failure.
	select {
	case release <- struct{}{}:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var first result
	select {
	case first = <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if first.err != nil || first.response.Totals.AppsTotal != 0 || first.response.BetaFunnel14d.WithApp != 0 || first.response.BetaFunnel14d.WithLiveDeployment != 0 {
		t.Fatalf("mixed snapshot: %+v err=%v", first.response, first.err)
	}
	second, err := f.store.OperatorOverview(ctx, now)
	if err != nil || second.Totals.AppsTotal != 1 || second.BetaFunnel14d.WithApp != 1 || second.BetaFunnel14d.WithLiveDeployment != 1 {
		t.Fatalf("next snapshot: %+v err=%v", second, err)
	}
}

func TestOperatorOverviewPostgresFailures(t *testing.T) {
	f := newOverviewFixture(t, "postgres")
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	cfg := f.pool.Config()
	cfg.ConnConfig.Tracer = &overviewSnapshotTracer{afterTotals: func(context.Context) { cancel() }}
	reader, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, err := NewPgStore(reader).OperatorOverview(ctx, time.Now().UTC())
	if err == nil || !reflect.DeepEqual(got, api.ObsOverviewResponse{}) {
		t.Fatalf("partial snapshot escaped after cancellation: %+v err=%v", got, err)
	}
	f.pool.Close()
	if _, err := f.store.OperatorOverview(f.ctx, time.Now().UTC()); err == nil {
		t.Fatal("closed database returned success")
	}
}
