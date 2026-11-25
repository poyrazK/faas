package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCommitReceiptStorageSample(t *testing.T) {
	store, pool, source, _ := commitManagedFixture(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `INSERT INTO commit_receipts(id,account_id,source_id,event_id,event_type,payload,invocation_id)
 VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,'order.created','{"total":1.0}'::jsonb,$5::uuid)`,
		uuid.NewString(), source.AccountID, source.ID, uuid.NewString(), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var expectedBytes int64
	if err := pool.QueryRow(ctx, `SELECT octet_length(payload::text)::bigint FROM commit_receipts WHERE source_id=$1::uuid`, source.ID).Scan(&expectedBytes); err != nil {
		t.Fatal(err)
	}
	for range 256 {
		worked, err := store.ReconcileCommitReceiptStorage(ctx)
		if err != nil || !worked {
			t.Fatalf("receipt backfill: worked=%t err=%v", worked, err)
		}
	}
	usage, err := store.CommitReceiptStorage(ctx)
	if err != nil || usage.InitializedShards != 256 || usage.Count != 1 || usage.PayloadJSONBytes != expectedBytes || usage.OldestReconciledAt == nil {
		t.Fatalf("receipt storage=%+v expected bytes=%d err=%v", usage, expectedBytes, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commit_receipt_usage SET receipt_count=receipt_count+10,
 payload_json_bytes=payload_json_bytes+10,reconciled_at=now()-interval '25 hours'
 WHERE shard=(SELECT get_byte(uuid_send(id),0) FROM commit_receipts LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	worked, err := store.ReconcileCommitReceiptStorage(ctx)
	if err != nil || !worked {
		t.Fatalf("reconcile drift: worked=%t err=%v", worked, err)
	}
	usage, err = store.CommitReceiptStorage(ctx)
	if err != nil || usage.Count != 1 || usage.PayloadJSONBytes != expectedBytes {
		t.Fatalf("reconciled receipt storage=%+v expected bytes=%d err=%v", usage, expectedBytes, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commit_receipt_usage SET reconciled_at=NULL WHERE shard=0`); err != nil {
		t.Fatal(err)
	}
	usage, err = store.CommitReceiptStorage(ctx)
	if err != nil || usage.OldestReconciledAt != nil {
		t.Fatalf("missing reconciliation timestamp was hidden: usage=%+v err=%v", usage, err)
	}
	if worked, err := store.ReconcileCommitReceiptStorage(ctx); err != nil || !worked {
		t.Fatalf("retry missing reconciliation: worked=%t err=%v", worked, err)
	}
}

func TestPgCommitReceiptUsageResumesBatches(t *testing.T) {
	store, pool, source, _ := commitManagedFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `UPDATE commit_receipt_usage SET initialized=true,reconciled_at=now() WHERE shard<>0`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO commit_receipts(id,account_id,source_id,event_id,event_type,payload,invocation_id)
 SELECT lpad(to_hex(n),32,'0')::uuid,$1::uuid,$2::uuid,gen_random_uuid(),
 'order.created',jsonb_build_object('n',n),gen_random_uuid() FROM generate_series(1,1001) AS n`, source.AccountID, source.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := store.ReconcileCommitReceiptStorage(ctx); err != nil || !worked {
		t.Fatalf("first batch: worked=%t err=%v", worked, err)
	}
	var active bool
	var partial int64
	if err := pool.QueryRow(ctx, `SELECT scan_in_progress,scan_count FROM commit_receipt_usage WHERE shard=0`).Scan(&active, &partial); err != nil || !active || partial != 1000 {
		t.Fatalf("persisted batch: active=%t count=%d err=%v", active, partial, err)
	}
	// Exercise changes on both sides of the cursor, including IDs crossing it.
	statements := []string{
		`DELETE FROM commit_receipts WHERE id=lpad(to_hex(1),32,'0')::uuid`,
		`UPDATE commit_receipts SET payload='{"n":2.0}' WHERE id=lpad(to_hex(2),32,'0')::uuid`,
		`UPDATE commit_receipts SET id=lpad(to_hex(3000),32,'0')::uuid WHERE id=lpad(to_hex(1000),32,'0')::uuid`,
		`UPDATE commit_receipts SET id=lpad(to_hex(1000),32,'0')::uuid WHERE id=lpad(to_hex(1001),32,'0')::uuid`,
		`INSERT INTO commit_receipts(id,account_id,source_id,event_id,event_type,payload,invocation_id)
 VALUES ('00000000-0000-0000-0000-000000000000',$1::uuid,$2::uuid,gen_random_uuid(),'order.created','{"new":true}',gen_random_uuid())`,
	}
	for _, statement := range statements {
		var err error
		if strings.Contains(statement, "$1") {
			_, err = pool.Exec(ctx, statement, source.AccountID, source.ID)
		} else {
			_, err = pool.Exec(ctx, statement)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	// An interrupted claim survives a restart and becomes retryable after its lease.
	if _, err := pool.Exec(ctx, `UPDATE commit_receipt_usage SET reconcile_token=gen_random_uuid(),
 retry_at=clock_timestamp()+interval '1 minute' WHERE shard=0`); err != nil {
		t.Fatal(err)
	}
	store = state.NewPgStore(pool)
	if worked, err := store.ReconcileCommitReceiptStorage(ctx); err != nil || worked {
		t.Fatalf("claimed shard was not deferred: worked=%t err=%v", worked, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commit_receipt_usage SET retry_at=clock_timestamp()-interval '1 second' WHERE shard=0`); err != nil {
		t.Fatal(err)
	}
	if worked, err := store.ReconcileCommitReceiptStorage(ctx); err != nil || !worked {
		t.Fatalf("resume: worked=%t err=%v", worked, err)
	}
	usage, err := store.CommitReceiptStorage(ctx)
	var count, bytes int64
	if scanErr := pool.QueryRow(ctx, `SELECT count(*),coalesce(sum(octet_length(payload::text)),0) FROM commit_receipts`).Scan(&count, &bytes); scanErr != nil {
		t.Fatal(scanErr)
	}
	if err != nil || usage.InitializedShards != 256 || usage.Count != count || usage.PayloadJSONBytes != bytes {
		t.Fatalf("resumed usage=%+v actual count=%d bytes=%d err=%v", usage, count, bytes, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commit_receipt_usage SET reconciled_at=now()-interval '25 hours' WHERE shard=0`); err != nil {
		t.Fatal(err)
	}
	if worked, err := store.ReconcileCommitReceiptStorage(ctx); err != nil || !worked {
		t.Fatalf("start periodic scan: worked=%t err=%v", worked, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM commit_sources WHERE id=$1::uuid`, source.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := store.ReconcileCommitReceiptStorage(ctx); err != nil || !worked {
		t.Fatalf("finish after source cascade: worked=%t err=%v", worked, err)
	}
	usage, err = store.CommitReceiptStorage(ctx)
	if err != nil || usage.Count != 0 || usage.PayloadJSONBytes != 0 {
		t.Fatalf("source cascade during scan: usage=%+v err=%v", usage, err)
	}
}

func TestPgCommitReceiptUsageFailedShardDoesNotBlockOthers(t *testing.T) {
	store, pool, _, _ := commitManagedFixture(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `UPDATE commit_receipt_usage SET initialized=true,reconciled_at=now() WHERE shard>1`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commit_receipt_usage SET retry_at=now()+interval '1 minute' WHERE shard=0`); err != nil {
		t.Fatal(err)
	}
	if worked, err := store.ReconcileCommitReceiptStorage(ctx); err != nil || !worked {
		t.Fatalf("other shard: worked=%t err=%v", worked, err)
	}
	var first, second bool
	if err := pool.QueryRow(ctx, `SELECT bool_or(initialized) FILTER(WHERE shard=0),
 bool_or(initialized) FILTER(WHERE shard=1) FROM commit_receipt_usage`).Scan(&first, &second); err != nil || first || !second {
		t.Fatalf("fair retry: first=%t second=%t err=%v", first, second, err)
	}
	// A progressing initial scan must also yield to an overdue initialized shard.
	if _, err := pool.Exec(ctx, `UPDATE commit_receipt_usage SET retry_at=clock_timestamp() WHERE shard=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commit_receipt_usage SET reconciled_at=now()-interval '25 hours',
 retry_at='-infinity' WHERE shard=1`); err != nil {
		t.Fatal(err)
	}
	if worked, err := store.ReconcileCommitReceiptStorage(ctx); err != nil || !worked {
		t.Fatalf("overdue initialized shard: worked=%t err=%v", worked, err)
	}
	var recentlyReconciled bool
	if err := pool.QueryRow(ctx, `SELECT reconciled_at>now()-interval '1 minute' FROM commit_receipt_usage WHERE shard=1`).
		Scan(&recentlyReconciled); err != nil || !recentlyReconciled {
		t.Fatalf("initialized shard starved: reconciled=%t err=%v", recentlyReconciled, err)
	}
}

func TestPgCommitReceiptUsageTimedOutBatchResumes(t *testing.T) {
	store, pool, source, _ := commitManagedFixture(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `UPDATE commit_receipt_usage SET initialized=true,reconciled_at=now() WHERE shard<>0`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO commit_receipts(id,account_id,source_id,event_id,event_type,payload,invocation_id)
 SELECT lpad(to_hex(n),32,'0')::uuid,$1::uuid,$2::uuid,gen_random_uuid(),
 'order.created',jsonb_build_object('n',n),gen_random_uuid() FROM generate_series(1,1001) n`, source.AccountID, source.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := store.ReconcileCommitReceiptStorage(ctx); err != nil || !worked {
		t.Fatalf("first batch: worked=%t err=%v", worked, err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `LOCK TABLE commit_receipts IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	timedCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	_, err = store.ReconcileCommitReceiptStorage(timedCtx)
	cancel()
	if err == nil {
		t.Fatal("blocked scan did not time out")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var active bool
	var partial int64
	var deferred bool
	if err := pool.QueryRow(ctx, `SELECT scan_in_progress,scan_count,retry_at>now() FROM commit_receipt_usage WHERE shard=0`).
		Scan(&active, &partial, &deferred); err != nil || !active || partial != 1000 || !deferred {
		t.Fatalf("timed-out progress: active=%t partial=%d deferred=%t err=%v", active, partial, deferred, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commit_receipt_usage SET initialized=false,reconciled_at=NULL,retry_at='-infinity' WHERE shard=1`); err != nil {
		t.Fatal(err)
	}
	if worked, err := store.ReconcileCommitReceiptStorage(ctx); err != nil || !worked {
		t.Fatalf("other shard after timeout: worked=%t err=%v", worked, err)
	}
	var otherInitialized bool
	if err := pool.QueryRow(ctx, `SELECT initialized FROM commit_receipt_usage WHERE shard=1`).Scan(&otherInitialized); err != nil || !otherInitialized {
		t.Fatalf("other shard: initialized=%t err=%v", otherInitialized, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commit_receipt_usage SET retry_at=now()-interval '1 second' WHERE shard=0`); err != nil {
		t.Fatal(err)
	}
	if worked, err := store.ReconcileCommitReceiptStorage(ctx); err != nil || !worked {
		t.Fatalf("retry after timeout: worked=%t err=%v", worked, err)
	}
	usage, err := store.CommitReceiptStorage(ctx)
	var count, bytes int64
	if scanErr := pool.QueryRow(ctx, `SELECT count(*),coalesce(sum(octet_length(payload::text)),0) FROM commit_receipts`).Scan(&count, &bytes); scanErr != nil {
		t.Fatal(scanErr)
	}
	if err != nil || usage.InitializedShards != 256 || usage.Count != count || usage.PayloadJSONBytes != bytes {
		t.Fatalf("retry usage=%+v actual count=%d bytes=%d err=%v", usage, count, bytes, err)
	}
}

func TestPgCommitReceiptUsageConcurrentBatches(t *testing.T) {
	store, pool, source, _ := commitManagedFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `UPDATE commit_receipt_usage SET initialized=true,reconciled_at=now() WHERE shard<>0`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO commit_receipts(id,account_id,source_id,event_id,event_type,payload,invocation_id)
 SELECT lpad(to_hex(n),32,'0')::uuid,$1::uuid,$2::uuid,gen_random_uuid(),
 'order.created',jsonb_build_object('n',n),gen_random_uuid() FROM generate_series(1,5001) AS n`, source.AccountID, source.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := store.ReconcileCommitReceiptStorage(ctx); err != nil || !worked {
		t.Fatalf("first batch: worked=%t err=%v", worked, err)
	}
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		for i := 0; i < 20; i++ {
			if _, err := pool.Exec(ctx, `UPDATE commit_receipts SET payload=jsonb_build_object('revision',$1::int)
 WHERE id >= lpad(to_hex(1),32,'0')::uuid AND id <= lpad(to_hex(100),32,'0')::uuid`, i); err != nil {
				done <- err
				return
			}
			if _, err := pool.Exec(ctx, `INSERT INTO commit_receipts(id,account_id,source_id,event_id,event_type,payload,invocation_id)
 SELECT lpad(to_hex(10000+$1::int*10+n),32,'0')::uuid,$2::uuid,$3::uuid,
 gen_random_uuid(),'order.created',jsonb_build_object('n',n),gen_random_uuid() FROM generate_series(1,10) n`, i, source.AccountID, source.ID); err != nil {
				done <- err
				return
			}
			if _, err := pool.Exec(ctx, `DELETE FROM commit_receipts WHERE id=lpad(to_hex($1::int+1),32,'0')::uuid`, i); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	<-started
	for {
		var initialized bool
		if err := pool.QueryRow(ctx, `SELECT initialized FROM commit_receipt_usage WHERE shard=0`).Scan(&initialized); err != nil {
			t.Fatal(err)
		}
		if initialized {
			break
		}
		worked, err := store.ReconcileCommitReceiptStorage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	usage, err := store.CommitReceiptStorage(ctx)
	var count, bytes int64
	if scanErr := pool.QueryRow(ctx, `SELECT count(*),coalesce(sum(octet_length(payload::text)),0) FROM commit_receipts`).Scan(&count, &bytes); scanErr != nil {
		t.Fatal(scanErr)
	}
	if err != nil || usage.Count != count || usage.PayloadJSONBytes != bytes {
		t.Fatalf("concurrent usage=%+v actual count=%d bytes=%d err=%v", usage, count, bytes, err)
	}
}

func TestPgCommitReceiptUsageConcurrentBackfillAndDeletion(t *testing.T) {
	store, pool, source, _ := commitManagedFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `UPDATE commit_receipt_usage SET initialized=true,reconciled_at=now() WHERE shard<>0`); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO commit_receipts(id,account_id,source_id,event_id,event_type,payload,invocation_id)
 VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,'order.created',$5::jsonb,$6::uuid)`
	first, second := "00000000-0000-0000-0000-000000000011", "00000000-0000-0000-0000-000000000012"
	if _, err := pool.Exec(ctx, insert, first, source.AccountID, source.ID, uuid.NewString(), `{"n":1}`, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SELECT shard FROM commit_receipt_usage WHERE shard=0 FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	if worked, err := store.ReconcileCommitReceiptStorage(ctx); err != nil || worked {
		t.Fatalf("locked shard should be retried: worked=%t err=%v", worked, err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var pid int
	if err := conn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := conn.Exec(ctx, insert, second, source.AccountID, source.ID, uuid.NewString(), `{"n":2}`, uuid.NewString())
		done <- err
	}()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("writer did not wait for backfill lock")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE commit_receipt_usage SET
 receipt_count=(SELECT count(*) FROM commit_receipts WHERE id >= '00000000-0000-0000-0000-000000000000' AND id <= '00ffffff-ffff-ffff-ffff-ffffffffffff'),
 payload_json_bytes=(SELECT coalesce(sum(octet_length(payload::text)),0) FROM commit_receipts WHERE id >= '00000000-0000-0000-0000-000000000000' AND id <= '00ffffff-ffff-ffff-ffff-ffffffffffff'),
 initialized=true,reconciled_at=now() WHERE shard=0`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	assertUsage := func(wantCount int64) {
		t.Helper()
		usage, err := store.CommitReceiptStorage(ctx)
		var count, bytes int64
		if scanErr := pool.QueryRow(ctx, `SELECT count(*),coalesce(sum(octet_length(payload::text)),0) FROM commit_receipts`).Scan(&count, &bytes); scanErr != nil {
			t.Fatal(scanErr)
		}
		if err != nil || usage.InitializedShards != 256 || usage.Count != count || count != wantCount || usage.PayloadJSONBytes != bytes {
			t.Fatalf("usage=%+v actual count=%d bytes=%d err=%v", usage, count, bytes, err)
		}
	}
	assertUsage(2)
	if _, err := pool.Exec(ctx, insert+` ON CONFLICT (id) DO NOTHING`, second, source.AccountID, source.ID, uuid.NewString(), `{"n":2}`, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commit_receipts SET payload='{"n":1.0}' WHERE id=$1::uuid`, first); err != nil {
		t.Fatal(err)
	}
	assertUsage(2)
	if _, err := pool.Exec(ctx, `DELETE FROM commit_sources WHERE id=$1::uuid`, source.ID); err != nil {
		t.Fatal(err)
	}
	assertUsage(0)
}

func TestPgCommitRelayHealthyReservation(t *testing.T) {
	store, pool, existing, _ := commitManagedFixture(t)
	ctx := t.Context()
	ids := map[string]string{}
	for _, c := range []struct {
		name, status, checkedAgo, dueAgo string
		enabled                          bool
	}{
		{"failed", "database_unavailable", "30 seconds", "20 seconds", true},
		{"stale", "healthy", "10 minutes", "15 seconds", true},
		{"new", "unconfigured", "", "10 seconds", true},
		{"fresh", "healthy", "30 seconds", "5 seconds", true},
		{"paused", "healthy", "30 seconds", "20 seconds", false},
		{"waiting", "healthy", "30 seconds", "-5 minutes", true},
	} {
		source, err := store.CreateCommitSource(ctx, state.CommitSource{
			AccountID: existing.AccountID, AppID: existing.AppID, Name: c.name,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetCommitSourceConnection(ctx, source.AccountID, source.ID, []byte("relay-fixture")); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE commit_sources SET relay_status=$2,
			last_checked_at=statement_timestamp()-NULLIF($3,'')::interval,
			relay_next_poll_at=statement_timestamp()-$4::interval,enabled=$5 WHERE id=$1::uuid`,
			source.ID, c.status, c.checkedAgo, c.dueAgo, c.enabled); err != nil {
			t.Fatal(err)
		}
		ids[c.name] = source.ID
	}
	reserved, err := store.ClaimHealthyCommitRelaySources(ctx, 8, time.Minute)
	if err != nil || len(reserved) != 1 || reserved[0].ID != ids["fresh"] {
		t.Fatalf("expected only the fresh and due source, got %d reservations and error %v", len(reserved), err)
	}
	for _, name := range []string{"failed", "stale", "new"} {
		claimed, err := store.ClaimCommitRelaySources(ctx, 1, time.Minute)
		if err != nil || len(claimed) != 1 || claimed[0].ID != ids[name] {
			t.Fatalf("expected general worker to claim %s in due order, got %d claims and error %v", name, len(claimed), err)
		}
	}
	if claimed, err := store.ClaimCommitRelaySources(ctx, 1, time.Minute); err != nil || len(claimed) != 0 {
		t.Fatalf("expected leased, future and paused sources to stay unclaimed, got %d claims and error %v", len(claimed), err)
	}
}

func TestPgCommitRelayConnectionObservationFenced(t *testing.T) {
	store, pool, source, _ := commitManagedFixture(t)
	ctx := t.Context()
	seal := func() {
		t.Helper()
		if err := store.SetCommitSourceConnection(ctx, source.AccountID, source.ID, []byte("relay-observation-fixture")); err != nil {
			t.Fatal(err)
		}
	}
	claim := func(reserved bool) state.CommitRelaySource {
		t.Helper()
		take := store.ClaimCommitRelaySources
		if reserved {
			take = store.ClaimHealthyCommitRelaySources
		}
		rows, err := take(ctx, 1, time.Minute)
		if err != nil || len(rows) != 1 {
			t.Fatalf("relay claim: %d %v", len(rows), err)
		}
		return rows[0]
	}
	record := func(lease state.CommitRelaySource) {
		t.Helper()
		observed := int32(1258)
		if err := store.RecordCommitRelayPoll(ctx, lease, "healthy", nil, nil, nil, 0, state.CommitRelayPollObservation{ConnectMS: &observed}); err != nil {
			t.Fatal(err)
		}
	}
	seal()
	record(claim(false))
	if _, err := pool.Exec(ctx, `UPDATE commit_sources SET relay_next_poll_at='-infinity',
		last_checked_at=statement_timestamp()-interval '10 seconds',
		relay_connect_observed_at=statement_timestamp()-interval '10 seconds' WHERE id=$1::uuid`, source.ID); err != nil {
		t.Fatal(err)
	}
	fresh := claim(true)
	if fresh.LastConnectMS != 1258 {
		t.Fatalf("fresh connection observation=%d", fresh.LastConnectMS)
	}
	// A scheduler that only writes health advances last_checked_at without
	// publishing a connection observation.
	if _, err := pool.Exec(ctx, `UPDATE commit_sources SET relay_next_poll_at='-infinity', relay_poll_token=NULL,
		last_checked_at=statement_timestamp()-interval '9 seconds' WHERE id=$1::uuid`, source.ID); err != nil {
		t.Fatal(err)
	}
	stale := claim(true)
	if stale.LastConnectMS != 0 {
		t.Fatalf("stale connection observation=%d", stale.LastConnectMS)
	}
	seal()
	record(stale)
	if got := claim(false).LastConnectMS; got != 0 {
		t.Fatalf("old credential published connection observation=%d", got)
	}
}

func TestPgCommitObservationSummaryLifecycle(t *testing.T) {
	store, pool, source, _ := commitManagedFixture(t)
	ctx := t.Context()
	read := func() state.CommitRelayObservation {
		t.Helper()
		snapshot, err := store.CommitRelayObservationSummary(ctx, time.Now().Add(-commitwork.ObservationGracePeriod))
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	if got := read(); got.EnabledSources != 1 || got.UnknownSources != 1 {
		t.Fatalf("unconfigured=%+v", got)
	}
	if err := store.SetCommitSourceConnection(ctx, source.AccountID, source.ID, []byte("opaque-ciphertext-fixture")); err != nil {
		t.Fatal(err)
	}
	claim := func() state.CommitRelaySource {
		t.Helper()
		claimed, err := store.ClaimCommitRelaySources(ctx, 1, time.Minute)
		if err != nil || len(claimed) != 1 {
			t.Fatalf("claim count=%d err=%v", len(claimed), err)
		}
		return claimed[0]
	}
	makeDue := func(id string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE commit_sources SET relay_next_poll_at='-infinity',
 last_checked_at=clock_timestamp()-interval '20 minutes' WHERE id=$1::uuid`, id); err != nil {
			t.Fatal(err)
		}
	}
	pending, blocked := int64(4), int64(2)
	cleanup := int64(17)
	oldest := time.Now().Add(-20 * time.Minute).UTC()
	if err := store.RecordCommitRelayStatus(ctx, claim(), "blocked_events", &pending, &blocked, &oldest, 0, &cleanup); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.UnknownSources != 0 || got.PendingEvents != 4 || got.BlockedEvents != 2 || got.FailingSources != 0 || got.OldestPendingTimestamp <= 0 || got.CleanupUnknownSources != 0 || got.CleanupEligibleEvents != 17 {
		t.Fatalf("known=%+v", got)
	}
	makeDue(source.ID)
	if err := store.RecordCommitRelayStatus(ctx, claim(), "database_unavailable", nil, nil, nil, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.UnknownSources != 1 || got.PendingEvents != 0 || got.BlockedEvents != 0 || got.FailingSources != 1 || got.OldestPendingTimestamp != 0 || got.CleanupUnknownSources != 1 || got.CleanupEligibleEvents != 0 {
		t.Fatalf("outage inferred empty/old snapshot=%+v", got)
	}
	makeDue(source.ID)
	if err := store.RecordCommitRelayStatus(ctx, claim(), "healthy", &pending, &blocked, &oldest, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commit_sources SET last_checked_at=now()-interval '10 minutes' WHERE id=$1::uuid`, source.ID); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.UnknownSources != 1 || got.PendingEvents != 0 || got.OldestPendingTimestamp != 0 {
		t.Fatalf("stale=%+v", got)
	}
	makeDue(source.ID)
	row := claim()
	if err := store.SetCommitSourceConnection(ctx, source.AccountID, source.ID, []byte("rotated-ciphertext-fixture")); err != nil {
		t.Fatal(err)
	}
	// An old pass must not make the rotated credential appear healthy.
	if err := store.RecordCommitRelayStatus(ctx, row, "healthy", &pending, &blocked, &oldest, 0); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.UnknownSources != 1 || got.PendingEvents != 0 {
		t.Fatalf("retired credential status=%+v", got)
	}
	row = claim()
	if _, err := store.SetCommitSourceEnabled(ctx, source.AccountID, source.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.EnabledSources != 0 || got.PendingEvents != 0 {
		t.Fatalf("paused=%+v", got)
	}
	if _, err := store.SetCommitSourceEnabled(ctx, source.AccountID, source.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.UnknownSources != 1 || got.PendingEvents != 0 {
		t.Fatalf("resume reused stale observations=%+v", got)
	}
	if err := store.RecordCommitRelayStatus(ctx, row, "healthy", &pending, &blocked, &oldest, 0); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.UnknownSources != 1 || got.PendingEvents != 0 {
		t.Fatalf("pre-pause pass overwrote resumed health=%+v", got)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM commit_sources WHERE id=$1::uuid`, source.ID); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.EnabledSources != 0 || got.UnknownSources != 0 {
		t.Fatalf("deleted=%+v", got)
	}

	changes, err := store.UpsertCommitChangesSource(ctx, source.AccountID, source.AppID, "order-changes", state.CommitChangesConfig{
		QueueName: "order-changes", Feeds: []byte(`[{"name":"orders","table":"public.orders","columns":["id"]}]`), PollIntervalMS: 600_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	changes, err = store.CommitSourceByID(ctx, source.AccountID, changes.ID)
	if err != nil || changes.PollIntervalMS != 600_000 {
		t.Fatalf("changes source did not expose its poll interval: %+v %v", changes, err)
	}
	if err := store.SetCommitSourceConnection(ctx, source.AccountID, changes.ID, []byte("changes-ciphertext-fixture")); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordCommitRelayStatus(ctx, claim(), "healthy", &pending, &blocked, &oldest, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commit_sources SET last_checked_at=now()-interval '10 minutes' WHERE id=$1::uuid`, changes.ID); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.UnknownSources != 0 || got.PendingEvents != 4 {
		t.Fatalf("changes source while due to poll=%+v", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE commit_sources SET last_checked_at=now()-interval '16 minutes' WHERE id=$1::uuid`, changes.ID); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.UnknownSources != 1 || got.PendingEvents != 0 {
		t.Fatalf("changes source past its grace period=%+v", got)
	}
}

func TestPgCommitRelayFencesExpiredSourceLease(t *testing.T) {
	store, pool, source, _ := commitManagedFixture(t)
	ctx := t.Context()
	if err := store.SetCommitSourceConnection(ctx, source.AccountID, source.ID, []byte("ciphertext-fixture")); err != nil {
		t.Fatal(err)
	}
	claim := func() state.CommitRelaySource {
		t.Helper()
		rows, err := store.ClaimCommitRelaySources(ctx, 1, time.Minute)
		if err != nil || len(rows) != 1 || rows[0].ID != source.ID {
			t.Fatalf("claim count=%d err=%v", len(rows), err)
		}
		return rows[0]
	}
	old := claim()
	if _, err := pool.Exec(ctx, `UPDATE commit_sources SET relay_next_poll_at=clock_timestamp()-interval '1 second' WHERE id=$1::uuid`, source.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordCommitRelayStatus(ctx, old, "database_unavailable", nil, nil, nil, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	current := claim()
	if old.PollToken == current.PollToken || old.CredentialRevision != current.CredentialRevision {
		t.Fatal("expired source was not reclaimed with a new lease for the same credential")
	}
	if err := store.RecordCommitRelayStatus(ctx, old, "database_unavailable", nil, nil, nil, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordCommitBlockedEvents(ctx, old, []state.CommitBlockedEvent{{EventID: "00000000-0000-4000-8000-000000000001", Type: "order.created", Code: "invalid_event", CreatedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	if items, err := store.ListCommitBlockedEvents(ctx, source.AccountID, source.ID); err != nil || len(items) != 0 {
		t.Fatalf("expired lease wrote blocked events: %v %v", items, err)
	}
	if got, err := store.CommitSourceByID(ctx, source.AccountID, source.ID); err != nil || got.RelayStatus != "unconfigured" {
		t.Fatalf("expired lease wrote status: %q %v", got.RelayStatus, err)
	}
	if err := store.RecordCommitRelayStatus(ctx, current, "healthy", nil, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	if got, err := store.CommitSourceByID(ctx, source.AccountID, source.ID); err != nil || got.RelayStatus != "healthy" {
		t.Fatalf("current lease could not write status: %q %v", got.RelayStatus, err)
	}
}
