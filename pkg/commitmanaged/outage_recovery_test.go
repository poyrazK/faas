package commitmanaged

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// A relay connection failure affects only its source. The producer databases
// remain available to this test, so committed rows can accumulate while the
// manager is unable to connect to the affected one.
func TestCommitRelaySourceOutageIsolationAndRecovery(t *testing.T) {
	// Receipt accounting triggers are public-qualified. Use a private migrated
	// database even when the broader suite defaults to isolated schemas.
	t.Setenv(pgtest.UseTemplateDatabase, "1")
	e := newChangesEnv(t, true)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	customer := pgtest.OpenDatabase(t)
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.account.ID, Slug: "outage-worker-" + uuid.NewString()[:8], RAMMB: 512, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	const queue = "outage-changes"
	if _, err := e.store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: e.account.ID, AppID: app.ID, Name: queue,
		QueueName: queue, Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	feeds := []commitwork.ChangeFeed{{Name: "orders", Table: "public.orders", Columns: []string{"id", "status"}}}
	raw, err := json.Marshal(feeds)
	if err != nil {
		t.Fatal(err)
	}
	affected, err := e.store.UpsertCommitChangesSource(ctx, e.account.ID, app.ID, "outage-"+app.Slug,
		state.CommitChangesConfig{QueueName: queue, Feeds: raw, PollIntervalMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	const affectedURL = "postgres://relay:fixture@outage.example.com/shop?sslmode=verify-full"
	sealed, err := commitwork.SealConnection(e.manager.Identities[0].Recipient(), affected.ID, affectedURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetCommitSourceConnection(ctx, e.account.ID, affected.ID, sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := customer.Exec(ctx, `CREATE TABLE public.orders (id int PRIMARY KEY, status text NOT NULL, secret text)`); err != nil {
		t.Fatal(err)
	}
	password, err := commitwork.NewRelayPassword()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := customer.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := commitwork.InstallChanges(ctx, tx, commitwork.ChangesInstall{SourceID: affected.ID, Feeds: feeds, RelayPassword: password})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = customer.Exec(context.Background(), `DROP OWNED BY `+installed.RelayRole)
		_, _ = customer.Exec(context.Background(), `DROP ROLE IF EXISTS `+installed.RelayRole)
	})

	var unavailable atomic.Bool
	missingSocket := filepath.Join(t.TempDir(), "unreachable")
	openHealthy := e.manager.Open
	e.manager.Open = func(openCtx context.Context, url string, policy commitwork.NetworkPolicy) (*pgxpool.Pool, error) {
		if url != affectedURL {
			return openHealthy(openCtx, url, policy)
		}
		cfg := customer.Config().Copy()
		if unavailable.Load() {
			// Use a nonexistent private Unix socket to exercise a real failed
			// connection without stopping the shared PostgreSQL test cluster.
			cfg.ConnConfig.Host = missingSocket
			cfg.ConnConfig.ConnectTimeout = 500 * time.Millisecond
			cfg.ConnConfig.Fallbacks = nil
		}
		pool, err := pgxpool.NewWithConfig(openCtx, cfg)
		if err != nil {
			return nil, err
		}
		if err := pool.Ping(openCtx); err != nil {
			pool.Close()
			return nil, err
		}
		return pool, nil
	}
	type report struct {
		source, status string
		accepted       int
	}
	reports := make(chan report, 128)
	e.manager.Report = func(source, status string, accepted int) {
		select {
		case reports <- report{source, status, accepted}:
		case <-ctx.Done():
		}
	}
	var pendingReports []report
	waitReport := func(source, status string, accepted int) {
		t.Helper()
		for {
			for i, got := range pendingReports {
				if got.source == source && got.status == status && got.accepted == accepted {
					pendingReports = append(pendingReports[:i], pendingReports[i+1:]...)
					return
				}
			}
			select {
			case got := <-reports:
				if got.source == "" && got.status == "source_scan_failed" {
					t.Fatalf("platform source scan failed: %+v", got)
				}
				if got.source == source && got.status == status && got.accepted == accepted {
					return
				}
				pendingReports = append(pendingReports, got)
			case <-ctx.Done():
				t.Fatalf("waiting for source status %s (accepted=%d): %v", status, accepted, ctx.Err())
			}
		}
	}
	for _, db := range []*pgxpool.Pool{e.customer, customer} {
		if _, err := db.Exec(ctx, `INSERT INTO public.orders VALUES (1,'initial',NULL)`); err != nil {
			t.Fatal(err)
		}
	}
	var initialEvent string
	if err := customer.QueryRow(ctx, `SELECT event_id::text FROM public.gregale_outbox`).Scan(&initialEvent); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- e.manager.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("manager shutdown: %v", err)
		}
	}()
	waitReport(e.source.ID, "healthy", 1)
	waitReport(affected.ID, "healthy", 1)
	original, err := e.store.CommitReceiptByEvent(ctx, e.account.ID, affected.ID, initialEvent)
	if err != nil || original.ID == "" || original.InvocationID == "" {
		t.Fatalf("initial receipt: %+v err=%v", original, err)
	}
	// Hold the next affected poll until the outage and producer writes are
	// ready. A previously claimed poll must finish before changing its due time.
	for {
		tag, err := e.control.Exec(ctx, `UPDATE commit_sources SET relay_next_poll_at=now()+interval '1 hour'
 WHERE id=$1::uuid AND relay_poll_token IS NULL`, affected.ID)
		if err != nil {
			t.Fatal(err)
		}
		if tag.RowsAffected() == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}

	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(NewObservationCollector(e.store))
	readMetrics := func() map[string]float64 {
		t.Helper()
		families, err := registry.Gather()
		if err != nil {
			t.Fatal(err)
		}
		values := make(map[string]float64, len(families))
		for _, family := range families {
			if len(family.Metric) != 1 || len(family.Metric[0].Label) != 0 {
				t.Fatal("unexpected observation metric cardinality")
			}
			values[family.GetName()] = family.Metric[0].GetGauge().GetValue()
		}
		return values
	}
	assertMetrics := func(unknown, failing float64) {
		t.Helper()
		got := readMetrics()
		if got["schedd_commit_relay_observation_snapshot_success"] != 1 || got["schedd_commit_relay_enabled_sources"] != 2 ||
			got["schedd_commit_relay_unknown_sources"] != unknown || got["schedd_commit_relay_failing_sources"] != failing ||
			got["schedd_commit_relay_pending_events"] != 0 {
			t.Fatalf("observation metrics: %v", got)
		}
	}
	assertMetrics(0, 0)

	unavailable.Store(true)
	if _, err := customer.Exec(ctx, `INSERT INTO public.orders VALUES (2,'during-outage',NULL),(3,'during-outage',NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.customer.Exec(ctx, `INSERT INTO public.orders VALUES (2,'healthy',NULL)`); err != nil {
		t.Fatal(err)
	}
	e.makeDue(t)
	if tag, err := e.control.Exec(ctx, `UPDATE commit_sources SET relay_next_poll_at='-infinity' WHERE id=$1::uuid AND relay_poll_token IS NULL`, affected.ID); err != nil || tag.RowsAffected() != 1 {
		if err == nil {
			t.Fatal("affected source was claimed before the outage was ready")
		}
		t.Fatal(err)
	}
	waitReport(affected.ID, "database_unavailable", 0)
	var firstBackoff int
	if err := e.control.QueryRow(ctx, `SELECT relay_poll_backoff_ms FROM commit_sources WHERE id=$1::uuid`, affected.ID).Scan(&firstBackoff); err != nil || firstBackoff != 5000 {
		t.Fatalf("first outage backoff=%d err=%v", firstBackoff, err)
	}
	waitReport(e.source.ID, "healthy", 1)
	assertMetrics(1, 1)
	unknown, err := e.store.CommitSourceByID(ctx, e.account.ID, affected.ID)
	if err != nil || unknown.RelayStatus != "database_unavailable" || unknown.PendingEvents != nil || unknown.BlockedEvents != nil {
		t.Fatalf("outage source observation: %+v err=%v", unknown, err)
	}
	var pending int
	if err := customer.QueryRow(ctx, `SELECT count(*) FROM public.gregale_outbox WHERE accepted_at IS NULL`).Scan(&pending); err != nil || pending != 2 {
		t.Fatalf("outage pending=%d err=%v", pending, err)
	}
	// Do not alter the failed source's schedule: its next failure and recovery
	// must use the production backoff rather than an accelerated test clock.
	waitReport(affected.ID, "database_unavailable", 0)
	var secondBackoff int
	if err := e.control.QueryRow(ctx, `SELECT relay_poll_backoff_ms FROM commit_sources WHERE id=$1::uuid`, affected.ID).Scan(&secondBackoff); err != nil || secondBackoff != 10000 {
		t.Fatalf("second outage backoff=%d err=%v", secondBackoff, err)
	}
	assertMetrics(1, 1)
	unavailable.Store(false)
	waitReport(affected.ID, "healthy", 2)
	var resetBackoff int
	if err := e.control.QueryRow(ctx, `SELECT relay_poll_backoff_ms FROM commit_sources WHERE id=$1::uuid`, affected.ID).Scan(&resetBackoff); err != nil || resetBackoff != 0 {
		t.Fatalf("recovered backoff=%d err=%v", resetBackoff, err)
	}
	assertMetrics(0, 0)

	rows, err := customer.Query(ctx, `SELECT event_id::text,event_type,payload,receipt_id::text,invocation_id::text
 FROM public.gregale_outbox ORDER BY created_at,event_id`)
	if err != nil {
		t.Fatal(err)
	}
	type checkpoint struct {
		event, kind, receipt, invocation string
		payload                          []byte
	}
	var checkpoints []checkpoint
	for rows.Next() {
		var item checkpoint
		if err := rows.Scan(&item.event, &item.kind, &item.payload, &item.receipt, &item.invocation); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		checkpoints = append(checkpoints, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(checkpoints) != 3 {
		t.Fatalf("recovered checkpoints=%d err=%v", len(checkpoints), err)
	}
	for _, item := range checkpoints {
		receipt, err := e.store.CommitReceiptByEvent(ctx, e.account.ID, affected.ID, item.event)
		if err != nil || receipt.ID != item.receipt || receipt.InvocationID != item.invocation {
			t.Fatalf("checkpoint receipt mismatch: %+v err=%v", receipt, err)
		}
		replayed, err := e.store.AcceptCommitChange(ctx, e.account.ID, affected.ID, item.event, item.kind, item.payload, state.Invocation{}, nil)
		if err != nil || replayed.ID != receipt.ID || replayed.InvocationID != receipt.InvocationID {
			t.Fatalf("replay changed receipt: %+v err=%v", replayed, err)
		}
		if item.event == initialEvent && (receipt.ID != original.ID || receipt.InvocationID != original.InvocationID) {
			t.Fatal("the pre-outage receipt changed during recovery")
		}
	}
	var receipts, invocations int
	if err := e.control.QueryRow(ctx, `SELECT count(*),count(DISTINCT invocation_id) FROM commit_receipts WHERE source_id=$1::uuid`, affected.ID).
		Scan(&receipts, &invocations); err != nil || receipts != 3 || invocations != 3 {
		t.Fatalf("affected receipts=%d invocations=%d err=%v", receipts, invocations, err)
	}
	if n := e.pending(t); n != 0 {
		t.Fatalf("healthy source retained %d pending changes", n)
	}
}
