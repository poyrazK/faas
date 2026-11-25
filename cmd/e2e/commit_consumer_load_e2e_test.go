package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// The source and its private PostgreSQL cluster remain owned by the parent
// process fixture and are cleaned up after this subtest.
type commitConsumerLoadFixture struct {
	customer  *pgxpool.Pool
	source    state.CommitSource
	sealRelay func(sourceID, database string, port uint16) ([]byte, error)
	relayLog  func() string
}

type commitConsumerLoadResult struct {
	ids []string
	err error
}

func TestCommitConsumerLoadProducerTransactions(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL required")
	}
	pool := pgtest.OpenDatabase(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, commitwork.Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE public.orders(id uuid PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	var produced atomic.Int64
	ids, err := produceCommitConsumerLoad(ctx, pool, 2*time.Second, 250*time.Millisecond, &produced)
	if err != nil || len(ids) < 4 || int64(len(ids)) != produced.Load() {
		t.Fatalf("producer result: ids=%d count=%d err=%v", len(ids), produced.Load(), err)
	}
	var orders, events, matching int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM public.orders),
 (SELECT count(*) FROM public.gregale_outbox),
 (SELECT count(*) FROM public.orders o JOIN public.gregale_outbox e ON e.event_id=o.id AND e.event_type='order.created')`).Scan(&orders, &events, &matching); err != nil {
		t.Fatal(err)
	}
	if orders != len(ids) || events != len(ids) || matching != len(ids) {
		t.Fatalf("producer identity mismatch: ids=%d orders=%d events=%d matching=%d", len(ids), orders, events, matching)
	}
	for _, id := range ids {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.orders o JOIN public.gregale_outbox e ON e.event_id=o.id WHERE o.id=$1::uuid)`, id).Scan(&exists); err != nil || !exists {
			t.Fatalf("missing producer identity %s: %v", id, err)
		}
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_test_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture rejection'; END $$;
 CREATE TRIGGER reject_test_outbox BEFORE INSERT ON public.gregale_outbox FOR EACH ROW EXECUTE FUNCTION reject_test_outbox()`); err != nil {
		t.Fatal(err)
	}
	if _, err := produceCommitConsumerLoad(ctx, pool, time.Second, 250*time.Millisecond, &produced); err == nil {
		t.Fatal("rejected outbox insert returned success")
	}
	var afterFailure int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.orders`).Scan(&afterFailure); err != nil || afterFailure != orders {
		t.Fatalf("failed outbox insert did not roll back business row: orders=%d want=%d err=%v", afterFailure, orders, err)
	}
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := produceCommitConsumerLoad(stopped, pool, time.Second, time.Second, &produced); err == nil {
		t.Fatal("cancelled producer returned success")
	}
}

func runCommitConsumerSustainedLoad(t *testing.T, f *normalPathFixture, consumer *pgxpool.Pool, fixture commitConsumerLoadFixture, client *http.Client, address string) {
	t.Helper()
	duration := 75 * time.Second
	if raw := os.Getenv("GREGALE_COMMIT_CONSUMER_LOAD_DURATION"); raw != "" {
		var err error
		duration, err = time.ParseDuration(raw)
		if err != nil || duration < 65*time.Second || duration > 5*time.Minute {
			t.Fatal("GREGALE_COMMIT_CONSUMER_LOAD_DURATION must be between 65s and 5m")
		}
	}
	rate := 1
	if raw := os.Getenv("GREGALE_COMMIT_CONSUMER_LOAD_RATE"); raw != "" {
		var err error
		rate, err = strconv.Atoi(raw)
		if err != nil || rate < 1 || rate > 5 {
			t.Fatal("GREGALE_COMMIT_CONSUMER_LOAD_RATE must be between 1 and 5")
		}
	}
	if int((duration+time.Second)/time.Second)*rate+5 >= api.MaxExclusivePendingPerAccount {
		t.Fatal("configured workload could exceed the account's pending Operation quota")
	}
	if fixture.customer == nil || fixture.source.ID == "" {
		t.Fatal("missing private Commit source fixture")
	}
	ctx, cancel := context.WithTimeout(t.Context(), duration+5*time.Minute)
	defer cancel()
	var baselineOperations int
	if err := f.h.Pool.QueryRow(ctx, `SELECT count(*) FROM exclusive_work_operations WHERE app_id=$1::uuid`, f.app.ID).Scan(&baselineOperations); err != nil {
		t.Fatal(err)
	}
	// group_key is present in changes-source outboxes. Use the same relay
	// ordering contract for a blocked head and its younger follower.
	if _, err := fixture.customer.Exec(ctx, `ALTER TABLE public.gregale_outbox ADD COLUMN group_key text`); err != nil {
		t.Fatal(err)
	}
	var produced atomic.Int64
	result := make(chan commitConsumerLoadResult, 1)
	go func() {
		ids, err := produceCommitConsumerLoad(ctx, fixture.customer, duration, time.Second/time.Duration(rate), &produced)
		result <- commitConsumerLoadResult{ids: ids, err: err}
	}()
	defer func() { cancel(); <-result }()
	waitCommitLoadCondition(t, ctx, "initial producer transaction", func() (bool, error) { return produced.Load() >= 2, nil })

	// Reject only the relay role, leaving the producer's private database online.
	// Terminating its existing sessions makes the outage affect the next poll.
	if _, err := fixture.customer.Exec(ctx, `ALTER ROLE relay NOLOGIN`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = fixture.customer.Exec(context.Background(), `ALTER ROLE relay LOGIN`) })
	if _, err := fixture.customer.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename='relay' AND pid<>pg_backend_pid()`); err != nil {
		t.Fatal(err)
	}
	unknown := waitCommitSourceHealth(t, f.h, f.key, fixture.source.ID, "database_unavailable", time.Time{})
	if unknown.PendingEvents != nil || unknown.BlockedEvents != nil {
		t.Fatalf("unavailable source exposed a known backlog: %+v", unknown)
	}
	observation, err := f.store.CommitRelayObservationSummary(ctx, time.Now().Add(-time.Minute))
	if err != nil || observation.UnknownSources < 1 || observation.FailingSources < 1 {
		t.Fatalf("outage observation: %+v (%v)", observation, err)
	}
	before := produced.Load()
	waitCommitLoadCondition(t, ctx, "production while relay is unavailable", func() (bool, error) { return produced.Load() >= before+2, nil })
	var outageBacklog []string
	if err := fixture.customer.QueryRow(ctx, `SELECT ARRAY(SELECT event_id::text FROM public.gregale_outbox WHERE accepted_at IS NULL)`).Scan(&outageBacklog); err != nil || len(outageBacklog) < 2 {
		t.Fatalf("unavailable relay did not retain producer backlog: pending=%d err=%v", len(outageBacklog), err)
	}

	// Seed the poison and its follower in one transaction, plus an unrelated
	// event. The follower cannot be accepted until the blocked head is repaired.
	poison, follower, independent := uuid.NewString(), uuid.NewString(), uuid.NewString()
	tx, err := fixture.customer.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `INSERT INTO public.gregale_outbox(event_id,event_type,payload,group_key,created_at) VALUES
 ($1::uuid,' ','{}','blocked-order',clock_timestamp()),
 ($2::uuid,'order.created','{}','blocked-order',clock_timestamp()+interval '1 millisecond')`, poison, follower); err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO public.orders(id) VALUES($1::uuid),($2::uuid),($3::uuid)`, poison, follower, independent)
	}
	if err == nil {
		err = commitwork.Insert(ctx, tx, commitwork.Event{ID: independent, Type: "order.created", Data: []byte(`{}`)})
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
	select {
	case early := <-result:
		result <- early
		t.Fatal("producer stopped before relay recovery began")
	default:
	}
	recoveryStart := produced.Load()
	var relayRecoveryStartedAt time.Time
	if err := fixture.customer.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&relayRecoveryStartedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.customer.Exec(ctx, `ALTER ROLE relay LOGIN`); err != nil {
		t.Fatal(err)
	}
	waitCommitLoadCondition(t, ctx, "poison block and independent business effect", func() (bool, error) {
		var blocked, waiting, effect, published bool
		if err := fixture.customer.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM public.gregale_outbox WHERE event_id=$1::uuid AND blocked_code IS NOT NULL),
 EXISTS(SELECT 1 FROM public.gregale_outbox WHERE event_id=$2::uuid AND accepted_at IS NULL)`, poison, follower).Scan(&blocked, &waiting); err != nil {
			return false, err
		}
		if err := consumer.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM processed_events WHERE source=$1 AND event_id=$2::uuid)`, "gregale.commit."+fixture.source.ID, independent).Scan(&effect); err != nil {
			return false, err
		}
		if err := f.h.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM commit_blocked_events WHERE source_id=$1::uuid AND event_id=$2::uuid)`, fixture.source.ID, poison).Scan(&published); err != nil {
			return false, err
		}
		return blocked && waiting && effect && published, nil
	})
	waitCommitLoadCondition(t, ctx, "production through relay recovery", func() (bool, error) { return produced.Load() >= recoveryStart+2, nil })
	select {
	case early := <-result:
		result <- early
		t.Fatal("producer stopped before independent delivery recovered")
	default:
	}
	if _, err := fixture.customer.Exec(ctx, `UPDATE public.gregale_outbox SET event_type='order.created' WHERE event_id=$1::uuid AND blocked_code IS NOT NULL`, poison); err != nil {
		t.Fatal(err)
	}
	body, code := doReq(t, f.h, f.key, http.MethodPost, "/v1/commit-sources/"+fixture.source.ID+"/events/"+poison+"/replay", nil)
	if code != http.StatusAccepted {
		t.Fatalf("request poison replay: %d %s", code, body)
	}
	var workload commitConsumerLoadResult
	select {
	case workload = <-result:
		// Put the result back for the cleanup that joins the producer.
		result <- workload
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if workload.err != nil || len(workload.ids) < 2 {
		t.Fatalf("producer workload: events=%d err=%v", len(workload.ids), workload.err)
	}
	expected := append(workload.ids, poison, follower, independent)
	waitCommitConsumerLoadDrain(t, ctx, f, fixture, consumer, expected, baselineOperations)
	var checkpointedAt time.Time
	if err := fixture.customer.QueryRow(ctx, `SELECT max(accepted_at) FROM public.gregale_outbox WHERE event_id::text=ANY($1)`,
		outageBacklog).Scan(&checkpointedAt); err != nil {
		t.Fatal(err)
	}
	t.Logf("Commit relay outage backlog drained: events=%d checkpoint_delay=%s", len(outageBacklog), checkpointedAt.Sub(relayRecoveryStartedAt).Round(time.Millisecond))
	duplicate, err := json.Marshal(map[string]string{"id": workload.ids[0], "source": "gregale.commit." + fixture.source.ID, "type": "order.created"})
	if err != nil {
		t.Fatal(err)
	}
	duplicates := make(chan error, 8)
	var requests sync.WaitGroup
	for range 8 {
		requests.Add(1)
		go func() {
			defer requests.Done()
			response, err := client.Post("http://"+address+"/", "application/cloudevents+json", bytes.NewReader(duplicate))
			if err == nil {
				_, err = io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if err == nil && response.StatusCode != http.StatusOK {
					err = fmt.Errorf("duplicate consumer status: %d", response.StatusCode)
				}
			}
			duplicates <- err
		}()
	}
	requests.Wait()
	close(duplicates)
	for err := range duplicates {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := commitConsumerCounts(ctx, consumer, len(expected)+2, len(expected)+2); err != nil {
		t.Fatalf("duplicate delivery changed business effects: %v", err)
	}
	var ordered bool
	if err := fixture.customer.QueryRow(ctx, `SELECT p.accepted_at <= f.accepted_at FROM public.gregale_outbox p, public.gregale_outbox f WHERE p.event_id=$1::uuid AND f.event_id=$2::uuid`, poison, follower).Scan(&ordered); err != nil || !ordered {
		t.Fatalf("replayed head and follower checkpoint order: ordered=%t err=%v", ordered, err)
	}
	t.Logf("sustained Commit consumer load passed: duration=%s rate=%d events=%d", duration, rate, len(expected))
}

func produceCommitConsumerLoad(ctx context.Context, pool *pgxpool.Pool, duration, interval time.Duration, produced *atomic.Int64) ([]string, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	timer := time.NewTimer(duration)
	defer timer.Stop()
	var ids []string
	for {
		select {
		case <-ctx.Done():
			return ids, ctx.Err()
		case <-timer.C:
			return ids, nil
		case <-ticker.C:
			id, err := commitConsumerOrder(ctx, pool)
			if err != nil {
				return ids, err
			}
			ids = append(ids, id)
			produced.Add(1)
		}
	}
}

func commitConsumerOrder(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	id := uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `INSERT INTO public.orders(id) VALUES($1::uuid)`, id); err != nil {
		return "", err
	}
	if err := commitwork.Insert(ctx, tx, commitwork.Event{ID: id, Type: "order.created", Data: []byte(`{}`)}); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func waitCommitLoadCondition(t *testing.T, ctx context.Context, description string, check func() (bool, error)) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready, err := check()
		if err != nil {
			t.Fatalf("%s: %v", description, err)
		}
		if ready {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s: %v", description, ctx.Err())
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", description)
		case <-ticker.C:
		}
	}
}

func waitCommitConsumerLoadDrain(t *testing.T, ctx context.Context, f *normalPathFixture, fixture commitConsumerLoadFixture, consumer *pgxpool.Pool, expected []string, baselineOperations int) {
	t.Helper()
	want := len(expected) + 2 // The original crash/replay scenario completed two events.
	var receipts, completed, unsuccessful, checkpoints, effects, markers, operations int
	defer func() {
		if t.Failed() {
			t.Logf("drain counts: receipts=%d completed=%d unsuccessful=%d checkpoints=%d effects=%d markers=%d operations=%d want=%d",
				receipts, completed, unsuccessful, checkpoints, effects, markers, operations, want)
		}
	}()
	waitCommitLoadCondition(t, ctx, "completed Operations and consumer effects", func() (bool, error) {
		if err := f.h.Pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE operation_state='completed'),
			count(*) FILTER (WHERE operation_state IN ('failed','cancelled','expired'))
			FROM commit_receipts WHERE source_id=$1::uuid`, fixture.source.ID).Scan(&receipts, &completed, &unsuccessful); err != nil {
			return false, err
		}
		if unsuccessful != 0 {
			return false, fmt.Errorf("%d accepted Commit Operations finished unsuccessfully", unsuccessful)
		}
		if err := fixture.customer.QueryRow(ctx, `SELECT count(*) FROM public.gregale_outbox WHERE accepted_at IS NOT NULL AND receipt_id IS NOT NULL`).Scan(&checkpoints); err != nil {
			return false, err
		}
		if err := f.h.Pool.QueryRow(ctx, `SELECT count(*) FROM exclusive_work_operations WHERE app_id=$1::uuid`, f.app.ID).Scan(&operations); err != nil {
			return false, err
		}
		if err := consumer.QueryRow(ctx, `SELECT (SELECT total FROM business_effects WHERE id=1),(SELECT count(*) FROM processed_events WHERE source=$1)`, "gregale.commit."+fixture.source.ID).Scan(&effects, &markers); err != nil {
			return false, err
		}
		if receipts > want || completed > want || checkpoints > want || effects > want || markers > want || operations > baselineOperations+len(expected) {
			return false, fmt.Errorf("unexpected duplicate or extra work: receipts=%d completed=%d checkpoints=%d effects=%d markers=%d operations=%d want=%d", receipts, completed, checkpoints, effects, markers, operations, want)
		}
		return receipts == want && completed == want && checkpoints == want && effects == want && markers == want && operations == baselineOperations+len(expected), nil
	})
	for _, id := range expected {
		var business, checkpoint, marker bool
		if err := fixture.customer.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.orders WHERE id=$1::uuid),EXISTS(SELECT 1 FROM public.gregale_outbox WHERE event_id=$1::uuid AND accepted_at IS NOT NULL)`, id).Scan(&business, &checkpoint); err != nil {
			t.Fatal(err)
		}
		if err := consumer.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM processed_events WHERE source=$1 AND event_id=$2::uuid)`, "gregale.commit."+fixture.source.ID, id).Scan(&marker); err != nil || !business || !checkpoint || !marker {
			t.Fatalf("event identity %s: business=%t checkpoint=%t marker=%t err=%v", id, business, checkpoint, marker, err)
		}
		if _, err := f.store.CommitReceiptByEvent(ctx, fixture.source.AccountID, fixture.source.ID, id); err != nil {
			t.Fatalf("event %s receipt: %v", id, err)
		}
	}
	waitCommitLoadCondition(t, ctx, "fresh healthy source and zero backlog", func() (bool, error) {
		source, err := f.store.CommitSourceByID(ctx, fixture.source.AccountID, fixture.source.ID)
		if err != nil {
			return false, err
		}
		if source.RelayStatus != "healthy" || source.PendingEvents == nil || *source.PendingEvents != 0 || source.BlockedEvents == nil || *source.BlockedEvents != 0 || source.LastCheckedAt == nil || time.Since(*source.LastCheckedAt) > time.Minute {
			return false, nil
		}
		observation, err := f.store.CommitRelayObservationSummary(ctx, time.Now().Add(-time.Minute))
		return err == nil && observation.UnknownSources == 0 && observation.FailingSources == 0 && observation.PendingEvents == 0 && observation.BlockedEvents == 0, err
	})
}
