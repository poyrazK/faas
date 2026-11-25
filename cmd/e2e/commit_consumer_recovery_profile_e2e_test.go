package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	commitProfileOutageEvents   = 65
	commitProfileRecoveryEvents = 13
	commitProfileEventInterval  = 5 * time.Second
)

type commitProfileEvent struct {
	id     string
	source int
}

type commitProfileTiming struct {
	Source       int   `json:"source"`
	ReceiptMS    int64 `json:"receipt_ms"`
	CheckpointMS int64 `json:"checkpoint_ms"`
	StartedMS    int64 `json:"started_ms"`
	CompletedMS  int64 `json:"completed_ms"`
	Attempts     int   `json:"attempts"`
	producedAt   time.Time
	receiptAt    time.Time
}

func runCommitRecoveryProfile(t *testing.T, f *normalPathFixture, consumer *pgxpool.Pool, primary commitConsumerLoadFixture, binary, requestedSources string) {
	t.Helper()
	sourceCount, err := strconv.Atoi(requestedSources)
	if err != nil || sourceCount < 1 || sourceCount > 2 {
		t.Fatal("GREGALE_COMMIT_RECOVERY_PROFILE must be 1 or 2")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 17*time.Minute)
	defer cancel()
	sources := []commitConsumerLoadFixture{primary}
	if sourceCount == 2 {
		sources = append(sources, addCommitProfileSource(t, ctx, f.store, primary, "secondary", 0))
	}
	var relayTrace *commitRelayTrace
	if os.Getenv("GREGALE_COMMIT_RELAY_TRACE") == "1" {
		relayTrace = startCommitRelayTrace(t, ctx, f, sources)
	}
	recordCommitProfileClaims(t, ctx, f.h.Pool)

	prometheus := startCommitPrometheus(t, ctx, binary, f.h.ScheddMetricsURL)
	known := commitPrometheusObservation(sourceCount, 0)
	unknown := commitPrometheusObservation(sourceCount, sourceCount) + " and on() (schedd_commit_relay_oldest_pending_timestamp_seconds == 0)"
	waitCommitPrometheus(t, ctx, prometheus, time.Minute, "inactive", known)
	baselineEvents := 2
	if relayTrace != nil && sourceCount == 1 {
		baselineEvents += relayTrace.shortOutages(t, ctx, f, consumer, primary)
		waitCommitPrometheus(t, ctx, prometheus, time.Minute, "inactive", known)
	}
	outageStartedAt := time.Now()
	var attemptsBefore int64
	if relayTrace != nil {
		attemptsBefore = relayTrace.proxy.RejectedConnections()
		relayTrace.proxy.SetAvailable(false)
	} else {
		if _, err := primary.customer.Exec(ctx, `ALTER ROLE relay NOLOGIN`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if _, err := primary.customer.Exec(cleanupCtx, `ALTER ROLE relay LOGIN`); err != nil {
				t.Error(err)
			}
		})
		if _, err := primary.customer.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename='relay' AND pid<>pg_backend_pid()`); err != nil {
			t.Fatal(err)
		}
	}
	outage := startCommitProfileProducer(ctx, sources, 0, commitProfileOutageEvents, commitProfileEventInterval)
	t.Logf("Commit recovery profile outage started with %d sources", sourceCount)
	waitCommitPrometheus(t, ctx, prometheus, 6*time.Minute, "firing", unknown)
	firedAt := time.Now()
	outageEvents := awaitCommitProfileProducer(t, ctx, outage)
	if relayTrace != nil {
		// Hold the same prolonged outage in both comparisons. This crosses
		// the original backoff's first five-minute retry window.
		waitCommitRelayTraceUntil(t, ctx, outageStartedAt.Add(6*time.Minute+15*time.Second))
	}
	for _, source := range sources {
		health := waitCommitSourceHealth(t, f.h, f.key, source.source.ID, "database_unavailable", outageStartedAt)
		if health.PendingEvents != nil || health.BlockedEvents != nil || health.OldestPendingAt != nil {
			t.Fatal("unavailable source reported a known backlog")
		}
	}
	var restoredAt time.Time
	if err := primary.customer.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&restoredAt); err != nil {
		t.Fatal(err)
	}
	if relayTrace != nil {
		relayTrace.proxy.SetAvailable(true)
	} else if _, err := primary.customer.Exec(ctx, `ALTER ROLE relay LOGIN`); err != nil {
		t.Fatal(err)
	}
	t.Logf("Commit recovery profile restored access with %d queued events", len(outageEvents))
	recovery := startCommitProfileProducer(ctx, sources, commitProfileOutageEvents, commitProfileRecoveryEvents, commitProfileEventInterval)
	waitCommitPrometheus(t, ctx, prometheus, 6*time.Minute, "inactive", known)
	clearedAt := time.Now()
	events := append(outageEvents, awaitCommitProfileProducer(t, ctx, recovery)...)
	waitCommitProfileDrain(t, ctx, f, consumer, sources, len(events)+baselineEvents)
	waitCommitPrometheus(t, ctx, prometheus, time.Minute, "inactive", known+`
		and on() (schedd_commit_relay_pending_events == 0)
		and on() (schedd_commit_relay_blocked_events == 0)`)

	if relayTrace != nil {
		relayTrace.report(t, ctx, f, "long", sources, outageEvents, outageStartedAt, restoredAt, attemptsBefore)
	}
	timings := readCommitProfileTimings(t, ctx, f.h.Pool, consumer, sources, events, restoredAt)
	result := struct {
		Sources          int   `json:"sources"`
		OutageEvents     int   `json:"outage_events"`
		IntervalMS       int64 `json:"producer_interval_ms"`
		OutageToFiringMS int64 `json:"outage_to_alerts_firing_ms"`
		RestoreToClearMS int64 `json:"restore_to_alerts_clear_ms"`
	}{
		Sources: sourceCount, OutageEvents: len(outageEvents), IntervalMS: commitProfileEventInterval.Milliseconds(),
		OutageToFiringMS: firedAt.Sub(outageStartedAt).Milliseconds(),
		RestoreToClearMS: clearedAt.Sub(restoredAt).Milliseconds(),
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Commit recovery profile result %s", data)
	// Keep each record on its own line so go test -json emits it intact.
	for _, timing := range timings {
		data, err := json.Marshal(timing)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("Commit recovery profile event %s", data)
	}
}

func recordCommitProfileClaims(t *testing.T, ctx context.Context, catalog *pgxpool.Pool) {
	t.Helper()
	// Capture each real ownership transition in the disposable platform DB.
	if _, err := catalog.Exec(ctx, `
		CREATE TABLE commit_profile_claims(operation_id uuid NOT NULL, attempt int NOT NULL, started_at timestamptz NOT NULL,
			PRIMARY KEY(operation_id, attempt));
		CREATE FUNCTION commit_profile_claim() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			INSERT INTO commit_profile_claims VALUES (NEW.id, NEW.attempts, clock_timestamp());
			RETURN NEW;
		END $$;
		CREATE TRIGGER commit_profile_claim AFTER UPDATE OF state ON exclusive_work_operations
		FOR EACH ROW WHEN (OLD.state IS DISTINCT FROM NEW.state AND NEW.state = 'running')
		EXECUTE FUNCTION commit_profile_claim()`); err != nil {
		t.Fatal(err)
	}
}

func addCommitProfileSource(t *testing.T, ctx context.Context, store *state.PgStore, primary commitConsumerLoadFixture, name string, port uint16) commitConsumerLoadFixture {
	t.Helper()
	database := "commit_profile_" + name
	if _, err := primary.customer.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{database}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	cfg := primary.customer.Config()
	cfg.ConnConfig.Database = database
	customer, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(customer.Close)
	if _, err := customer.Exec(ctx, commitwork.Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := customer.Exec(ctx, `CREATE TABLE public.orders(id uuid PRIMARY KEY);
		GRANT SELECT,INSERT,UPDATE,DELETE ON public.gregale_outbox TO relay;
		GRANT SELECT ON public.gregale_commit_binding TO relay`); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateCommitSource(ctx, state.CommitSource{
		AccountID: primary.source.AccountID, AppID: primary.source.AppID,
		Name: "profile-" + name, OperationPolicy: primary.source.OperationPolicy,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := store.SetCommitSourceEnabled(cleanupCtx, source.AccountID, source.ID, false); err != nil {
			t.Error(err)
		}
	})
	if _, err := customer.Exec(ctx, `INSERT INTO public.gregale_commit_binding(source_id) VALUES($1::uuid)`, source.ID); err != nil {
		t.Fatal(err)
	}
	sealed, err := primary.sealRelay(source.ID, database, port)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetCommitSourceConnection(ctx, source.AccountID, source.ID, sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetCommitSourceEnabled(ctx, source.AccountID, source.ID, true); err != nil {
		t.Fatal(err)
	}
	return commitConsumerLoadFixture{customer: customer, source: source}
}

type commitProfileProduction struct {
	events []commitProfileEvent
	err    error
}

func startCommitProfileProducer(ctx context.Context, sources []commitConsumerLoadFixture, offset, count int, interval time.Duration) <-chan commitProfileProduction {
	done := make(chan commitProfileProduction, 1)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		events := make([]commitProfileEvent, 0, count)
		for i := range count {
			select {
			case <-ctx.Done():
				done <- commitProfileProduction{err: ctx.Err()}
				return
			case <-ticker.C:
			}
			source := (offset + i) % len(sources)
			id, err := commitConsumerOrder(ctx, sources[source].customer)
			if err != nil {
				done <- commitProfileProduction{err: err}
				return
			}
			events = append(events, commitProfileEvent{id: id, source: source})
		}
		done <- commitProfileProduction{events: events}
	}()
	return done
}

func awaitCommitProfileProducer(t *testing.T, ctx context.Context, done <-chan commitProfileProduction) []commitProfileEvent {
	t.Helper()
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		return result.events
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return nil
	}
}

func waitCommitProfileDrain(t *testing.T, ctx context.Context, f *normalPathFixture, consumer *pgxpool.Pool, sources []commitConsumerLoadFixture, want int) {
	t.Helper()
	var receipts, completed, effects, markers, operations, unsuccessful int
	defer func() {
		if t.Failed() {
			t.Logf("profile drain counts: receipts=%d completed=%d unsuccessful=%d effects=%d markers=%d operations=%d want=%d",
				receipts, completed, unsuccessful, effects, markers, operations, want)
		}
	}()
	sourceIDs := make([]string, 0, len(sources))
	for _, source := range sources {
		sourceIDs = append(sourceIDs, source.source.ID)
	}
	waitCommitLoadCondition(t, ctx, "profile receipts, completed Operations, and business effects", func() (bool, error) {
		if err := f.h.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE operation_state='completed'),
			count(*) FILTER (WHERE operation_state IN ('failed','cancelled','expired')),
			(SELECT count(*) FROM exclusive_work_operations WHERE app_id=$2::uuid)
			FROM commit_receipts WHERE source_id::text=ANY($1)`, sourceIDs, f.app.ID).
			Scan(&receipts, &completed, &unsuccessful, &operations); err != nil {
			return false, err
		}
		if err := consumer.QueryRow(ctx, `SELECT (SELECT total FROM business_effects WHERE id=1),
			(SELECT count(*) FROM processed_events)`).Scan(&effects, &markers); err != nil {
			return false, err
		}
		if unsuccessful != 0 || receipts > want || effects > want || markers > want || operations > want {
			return false, fmt.Errorf("unexpected terminal result or duplicate profile work")
		}
		return completed == want && receipts == want && effects == want && markers == want && operations == want, nil
	})
}

func readCommitProfileTimings(t *testing.T, ctx context.Context, catalog, consumer *pgxpool.Pool, sources []commitConsumerLoadFixture, events []commitProfileEvent, restoredAt time.Time) []commitProfileTiming {
	t.Helper()
	lastSequence := make([]int64, len(sources))
	lastCompletion := make([]time.Time, len(sources))
	timings := make([]commitProfileTiming, 0, len(events))
	for i, event := range events {
		source := sources[event.source]
		var receipt, checkpointReceipt, operation, checkpointOperation string
		var producedAt, acceptedAt, checkpointAt, startedAt, completedAt time.Time
		var sequence int64
		var attempts, observedAttempts int
		if err := catalog.QueryRow(ctx, `SELECT r.id::text,r.operation_id::text,r.accepted_at,r.completed_at,o.sequence,o.attempts,
			(SELECT min(started_at) FROM commit_profile_claims WHERE operation_id=o.id),
			(SELECT count(*) FROM commit_profile_claims WHERE operation_id=o.id)
			FROM commit_receipts r JOIN exclusive_work_operations o ON o.id=r.operation_id
			WHERE r.source_id=$1::uuid AND r.event_id=$2::uuid AND o.state='completed'`,
			source.source.ID, event.id).Scan(&receipt, &operation, &acceptedAt, &completedAt, &sequence, &attempts, &startedAt, &observedAttempts); err != nil {
			t.Fatalf("event %d platform timing: %v", i+1, err)
		}
		if err := source.customer.QueryRow(ctx, `SELECT e.receipt_id::text,e.operation_id::text,e.accepted_at,e.created_at
			FROM public.gregale_outbox e JOIN public.orders o ON o.id=e.event_id WHERE e.event_id=$1::uuid`,
			event.id).Scan(&checkpointReceipt, &checkpointOperation, &checkpointAt, &producedAt); err != nil {
			t.Fatalf("event %d outbox checkpoint: %v", i+1, err)
		}
		var marker bool
		if err := consumer.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM processed_events WHERE source=$1 AND event_id=$2::uuid)`,
			"gregale.commit."+source.source.ID, event.id).Scan(&marker); err != nil || !marker {
			t.Fatalf("event %d missing consumer effect marker: %v", i+1, err)
		}
		if checkpointReceipt != receipt || checkpointOperation != operation || attempts < 1 || attempts != observedAttempts {
			t.Fatalf("event %d receipt identity or ownership attempt mismatch", i+1)
		}
		if acceptedAt.Before(restoredAt) || acceptedAt.Before(producedAt) || checkpointAt.Before(acceptedAt) || startedAt.Before(acceptedAt) || completedAt.Before(startedAt) {
			t.Fatalf("event %d timing mismatch: restored=%v receipt=%v checkpoint=%v claim=%v completion=%v",
				i+1, restoredAt, acceptedAt, checkpointAt, startedAt, completedAt)
		}
		if sequence <= lastSequence[event.source] || startedAt.Before(lastCompletion[event.source]) {
			t.Fatalf("event %d per-source ordering mismatch: sequence=%d previous=%d claim=%v previous completion=%v",
				i+1, sequence, lastSequence[event.source], startedAt, lastCompletion[event.source])
		}
		lastSequence[event.source], lastCompletion[event.source] = sequence, completedAt
		timings = append(timings, commitProfileTiming{
			Source: event.source + 1, ReceiptMS: acceptedAt.Sub(restoredAt).Milliseconds(),
			CheckpointMS: checkpointAt.Sub(restoredAt).Milliseconds(), StartedMS: startedAt.Sub(restoredAt).Milliseconds(),
			CompletedMS: completedAt.Sub(restoredAt).Milliseconds(), Attempts: attempts,
			producedAt: producedAt, receiptAt: acceptedAt,
		})
	}
	return timings
}
