package commitload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/commitmanaged"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// This opt-in qualification exercises real relays and the platform store. It
// creates two private databases on the configured test cluster; it does not
// start schedd, run an HTTP consumer, or qualify a production deployment.
func TestCommitSustainedLoadAndRecovery(t *testing.T) {
	if os.Getenv("GREGALE_COMMIT_LOAD_TEST") != "1" {
		t.Skip("run through make test-commit-load")
	}
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("a disposable PostgreSQL test cluster is required")
	}
	duration := 75 * time.Second
	if raw := os.Getenv("GREGALE_COMMIT_LOAD_DURATION"); raw != "" {
		var err error
		duration, err = time.ParseDuration(raw)
		if err != nil || duration < 65*time.Second || duration > 10*time.Minute {
			t.Fatal("GREGALE_COMMIT_LOAD_DURATION must be between 65s and 10m")
		}
	}
	rate := 4
	if raw := os.Getenv("GREGALE_COMMIT_LOAD_RATE"); raw != "" {
		var err error
		rate, err = strconv.Atoi(raw)
		if err != nil || rate < 1 || rate > 50 {
			t.Fatal("GREGALE_COMMIT_LOAD_RATE must be between 1 and 50 events per second")
		}
	}
	// This test retains the accepted Operations; it does not run a consumer.
	// Reject combinations that could exhaust the real per-account pending quota.
	maxEvents := int((duration+6*time.Second)/time.Second) * rate
	if maxEvents >= api.MaxExclusivePendingPerAccount {
		t.Fatalf("configured duration and rate could exceed the pending Operation limit of %d", api.MaxExclusivePendingPerAccount)
	}

	platformBase := pgtest.OpenDatabase(t)
	if err := db.MigrateUp(t.Context(), platformBase); err != nil {
		t.Fatal(err)
	}
	customer := pgtest.OpenDatabase(t)
	cfg := platformBase.Config().Copy()
	cfg.MaxConns = 20
	platform, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(platform.Close)
	ctx, cancel := context.WithTimeout(t.Context(), duration+3*time.Minute)
	defer cancel()
	store := state.NewPgStore(platform)
	account, err := store.CreateAccount(ctx, "commit-load-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "commit-load-" + uuid.NewString()[:8], Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertExclusiveWorkPolicy(ctx, account.ID, exclusivework.Policy{
		Name: "qualification", Scope: "account", Contention: "queue", MemberAppIDs: []string{app.ID}, LeaseSeconds: 15, MaxAttemptSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateCommitSource(ctx, state.CommitSource{AccountID: account.ID, AppID: app.ID, Name: "qualification", OperationPolicy: "qualification"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := customer.Exec(ctx, commitwork.Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := customer.Exec(ctx, `INSERT INTO public.gregale_commit_binding(singleton,source_id) VALUES(true,$1::uuid)`, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := customer.Exec(ctx, `CREATE TABLE public.qualification_business(event_id uuid PRIMARY KEY,phase text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	// The platform database is new and empty. Establish an exact empty ledger
	// so the first phase measures relay activity without a concurrent backfill.
	if _, err := platform.Exec(ctx, `UPDATE commit_receipt_usage SET initialized=true,reconciled_at=now()`); err != nil {
		t.Fatal(err)
	}
	accept := &loadAcceptor{store: store, source: source, appID: app.ID}
	monitor := startLoadMonitor(ctx, platform)
	defer monitor.stop()

	baselineBefore, err := loadDatabaseStats(ctx, platform)
	if err != nil {
		t.Fatal(err)
	}
	baselineStart := time.Now()
	baselineRelays := startLoadRelays(ctx, customer, source.ID, accept, 4)
	baselineCount, err := produceLoad(ctx, customer, "baseline", rate, 5*time.Second)
	if err != nil {
		baselineRelays.stop()
		t.Fatal(err)
	}
	if err := waitLoadDrain(ctx, customer, "baseline", baselineCount); err != nil {
		baselineRelays.stop()
		t.Fatal(err)
	}
	baselineDuration := time.Since(baselineStart)
	baselineRelays.stop()
	baselineAfter, err := loadDatabaseStats(ctx, platform)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := readLoadPhase(ctx, customer, "baseline", baselineCount, baselineDuration, baselineBefore, baselineAfter)
	if err != nil {
		t.Fatal(err)
	}

	// Seed four UUID ranges with enough receipts for more than one batch. The
	// other 252 shards retain exact live counters. Seed rows are not outbox work.
	if _, err := platform.Exec(ctx, `INSERT INTO commit_receipts(id,account_id,source_id,event_id,event_type,payload,invocation_id)
 SELECT (lpad(to_hex(shard),2,'0')||lpad(to_hex(n),30,'0'))::uuid,$1::uuid,$2::uuid,gen_random_uuid(),
 'qualification.seed',jsonb_build_object('n',n),gen_random_uuid()
 FROM generate_series(0,3) shard CROSS JOIN generate_series(1,1001) n`, account.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := platform.Exec(ctx, `UPDATE commit_receipt_usage SET initialized=false,reconciled_at=NULL,
 retry_at=CASE WHEN shard=0 THEN '-infinity'::timestamptz ELSE now()+interval '2 minutes' END WHERE shard<4`); err != nil {
		t.Fatal(err)
	}
	backfillStarted := time.Now()
	if worked, err := store.ReconcileCommitReceiptStorage(ctx); err != nil || !worked {
		t.Fatalf("start multi-batch backfill: worked=%t err=%v", worked, err)
	}
	var cursorPresent bool
	if err := platform.QueryRow(ctx, `SELECT scan_in_progress AND scan_cursor IS NOT NULL FROM commit_receipt_usage WHERE shard=0`).Scan(&cursorPresent); err != nil || !cursorPresent {
		t.Fatalf("backfill cursor missing: present=%t err=%v", cursorPresent, err)
	}
	faultBefore, err := loadDatabaseStats(ctx, platform)
	if err != nil {
		t.Fatal(err)
	}
	faultStart := time.Now()
	produced := make(chan loadProductionResult, 1)
	go func() {
		count, err := produceLoad(ctx, customer, "fault", rate, duration)
		produced <- loadProductionResult{count: count, err: err}
	}()

	// Stop one relay after acceptance commits and before its outbox checkpoint.
	// Its one-second lease expires naturally; a competing relay must recover the
	// original receipt and operation rather than create another owner.
	crash := &loadCrashAcceptor{loadAcceptor: accept, accepted: make(chan loadAccepted, 1)}
	crashCtx, crashCancel := context.WithCancel(ctx)
	crashDone := make(chan error, 1)
	go func() {
		r := commitwork.Relay{Pool: customer, SourceID: source.ID, Acceptor: crash, Batch: 1, Lease: time.Second, RetryDelay: time.Second}
		for crashCtx.Err() == nil {
			n, err := r.Tick(crashCtx)
			if err != nil {
				crashDone <- err
				return
			}
			if n == 0 && !loadWait(crashCtx, 25*time.Millisecond) {
				break
			}
		}
		crashDone <- crashCtx.Err()
	}()
	var interrupted loadAccepted
	select {
	case interrupted = <-crash.accepted:
	case err := <-crashDone:
		crashCancel()
		t.Fatalf("relay stopped before durable acceptance: %v", err)
	case <-time.After(20 * time.Second):
		crashCancel()
		t.Fatal("relay did not reach durable acceptance")
	case <-ctx.Done():
		crashCancel()
		t.Fatal(ctx.Err())
	}
	crashCancel()
	if err := <-crashDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("relay cancellation: %v", err)
	}
	relays := startLoadRelays(ctx, customer, source.ID, accept, 4)
	defer relays.stop()

	// Claim the saved shard from a dedicated connection and terminate exactly
	// that backend while its scan waits behind a test-owned table lock.
	blockedFor, err := interruptLoadReconciler(ctx, platform, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := platform.Exec(ctx, `UPDATE commit_receipt_usage SET retry_at='-infinity' WHERE shard BETWEEN 1 AND 3`); err != nil {
		t.Fatal(err)
	}
	var preserved, deferred bool
	if err := platform.QueryRow(ctx, `SELECT scan_in_progress AND scan_cursor IS NOT NULL,retry_at>clock_timestamp()
 FROM commit_receipt_usage WHERE shard=0`).Scan(&preserved, &deferred); err != nil || !preserved || !deferred {
		t.Fatalf("interrupted progress: preserved=%t deferred=%t err=%v", preserved, deferred, err)
	}

	// Exercise the actual collector against a closed pool, then restore it.
	// This is a connection failure in the isolated platform test database, not
	// a network or whole-cluster outage.
	closedPool, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	closedPool.Close()
	switcher := &loadSwitchStore{store: state.NewPgStore(closedPool)}
	collector := commitmanaged.NewReceiptStorageCollector(switcher)
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(collector)
	collectorCtx, collectorCancel := context.WithCancel(ctx)
	collectorDone := make(chan struct{})
	go func() { defer close(collectorDone); collector.Run(collectorCtx) }()
	defer func() { collectorCancel(); <-collectorDone }()
	failureCtx, cancelFailure := context.WithTimeout(ctx, 15*time.Second)
	err = waitLoadMetric(failureCtx, registry, func(success, current, failures float64) bool {
		return success == 0 && current == 0 && failures >= 3
	})
	cancelFailure()
	if err != nil {
		t.Fatalf("collector did not report closed connection: %v", err)
	}
	switcher.set(store)
	reconcilers := startLoadReconcilers(ctx, store, 3)
	defer reconcilers.stop()
	type recoveryResult struct {
		elapsed time.Duration
		err     error
	}
	recovered := make(chan recoveryResult, 1)
	go func() {
		err := waitLoadMetric(ctx, registry, func(success, current, failures float64) bool {
			return success == 1 && current == 1 && failures >= 1
		})
		recovered <- recoveryResult{elapsed: time.Since(backfillStarted), err: err}
	}()

	result := <-produced
	if result.err != nil {
		t.Fatal(result.err)
	}
	if err := waitLoadDrain(ctx, customer, "fault", result.count); err != nil {
		t.Fatal(err)
	}
	faultDuration := time.Since(faultStart)
	recovery := <-recovered
	if recovery.err != nil {
		t.Fatalf("collector did not recover: %v", recovery.err)
	}
	catchup := recovery.elapsed
	faultAfter, err := loadDatabaseStats(ctx, platform)
	if err != nil {
		t.Fatal(err)
	}
	fault, err := readLoadPhase(ctx, customer, "fault", result.count, faultDuration, faultBefore, faultAfter)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyLoadIdentities(ctx, platform, customer, source, baselineCount+result.count, interrupted); err != nil {
		t.Fatal(err)
	}
	usage, err := store.CommitReceiptStorage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var actualCount, actualBytes int64
	if err := platform.QueryRow(ctx, `SELECT count(*),coalesce(sum(octet_length(payload::text)),0) FROM commit_receipts`).Scan(&actualCount, &actualBytes); err != nil {
		t.Fatal(err)
	}
	if usage.InitializedShards != 256 || usage.TotalShards != 256 || usage.Count != actualCount || usage.PayloadJSONBytes != actualBytes {
		t.Fatalf("ledger mismatch: usage=%+v actual_count=%d actual_bytes=%d", usage, actualCount, actualBytes)
	}
	if relays.errors.Load() != 0 || baselineRelays.errors.Load() != 0 || reconcilers.errors.Load() != 0 {
		t.Fatalf("unexpected worker errors: baseline_relays=%d relays=%d reconcilers=%d", baselineRelays.errors.Load(), relays.errors.Load(), reconcilers.errors.Load())
	}
	report := loadReport{ConfiguredDurationSeconds: duration.Seconds(), ConfiguredRate: rate, Baseline: baseline, Fault: fault,
		BackfillCatchupSeconds: catchup.Seconds(), InterruptedScanLockSeconds: blockedFor.Seconds(),
		PeakActivePlatformSessions: monitor.active.Load(), PeakPlatformLockWaits: monitor.lockWaits.Load(),
		ReceiptCount: actualCount, ReceiptPayloadJSONBytes: actualBytes, ReconciliationFailuresObserved: 3}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("COMMIT_LOAD_RESULT %s", raw)
}

type loadAcceptor struct {
	store  *state.PgStore
	source state.CommitSource
	appID  string
}

func (a *loadAcceptor) Accept(ctx context.Context, event commitwork.Event) (commitwork.Receipt, error) {
	inv := state.Invocation{AccountID: a.source.AccountID, AppID: a.appID, Method: "POST", Path: "/", Payload: event.Data}
	r, err := a.store.AcceptCommitEvent(ctx, a.source.AccountID, a.source.ID, event.ID, event.Type, event.Data, inv, 1)
	return commitwork.Receipt{ID: r.ID, InvocationID: r.InvocationID, OperationID: r.OperationID}, err
}

type loadAccepted struct{ eventID, receiptID, operationID string }
type loadCrashAcceptor struct {
	*loadAcceptor
	accepted chan loadAccepted
}

func (a *loadCrashAcceptor) Accept(ctx context.Context, event commitwork.Event) (commitwork.Receipt, error) {
	r, err := a.loadAcceptor.Accept(ctx, event)
	if err != nil {
		return r, err
	}
	select {
	case a.accepted <- loadAccepted{event.ID, r.ID, r.OperationID}:
	case <-ctx.Done():
		return commitwork.Receipt{}, ctx.Err()
	}
	<-ctx.Done()
	return commitwork.Receipt{}, ctx.Err()
}

type loadProductionResult struct {
	count int
	err   error
}

func produceLoad(ctx context.Context, pool *pgxpool.Pool, phase string, rate int, duration time.Duration) (int, error) {
	ticker := time.NewTicker(time.Second / time.Duration(rate))
	defer ticker.Stop()
	end := time.NewTimer(duration)
	defer end.Stop()
	count := 0
	for {
		select {
		case <-ctx.Done():
			return count, ctx.Err()
		case <-end.C:
			return count, nil
		case <-ticker.C:
			event := commitwork.Event{ID: uuid.NewString(), Type: "qualification.load", Data: []byte(fmt.Sprintf(`{"phase":%q,"sequence":%d}`, phase, count))}
			tx, err := pool.Begin(ctx)
			if err != nil {
				return count, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO public.qualification_business(event_id,phase) VALUES($1::uuid,$2)`, event.ID, phase); err == nil {
				err = commitwork.Insert(ctx, tx, event)
			}
			if err == nil {
				err = tx.Commit(ctx)
			} else {
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				_ = tx.Rollback(cleanup)
				cancel()
			}
			if err != nil {
				return count, err
			}
			count++
		}
	}
}

type loadWorkers struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
	errors atomic.Int64
	once   sync.Once
}

func (w *loadWorkers) stop() { w.once.Do(func() { w.cancel(); w.wg.Wait() }) }

func startLoadRelays(parent context.Context, pool *pgxpool.Pool, sourceID string, accept commitwork.Acceptor, count int) *loadWorkers {
	ctx, cancel := context.WithCancel(parent)
	w := &loadWorkers{cancel: cancel}
	for range count {
		w.wg.Go(func() {
			r := commitwork.Relay{Pool: pool, SourceID: sourceID, Acceptor: accept, Batch: 1, Lease: 2 * time.Second, RetryDelay: time.Second}
			for ctx.Err() == nil {
				n, err := r.Tick(ctx)
				if err != nil && ctx.Err() == nil {
					w.errors.Add(1)
				}
				if (err != nil || n == 0) && !loadWait(ctx, 25*time.Millisecond) {
					return
				}
			}
		})
	}
	return w
}

func startLoadReconcilers(parent context.Context, store *state.PgStore, count int) *loadWorkers {
	ctx, cancel := context.WithCancel(parent)
	w := &loadWorkers{cancel: cancel}
	for range count {
		w.wg.Go(func() {
			for ctx.Err() == nil {
				batchCtx, batchCancel := context.WithTimeout(ctx, 5*time.Second)
				worked, err := store.ReconcileCommitReceiptStorage(batchCtx)
				batchCancel()
				if err != nil && ctx.Err() == nil {
					w.errors.Add(1)
				}
				if (!worked || err != nil) && !loadWait(ctx, 100*time.Millisecond) {
					return
				}
			}
		})
	}
	return w
}

func loadWait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func interruptLoadReconciler(ctx context.Context, platform *pgxpool.Pool, cfg *pgxpool.Config) (time.Duration, error) {
	config := cfg.Copy()
	config.MaxConns, config.MinConns = 1, 0
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return 0, err
	}
	defer pool.Close()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	pid := int32(conn.Conn().PgConn().PID())
	conn.Release()
	tx, err := platform.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func(parent context.Context) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}(ctx)
	lockCtx, cancelLock := context.WithTimeout(ctx, 10*time.Second)
	_, err = tx.Exec(lockCtx, `LOCK TABLE commit_receipts IN ACCESS EXCLUSIVE MODE`)
	cancelLock()
	if err != nil {
		return 0, err
	}
	started := time.Now()
	scanCtx, cancelScan := context.WithCancel(ctx)
	defer cancelScan()
	done := make(chan error, 1)
	go func() {
		_, err := state.NewPgStore(pool).ReconcileCommitReceiptStorage(scanCtx)
		done <- err
	}()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		var blocked bool
		if err := platform.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
 WHERE pid=$1 AND wait_event_type='Lock' AND query LIKE 'SELECT count(*)::bigint,%')`, pid).Scan(&blocked); err != nil {
			return 0, err
		}
		if blocked {
			break
		}
		select {
		case err := <-done:
			if err != nil {
				return 0, fmt.Errorf("reconciliation exited before connection interruption: %w", err)
			}
			return 0, errors.New("reconciliation exited before connection interruption")
		case <-deadline.C:
			return 0, errors.New("reconciliation did not reach the controlled lock")
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	var terminated bool
	if err := platform.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&terminated); err != nil {
		return 0, fmt.Errorf("terminate the test-owned backend: %w", err)
	}
	if !terminated {
		return 0, errors.New("test-owned backend was not terminated")
	}
	select {
	case err := <-done:
		if err == nil {
			return 0, errors.New("interrupted reconciliation unexpectedly succeeded")
		}
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	if err := tx.Rollback(ctx); err != nil {
		return 0, err
	}
	return time.Since(started), nil
}

type loadSwitchStore struct {
	mu    sync.RWMutex
	store *state.PgStore
}

func (s *loadSwitchStore) set(store *state.PgStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store = store
}
func (s *loadSwitchStore) current() *state.PgStore {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store
}
func (s *loadSwitchStore) CommitReceiptStorage(ctx context.Context) (state.CommitReceiptUsage, error) {
	return s.current().CommitReceiptStorage(ctx)
}
func (s *loadSwitchStore) ReconcileCommitReceiptStorage(ctx context.Context) (bool, error) {
	return s.current().ReconcileCommitReceiptStorage(ctx)
}

func waitLoadMetric(ctx context.Context, registry *prometheus.Registry, accept func(float64, float64, float64) bool) error {
	for {
		families, err := registry.Gather()
		if err != nil {
			return err
		}
		var success, current, failures float64
		for _, family := range families {
			if len(family.Metric) != 1 {
				return fmt.Errorf("unexpected metric cardinality for %s", family.GetName())
			}
			switch family.GetName() {
			case "schedd_commit_receipt_storage_sample_success":
				success = family.Metric[0].GetGauge().GetValue()
			case "schedd_commit_receipt_usage_reconciliation_current":
				current = family.Metric[0].GetGauge().GetValue()
			case "schedd_commit_receipt_reconciliation_failures_total":
				failures = family.Metric[0].GetCounter().GetValue()
			}
		}
		if accept(success, current, failures) {
			return nil
		}
		if !loadWait(ctx, 100*time.Millisecond) {
			return ctx.Err()
		}
	}
}

func waitLoadDrain(ctx context.Context, customer *pgxpool.Pool, phase string, expected int) error {
	if expected <= 0 {
		return errors.New("load producer generated no events")
	}
	for {
		var total, accepted, blocked int
		if err := customer.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE accepted_at IS NOT NULL),
 count(*) FILTER (WHERE blocked_code IS NOT NULL) FROM public.gregale_outbox WHERE payload->>'phase'=$1`, phase).
			Scan(&total, &accepted, &blocked); err != nil {
			return err
		}
		if total != expected || blocked != 0 {
			return fmt.Errorf("%s outbox totals: produced=%d total=%d blocked=%d", phase, expected, total, blocked)
		}
		if accepted == expected {
			return nil
		}
		if !loadWait(ctx, 100*time.Millisecond) {
			return fmt.Errorf("%s outbox did not drain (%d/%d): %w", phase, accepted, expected, ctx.Err())
		}
	}
}

type loadDBStats struct {
	Commits    int64 `json:"commits"`
	Rollbacks  int64 `json:"rollbacks"`
	BlocksHit  int64 `json:"blocks_hit"`
	BlocksRead int64 `json:"blocks_read"`
	Deadlocks  int64 `json:"deadlocks"`
}

func loadDatabaseStats(ctx context.Context, pool *pgxpool.Pool) (loadDBStats, error) {
	var s loadDBStats
	err := pool.QueryRow(ctx, `SELECT xact_commit,xact_rollback,blks_hit,blks_read,deadlocks
 FROM pg_stat_database WHERE datname=current_database()`).Scan(&s.Commits, &s.Rollbacks, &s.BlocksHit, &s.BlocksRead, &s.Deadlocks)
	return s, err
}

func (s loadDBStats) since(before loadDBStats) loadDBStats {
	return loadDBStats{Commits: s.Commits - before.Commits, Rollbacks: s.Rollbacks - before.Rollbacks,
		BlocksHit: s.BlocksHit - before.BlocksHit, BlocksRead: s.BlocksRead - before.BlocksRead, Deadlocks: s.Deadlocks - before.Deadlocks}
}

type loadPhase struct {
	Events           int         `json:"events"`
	ElapsedSeconds   float64     `json:"elapsed_seconds"`
	EventsPerSecond  float64     `json:"events_per_second"`
	CheckpointP50MS  float64     `json:"checkpoint_p50_ms"`
	CheckpointP95MS  float64     `json:"checkpoint_p95_ms"`
	CheckpointP99MS  float64     `json:"checkpoint_p99_ms"`
	MaximumAttempts  int         `json:"maximum_attempts"`
	PlatformDatabase loadDBStats `json:"platform_database_delta"`
}

func readLoadPhase(ctx context.Context, customer *pgxpool.Pool, phase string, count int, elapsed time.Duration, before, after loadDBStats) (loadPhase, error) {
	result := loadPhase{Events: count, ElapsedSeconds: elapsed.Seconds(), EventsPerSecond: float64(count) / elapsed.Seconds(), PlatformDatabase: after.since(before)}
	err := customer.QueryRow(ctx, `SELECT
 coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM (accepted_at-created_at))*1000),0)::float8,
 coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM (accepted_at-created_at))*1000),0)::float8,
 coalesce(percentile_cont(0.99) WITHIN GROUP (ORDER BY extract(epoch FROM (accepted_at-created_at))*1000),0)::float8,
 coalesce(max(attempts),0) FROM public.gregale_outbox WHERE payload->>'phase'=$1 AND accepted_at IS NOT NULL`, phase).
		Scan(&result.CheckpointP50MS, &result.CheckpointP95MS, &result.CheckpointP99MS, &result.MaximumAttempts)
	return result, err
}

type loadReport struct {
	ConfiguredDurationSeconds      float64   `json:"configured_duration_seconds"`
	ConfiguredRate                 int       `json:"configured_events_per_second"`
	Baseline                       loadPhase `json:"baseline"`
	Fault                          loadPhase `json:"fault"`
	BackfillCatchupSeconds         float64   `json:"backfill_catchup_seconds"`
	InterruptedScanLockSeconds     float64   `json:"interrupted_scan_lock_seconds"`
	PeakActivePlatformSessions     int64     `json:"peak_active_platform_sessions"`
	PeakPlatformLockWaits          int64     `json:"peak_platform_lock_waits"`
	ReceiptCount                   int64     `json:"receipt_count"`
	ReceiptPayloadJSONBytes        int64     `json:"receipt_payload_json_bytes"`
	ReconciliationFailuresObserved int       `json:"reconciliation_failures_observed_minimum"`
}

type loadMonitor struct {
	cancel    context.CancelFunc
	done      chan struct{}
	active    atomic.Int64
	lockWaits atomic.Int64
}

func startLoadMonitor(parent context.Context, pool *pgxpool.Pool) *loadMonitor {
	ctx, cancel := context.WithCancel(parent)
	m := &loadMonitor{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(m.done)
		for ctx.Err() == nil {
			var active, locked int64
			if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state='active'),
 count(*) FILTER (WHERE wait_event_type='Lock') FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid()`).Scan(&active, &locked); err == nil {
				loadMax(&m.active, active)
				loadMax(&m.lockWaits, locked)
			}
			if !loadWait(ctx, 200*time.Millisecond) {
				return
			}
		}
	}()
	return m
}
func (m *loadMonitor) stop() { m.cancel(); <-m.done }
func loadMax(target *atomic.Int64, value int64) {
	for previous := target.Load(); value > previous; previous = target.Load() {
		if target.CompareAndSwap(previous, value) {
			return
		}
	}
}

func verifyLoadIdentities(ctx context.Context, platform, customer *pgxpool.Pool, source state.CommitSource, expected int, interrupted loadAccepted) error {
	type identity struct{ receipt, operation string }
	accepted := make(map[string]identity, expected)
	operations := make(map[string]struct{}, expected)
	rows, err := platform.Query(ctx, `SELECT r.event_id::text,r.id::text,r.operation_id::text,(o.id IS NOT NULL)
 FROM commit_receipts r LEFT JOIN exclusive_work_operations o ON o.id=r.operation_id
 WHERE r.account_id=$1::uuid AND r.source_id=$2::uuid AND r.event_type='qualification.load'`, source.AccountID, source.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var event, receipt, operation string
		var exists bool
		if err := rows.Scan(&event, &receipt, &operation, &exists); err != nil {
			rows.Close()
			return err
		}
		_, operationSeen := operations[operation]
		if !exists || operationSeen || accepted[event] != (identity{}) {
			rows.Close()
			return errors.New("duplicate event or operation, or missing managed operation")
		}
		accepted[event] = identity{receipt, operation}
		operations[operation] = struct{}{}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("read platform acceptances: %w", err)
	}
	if len(accepted) != expected {
		return fmt.Errorf("platform acceptance count=%d expected=%d", len(accepted), expected)
	}
	customerRows, err := customer.Query(ctx, `SELECT event_id::text,receipt_id::text,operation_id::text
 FROM public.gregale_outbox WHERE event_type='qualification.load' AND accepted_at IS NOT NULL`)
	if err != nil {
		return err
	}
	matched := 0
	for customerRows.Next() {
		var event, receipt, operation string
		if err := customerRows.Scan(&event, &receipt, &operation); err != nil {
			customerRows.Close()
			return err
		}
		if accepted[event] != (identity{receipt, operation}) {
			customerRows.Close()
			return errors.New("customer checkpoint differs from platform receipt")
		}
		matched++
	}
	err = customerRows.Err()
	customerRows.Close()
	if err != nil {
		return fmt.Errorf("read customer checkpoints: %w", err)
	}
	if matched != expected {
		return fmt.Errorf("customer checkpoint count=%d expected=%d", matched, expected)
	}
	if accepted[interrupted.eventID] != (identity{interrupted.receiptID, interrupted.operationID}) {
		return errors.New("relay interruption did not preserve the original receipt and operation")
	}
	var operationCount int
	if err := platform.QueryRow(ctx, `SELECT count(*) FROM exclusive_work_operations WHERE account_id=$1::uuid`, source.AccountID).
		Scan(&operationCount); err != nil {
		return fmt.Errorf("count managed operations: %w", err)
	}
	if operationCount != expected {
		return fmt.Errorf("managed operation count=%d expected=%d", operationCount, expected)
	}
	var business int
	if err := customer.QueryRow(ctx, `SELECT count(*) FROM public.qualification_business`).Scan(&business); err != nil {
		return fmt.Errorf("count business transactions: %w", err)
	}
	if business != expected {
		return fmt.Errorf("business transaction count=%d expected=%d", business, expected)
	}
	return nil
}
