package managedpostgres

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// Each start takes the first cause in ADR-20261125231222's order among the
// apps bound to the database at the start; a suspend closes the start before
// it, a replayed window records nothing twice, and prune drops old starts.
func TestComputeActivityPGAttributesStarts(t *testing.T) {
	s, pool, ctx, accountID := postgresStoreFixture(t)
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	database := postgresReadyDatabase(t, s, accountID, "act-"+uuid.NewString()[:8], base.Add(-time.Hour))
	ps := state.NewPgStore(pool)
	newApp := func() state.App {
		t.Helper()
		app, err := ps.CreateApp(ctx, state.App{AccountID: accountID, Slug: "act-" + uuid.NewString()[:8], MaxConcurrency: 1, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	bound, other := newApp(), newApp()
	if _, _, err := s.ReserveBinding(ctx, testBinding(accountID, database.ID, bound.ID, uuid.NewString(), base.Add(-10*time.Minute))); err != nil {
		t.Fatal(err)
	}
	// A binding made after every start explains none of them.
	if _, _, err := s.ReserveBinding(ctx, testBinding(accountID, database.ID, other.ID, uuid.NewString(), base.Add(2*time.Hour))); err != nil {
		t.Fatal(err)
	}
	event := func(kind, appID string, at time.Time, data string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO events(at,actor,kind,data) VALUES($1,'test',$2,jsonb_build_object('app_id',$3::text) || $4::jsonb)`,
			at, kind, appID, data); err != nil {
			t.Fatal(err)
		}
	}
	at := func(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }
	event("cron.fired", bound.ID, at(0).Add(-30*time.Second), `{"path":"/cron/sync"}`)
	event("wake.admitted", bound.ID, at(10).Add(-10*time.Second), `{"request_id":"req-1"}`)
	event("wake.admitted", bound.ID, at(20).Add(-10*time.Second), `{"request_id":""}`)
	// A wake-with-app start outranks a cron beside it.
	event("cron.fired", bound.ID, at(30).Add(-10*time.Second), `{"path":"/cron/other"}`)
	if err := s.SetWakeWithApp(ctx, database.ID, true); err != nil {
		t.Fatal(err)
	}
	// The claim behind op-4 is matched from its history row even after a
	// later claim moved last_started_at.
	if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_app_wake_starts(database_id,claimed_at) VALUES($1,$2)`, database.ID, at(30).Add(-5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_wake_with_app SET last_started_at=$2 WHERE database_id=$1`, database.ID, at(65)); err != nil {
		t.Fatal(err)
	}
	role := "compute-node"
	node, err := ps.CreateComputeNode(ctx, state.ComputeNode{Name: "act-" + uuid.NewString(), TargetURL: "tcp://127.0.0.1:50051", VPCPUs: 4, MemMB: 4096, MaxConcurrency: 20, AdmissionCeilingMB: 4096, VCPUBudget: 4, Active: true, Role: &role})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO instances(app_id,state,ram_mb,node_id,started_at,terminal_at) VALUES($1,'stopped',256,$2,$3,$4)`,
		bound.ID, node.ID, at(35), at(45)); err != nil {
		t.Fatal(err)
	}
	// The unbound app's cron explains nothing.
	event("cron.fired", other.ID, at(60).Add(-10*time.Second), `{"path":"/cron/x"}`)

	events := []ComputeEvent{
		{OperationID: "op-s1", Kind: ComputeEventSuspend, At: at(5)},
		{OperationID: "op-1", Kind: ComputeEventStart, At: at(0)},
		{OperationID: "op-2", Kind: ComputeEventStart, At: at(10)},
		{OperationID: "op-3", Kind: ComputeEventStart, At: at(20)},
		{OperationID: "op-4", Kind: ComputeEventStart, At: at(30)},
		{OperationID: "op-5", Kind: ComputeEventStart, At: at(40)},
		{OperationID: "op-6", Kind: ComputeEventStart, At: at(60)},
	}
	if n, err := s.RecordComputeEvents(ctx, database.ID, events); err != nil || n != 6 {
		t.Fatalf("recorded %d, %v; want 6", n, err)
	}
	if n, err := s.RecordComputeEvents(ctx, database.ID, events); err != nil || n != 0 {
		t.Fatalf("replay recorded %d, %v; want 0", n, err)
	}
	starts, err := s.ComputeStarts(ctx, database.ID, at(-1), 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ id, cause, app, detail string }{
		{"op-6", CauseExternal, "", ""},
		{"op-5", CauseRunningApp, bound.ID, ""},
		{"op-4", CauseAppWake, "", ""},
		{"op-3", CauseAppStart, bound.ID, ""},
		{"op-2", CauseRequest, bound.ID, ""},
		{"op-1", CauseCron, bound.ID, "/cron/sync"},
	}
	if len(starts) != len(want) {
		t.Fatalf("starts = %+v", starts)
	}
	for i, w := range want {
		got := starts[i]
		if got.OperationID != w.id || got.Cause != w.cause || got.AppID != w.app || got.Detail != w.detail {
			t.Fatalf("start %d = %+v, want %+v", i, got, w)
		}
		if w.app != "" && got.AppSlug != bound.Slug {
			t.Fatalf("start %d slug = %q, want %q", i, got.AppSlug, bound.Slug)
		}
		closed := w.id == "op-1"
		if closed != !got.SuspendedAt.IsZero() || (closed && !got.SuspendedAt.Equal(at(5))) {
			t.Fatalf("start %d suspended at %v", i, got.SuspendedAt)
		}
	}

	// The cron start's 300 s count fully; open ones count to now.
	now := at(70)
	causes, err := s.ComputeStartCauses(ctx, database.ID, at(-1), now)
	if err != nil {
		t.Fatal(err)
	}
	var cron *ComputeStartCause
	for i := range causes {
		if causes[i].Cause == CauseCron {
			cron = &causes[i]
		}
	}
	if len(causes) != 6 || cron == nil || cron.Starts != 1 || cron.AwakeSeconds != 300 || cron.Detail != "/cron/sync" || !cron.FirstStartedAt.Equal(at(0)) {
		t.Fatalf("causes = %+v", causes)
	}
	totals, err := s.AccountActivityTotals(ctx, accountID, at(-1), now)
	if err != nil {
		t.Fatal(err)
	}
	// The closed start's 300 s; a start whose suspend was never read ends at
	// the next start (10, 10, 10 and 20 minutes); the last runs to now (10).
	if got := totals[database.ID]; got.Starts != 6 || got.AwakeSeconds != 300+(10+10+10+20+10)*60 {
		t.Fatalf("totals = %+v", totals)
	}
	// A start that began before the period counts its part inside it.
	since := at(15)
	totals, err = s.AccountActivityTotals(ctx, accountID, since, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := totals[database.ID]; got.Starts != 5 || got.AwakeSeconds != (5+10+10+20+10)*60 {
		t.Fatalf("totals since %s = %+v", since, totals)
	}
	if starts, err := s.ComputeStarts(ctx, database.ID, since, 100); err != nil || len(starts) != 5 || starts[4].OperationID != "op-2" {
		t.Fatalf("starts since %s = %+v, %v", since, starts, err)
	}

	if n, err := s.PruneComputeStarts(ctx, at(15), 1); err != nil || n != 1 {
		t.Fatalf("prune batch = %d, %v", n, err)
	}
	if n, err := s.PruneComputeStarts(ctx, at(15), 10); err != nil || n != 1 {
		t.Fatalf("prune rest = %d, %v", n, err)
	}
	if starts, err := s.ComputeStarts(ctx, database.ID, at(-1), 100); err != nil || len(starts) != 4 {
		t.Fatalf("after prune %d, %v", len(starts), err)
	}
	// Wake-with-app claims are pruned with the starts.
	if n, err := s.PruneComputeStarts(ctx, at(31), 10); err != nil || n != 2 {
		t.Fatalf("prune to 31 = %d, %v", n, err)
	}
	var claims int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM managed_postgres_app_wake_starts WHERE database_id=$1`, database.ID).Scan(&claims); err != nil || claims != 0 {
		t.Fatalf("claims left %d, %v", claims, err)
	}
}
