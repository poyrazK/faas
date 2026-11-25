package e2e_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
)

type commitRelayTrace struct {
	proxy   postgresfixture.Proxy
	readLog func() string
}

func startCommitRelayTrace(t *testing.T, ctx context.Context, f *normalPathFixture, sources []commitConsumerLoadFixture) *commitRelayTrace {
	t.Helper()
	primary := sources[0]
	recordCommitRelayPolls(t, ctx, f.h.Pool, primary.customer)
	trace := &commitRelayTrace{
		proxy:   postgresfixture.OpenProxyContext(t, ctx, primary.customer, "127.0.0.1", 0),
		readLog: primary.relayLog,
	}
	for _, source := range sources {
		sealed, err := primary.sealRelay(source.source.ID, source.customer.Config().ConnConfig.Database, trace.proxy.Port)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.SetCommitSourceConnection(ctx, source.source.AccountID, source.source.ID, sealed); err != nil {
			t.Fatal(err)
		}
	}
	return trace
}

func recordCommitRelayPolls(t *testing.T, ctx context.Context, catalog, customer *pgxpool.Pool) {
	t.Helper()
	// Capture socket authentication in the producer's disposable PostgreSQL
	// cluster. The relay still uses its usual hostname and verified TLS.
	for _, sql := range []string{
		`ALTER SYSTEM SET log_connections = 'on'`,
		`ALTER SYSTEM SET log_timezone = 'UTC'`,
		`ALTER SYSTEM SET log_line_prefix = '%m '`,
		`SELECT pg_reload_conf()`,
	} {
		if _, err := customer.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := catalog.Exec(ctx, `
		CREATE TABLE commit_relay_trace_polls
			(token uuid PRIMARY KEY, source_id uuid NOT NULL, due_at timestamptz NOT NULL, claimed_at timestamptz NOT NULL,
			reserved boolean NOT NULL, prior_status text NOT NULL, prior_connect_ms integer NOT NULL, finished_at timestamptz, outcome text);
		CREATE INDEX ON commit_relay_trace_polls(source_id,claimed_at);
		CREATE FUNCTION commit_relay_trace_poll() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.relay_poll_token IS NOT NULL THEN
				INSERT INTO commit_relay_trace_polls(token,source_id,due_at,claimed_at,reserved,prior_status,prior_connect_ms)
				VALUES (NEW.relay_poll_token,NEW.id,OLD.relay_next_poll_at,clock_timestamp(),
					position('/* commit relay reserved poll */' in current_query()) > 0,OLD.relay_status,
					CASE WHEN OLD.relay_connect_observed_at=OLD.last_checked_at THEN COALESCE(OLD.relay_connect_ms,0) ELSE 0 END);
			ELSIF position('/* commit relay general handoff */' in current_query()) > 0 THEN
				UPDATE commit_relay_trace_polls SET finished_at=clock_timestamp(),outcome='reserved_connect_deferred'
				WHERE token=OLD.relay_poll_token;
			ELSIF NEW.last_checked_at IS NOT NULL AND NEW.last_checked_at IS DISTINCT FROM OLD.last_checked_at THEN
				UPDATE commit_relay_trace_polls SET finished_at=clock_timestamp(),outcome=NEW.relay_status
				WHERE token=OLD.relay_poll_token;
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER commit_relay_trace_poll AFTER UPDATE OF relay_poll_token ON commit_sources
		FOR EACH ROW WHEN (OLD.relay_poll_token IS DISTINCT FROM NEW.relay_poll_token)
		EXECUTE FUNCTION commit_relay_trace_poll()`); err != nil {
		t.Fatal(err)
	}
}

func (r *commitRelayTrace) shortOutages(t *testing.T, ctx context.Context, f *normalPathFixture, consumer *pgxpool.Pool, primary commitConsumerLoadFixture) int {
	t.Helper()
	sources := []commitConsumerLoadFixture{primary}
	for i, phase := range []string{"short-early", "short-late"} {
		outageStartedAt := time.Now()
		attemptsBefore := r.proxy.RejectedConnections()
		r.proxy.SetAvailable(false)
		id, err := commitConsumerOrder(ctx, primary.customer)
		if err != nil {
			t.Fatal(err)
		}
		waitCommitSourceHealth(t, ctx, f.h, f.key, primary.source.ID, "database_unavailable", outageStartedAt)
		var failedAt, nextPoll time.Time
		if err := f.h.Pool.QueryRow(ctx, `SELECT last_checked_at, relay_next_poll_at FROM commit_sources WHERE id=$1::uuid`,
			primary.source.ID).Scan(&failedAt, &nextPoll); err != nil {
			t.Fatal(err)
		}
		restore := failedAt.Add(time.Second)
		if phase == "short-late" {
			restore = nextPoll.Add(-time.Second)
		}
		waitCommitRelayTraceUntil(t, ctx, restore)
		restoredAt := time.Now()
		r.proxy.SetAvailable(true)
		waitCommitProfileDrain(t, ctx, f, consumer, sources, i+3)
		waitCommitSourceHealth(t, ctx, f.h, f.key, primary.source.ID, "healthy", restoredAt)
		r.report(t, ctx, f, phase, sources, []commitProfileEvent{{id: id, source: 0}}, outageStartedAt, restoredAt, attemptsBefore)
	}
	return 2
}

func waitCommitRelayTraceUntil(t *testing.T, ctx context.Context, at time.Time) {
	t.Helper()
	wait := time.NewTimer(time.Until(at))
	defer wait.Stop()
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-wait.C:
	}
}

func (r *commitRelayTrace) report(t *testing.T, ctx context.Context, f *normalPathFixture, phase string, sources []commitConsumerLoadFixture, events []commitProfileEvent, outageStartedAt, restoredAt time.Time, attemptsBefore int64) {
	t.Helper()
	for i, source := range sources {
		var firstEvent string
		for _, event := range events {
			if event.source == i {
				firstEvent = event.id
				break
			}
		}
		receipt, err := f.store.CommitReceiptByEvent(ctx, source.source.AccountID, source.source.ID, firstEvent)
		if err != nil {
			t.Fatal(err)
		}
		var dueAt, claimAt time.Time
		var failedPolls int
		if err := f.h.Pool.QueryRow(ctx, `SELECT due_at, claimed_at FROM commit_relay_trace_polls
			WHERE source_id=$1::uuid AND claimed_at<=$2 ORDER BY claimed_at DESC LIMIT 1`,
			source.source.ID, receipt.AcceptedAt).Scan(&dueAt, &claimAt); err != nil {
			t.Fatal(err)
		}
		if err := f.h.Pool.QueryRow(ctx, `SELECT count(*) FROM commit_relay_trace_polls
			WHERE source_id=$1::uuid AND claimed_at>=$2 AND claimed_at<$3`,
			source.source.ID, outageStartedAt, restoredAt).Scan(&failedPolls); err != nil {
			t.Fatal(err)
		}
		connectedAt := r.firstConnection(source.customer.Config().ConnConfig.Database, claimAt)
		// PostgreSQL's log rounds to milliseconds. Allow the log timestamp
		// to precede a platform timestamp within the same millisecond.
		if connectedAt.IsZero() || connectedAt.Before(claimAt.Truncate(time.Millisecond)) ||
			receipt.AcceptedAt.Before(connectedAt) || claimAt.Before(restoredAt) || failedPolls == 0 {
			t.Fatalf("%s source %d missing ordered TLS recovery evidence: restored=%v claim=%v TLS=%v receipt=%v failed_polls=%d",
				phase, i+1, restoredAt, claimAt, connectedAt, receipt.AcceptedAt, failedPolls)
		}
		result := struct {
			Phase              string `json:"phase"`
			Source             int    `json:"source"`
			OutageMS           int64  `json:"outage_ms"`
			RestoreToDueMS     int64  `json:"restore_to_due_ms"`
			RestoreToClaimMS   int64  `json:"restore_to_claim_ms"`
			ClaimToTLSMS       int64  `json:"claim_to_tls_ms"`
			TLSToReceiptMS     int64  `json:"tls_to_receipt_ms"`
			RestoreToReceiptMS int64  `json:"restore_to_receipt_ms"`
			FailedPolls        int    `json:"outage_polls"`
			RefusedConnections int64  `json:"outage_refused_connections_total"`
		}{
			Phase: phase, Source: i + 1, OutageMS: restoredAt.Sub(outageStartedAt).Milliseconds(),
			RestoreToDueMS: dueAt.Sub(restoredAt).Milliseconds(), RestoreToClaimMS: claimAt.Sub(restoredAt).Milliseconds(),
			ClaimToTLSMS: connectedAt.Sub(claimAt).Milliseconds(), TLSToReceiptMS: receipt.AcceptedAt.Sub(connectedAt).Milliseconds(),
			RestoreToReceiptMS: receipt.AcceptedAt.Sub(restoredAt).Milliseconds(),
			FailedPolls:        failedPolls, RefusedConnections: r.proxy.RejectedConnections() - attemptsBefore,
		}
		body, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("Commit relay recovery trace %s", body)
	}
}

func (r *commitRelayTrace) firstConnection(database string, after time.Time) time.Time {
	return firstCommitRelayTLSConnection(commitRelayTLSConnections(r.readLog())[database], after)
}

func commitRelayTLSConnections(log string) map[string][]time.Time {
	const layout = "2006-01-02 15:04:05.000 UTC"
	connections := make(map[string][]time.Time)
	for line := range strings.SplitSeq(log, "\n") {
		_, session, ok := strings.Cut(line, "connection authorized: user=relay database=")
		if !ok || len(line) < len(layout) {
			continue
		}
		database, _, ok := strings.Cut(session, " application_name=gregale-commit SSL enabled")
		if !ok {
			continue
		}
		connectedAt, err := time.Parse(layout, line[:len(layout)])
		if err == nil {
			connections[database] = append(connections[database], connectedAt)
		}
	}
	for _, times := range connections {
		slices.SortFunc(times, time.Time.Compare)
	}
	return connections
}

func firstCommitRelayTLSConnection(times []time.Time, after time.Time) time.Time {
	i, _ := slices.BinarySearchFunc(times, after.Truncate(time.Millisecond), time.Time.Compare)
	if i == len(times) {
		return time.Time{}
	}
	return times[i]
}
