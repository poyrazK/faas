package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
)

type commitFleetTLSControl struct {
	source commitConsumerLoadFixture
	proxy  postgresfixture.Proxy
	delay  time.Duration
}

const commitFleetTLSCooldown = 30 * time.Second

type commitFleetTLSResult struct {
	Source           int                `json:"source"`
	TLSDelayMS       int64              `json:"tls_delay_ms"`
	Receipt          commitFleetLatency `json:"receipt_during_outage"`
	Attempts         int                `json:"tcp_attempts_during_outage"`
	HealthyPolls     int                `json:"healthy_polls"`
	ReservedHealthy  int                `json:"reserved_healthy_polls"`
	Handoffs         int                `json:"handoffs"`
	GeneralClaims    int                `json:"handoffs_claimed_by_general"`
	UnavailablePolls int                `json:"database_unavailable_polls"`
}

func reportCommitFleetSlowTLS(t *testing.T, ctx context.Context, catalog *pgxpool.Pool, controls []commitFleetTLSControl,
	events []commitProfileEvent, timings []commitProfileTiming, start, restore time.Time, fastConnect, cooldown, adaptive bool) {
	t.Helper()
	results := make([]commitFleetTLSResult, 0, len(controls))
	for i, control := range controls {
		var during []commitProfileTiming
		for j, event := range events {
			if event.source == i && timings[j].producedAt.Before(restore) {
				during = append(during, timings[j])
			}
		}
		if len(during) == 0 {
			t.Fatalf("missing receipts for TLS control %d", i+1)
		}
		result := commitFleetTLSResult{Source: i + 1, TLSDelayMS: control.delay.Milliseconds(), Receipt: commitFleetLatencies(during)}
		result.Attempts, _ = commitFleetAttemptRate(control.proxy.ConnectionAttempts(), start, restore)
		if err := catalog.QueryRow(ctx, `SELECT
			count(*) FILTER(WHERE p.outcome='healthy'),
			count(*) FILTER(WHERE p.outcome='healthy' AND p.reserved),
			count(*) FILTER(WHERE p.outcome='reserved_connect_deferred'),
			count(*) FILTER(WHERE p.outcome='reserved_connect_deferred' AND NOT next_poll.reserved AND next_poll.due_at=p.due_at),
			count(*) FILTER(WHERE p.outcome='database_unavailable')
			FROM commit_relay_trace_polls p
			LEFT JOIN LATERAL (
				SELECT reserved,due_at FROM commit_relay_trace_polls
				WHERE p.outcome='reserved_connect_deferred' AND source_id=p.source_id AND claimed_at>=p.finished_at
				ORDER BY claimed_at LIMIT 1
			) next_poll ON true
			WHERE p.source_id=$1::uuid AND p.claimed_at>=$2 AND p.claimed_at<$3`,
			control.source.source.ID, start, restore).Scan(&result.HealthyPolls, &result.ReservedHealthy,
			&result.Handoffs, &result.GeneralClaims, &result.UnavailablePolls); err != nil {
			t.Fatal(err)
		}
		if result.UnavailablePolls != 0 || result.Handoffs != result.GeneralClaims || (!fastConnect && result.Handoffs != 0) {
			t.Fatalf("TLS control %d lost health or changed its due time: %+v", i+1, result)
		}
		if fastConnect && !adaptive && control.delay == 1250*time.Millisecond && result.Handoffs < 2 {
			t.Fatalf("TLS control %d did not exercise repeated handoffs: %+v", i+1, result)
		}
		if adaptive && control.delay == 1250*time.Millisecond && result.ReservedHealthy == 0 {
			t.Fatalf("adaptive reserved worker did not complete the slow reachable source: %+v", result)
		}
		results = append(results, result)
	}
	var cooldownMS int64
	if cooldown {
		cooldownMS = commitFleetTLSCooldown.Milliseconds()
	}
	logCommitFleetSlowTLS(t, struct {
		FastConnect bool                   `json:"fast_reserved_connect"`
		CooldownMS  int64                  `json:"fixture_cooldown_ms,omitempty"`
		Controls    []commitFleetTLSResult `json:"controls"`
	}{fastConnect, cooldownMS, results})
}

func recordCommitSlowTLSCooldown(t *testing.T, ctx context.Context, catalog *pgxpool.Pool) {
	t.Helper()
	// Preserve the existing reservation exclusion across healthy general
	// polls until the fixture's cooldown expires. Both schedulers see it
	// through the shared platform database.
	if _, err := catalog.Exec(ctx, `
		CREATE TABLE commit_slow_tls_cooldowns(source_id uuid PRIMARY KEY,retry_at timestamptz NOT NULL);
		CREATE FUNCTION commit_slow_tls_cooldown() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF position('/* commit relay general handoff */' in current_query()) > 0 THEN
				INSERT INTO commit_slow_tls_cooldowns VALUES (NEW.id,statement_timestamp()+interval '30 seconds')
				ON CONFLICT (source_id) DO UPDATE SET retry_at=EXCLUDED.retry_at;
			ELSIF NEW.relay_status='healthy' AND NEW.last_checked_at IS DISTINCT FROM OLD.last_checked_at
			  AND EXISTS(SELECT 1 FROM commit_slow_tls_cooldowns WHERE source_id=NEW.id AND retry_at>statement_timestamp()) THEN
				NEW.relay_reserved_deferred_check_at=NEW.last_checked_at;
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER commit_slow_tls_cooldown BEFORE UPDATE OF relay_poll_token ON commit_sources
		FOR EACH ROW WHEN (OLD.relay_poll_token IS NOT NULL AND NEW.relay_poll_token IS NULL)
		EXECUTE FUNCTION commit_slow_tls_cooldown()`); err != nil {
		t.Fatal(err)
	}
}

func verifyCommitSlowTLSRestart(t *testing.T, ctx context.Context, f *normalPathFixture, consumer *pgxpool.Pool,
	all []commitConsumerLoadFixture, slow commitConsumerLoadFixture, expectedEvents int) {
	t.Helper()
	catalog := f.h.Pool
	id := slow.source.ID
	waitCommitLoadCondition(t, ctx, "healthy observation of the slow source", func() (bool, error) {
		var healthy bool
		err := catalog.QueryRow(ctx, `SELECT relay_status='healthy' FROM commit_sources WHERE id=$1::uuid`, id).Scan(&healthy)
		return healthy, err
	})
	// Hold the selected source's general claim until the replacement scheduler
	// is listening, so the test restarts the only scheduler with a real handoff
	// still pending in PostgreSQL. No lease is fabricated or advanced.
	if _, err := catalog.Exec(ctx, `
		CREATE TABLE commit_restart_slow_source(source_id uuid PRIMARY KEY);
		CREATE FUNCTION commit_hold_slow_general_claim() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF position('/* commit relay reserved poll */' in current_query())=0
			   AND EXISTS(SELECT 1 FROM commit_restart_slow_source WHERE source_id=NEW.id) THEN
				RETURN NULL;
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER commit_hold_slow_general_claim BEFORE UPDATE OF relay_poll_token ON commit_sources
		FOR EACH ROW WHEN (NEW.relay_poll_token IS NOT NULL AND OLD.relay_poll_token IS DISTINCT FROM NEW.relay_poll_token)
		EXECUTE FUNCTION commit_hold_slow_general_claim()`); err != nil {
		t.Fatal(err)
	}
	const dropGate = `DROP TRIGGER IF EXISTS commit_hold_slow_general_claim ON commit_sources;
		DROP FUNCTION IF EXISTS commit_hold_slow_general_claim();
		DROP TABLE IF EXISTS commit_restart_slow_source`
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := catalog.Exec(cleanup, dropGate); err != nil {
			t.Error(err)
		}
	})
	if _, err := catalog.Exec(ctx, `INSERT INTO commit_restart_slow_source VALUES($1::uuid)`, id); err != nil {
		t.Fatal(err)
	}
	var due, deferredAt, lastChecked time.Time
	waitCommitLoadCondition(t, ctx, "durably deferred poll before scheduler restart", func() (bool, error) {
		err := catalog.QueryRow(ctx, `SELECT p.due_at,p.finished_at,s.last_checked_at
			FROM commit_sources s JOIN commit_relay_trace_polls p ON p.source_id=s.id AND p.due_at=s.relay_next_poll_at
			WHERE s.id=$1::uuid AND s.relay_poll_token IS NULL AND s.relay_status='healthy'
			AND s.relay_reserved_deferred_check_at=s.last_checked_at AND p.outcome='reserved_connect_deferred'
			ORDER BY p.finished_at DESC LIMIT 1`, id).Scan(&due, &deferredAt, &lastChecked)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return err == nil, err
	})
	produced := time.Now()
	eventID, err := commitConsumerOrder(ctx, slow.customer)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.h.RestartScheddContext(ctx); err != nil {
		t.Fatal(err)
	}
	restarted := time.Now()
	var held bool
	if err := catalog.QueryRow(ctx, `SELECT relay_poll_token IS NULL AND relay_next_poll_at=$2
		AND relay_status='healthy' AND last_checked_at=$3 AND relay_reserved_deferred_check_at=$3
		FROM commit_sources WHERE id=$1::uuid`, id, due, lastChecked).Scan(&held); err != nil || !held {
		t.Fatalf("scheduler restart changed the pending handoff: %t %v", held, err)
	}
	if _, err := catalog.Exec(ctx, dropGate); err != nil {
		t.Fatal(err)
	}
	waitCommitProfileDrain(t, ctx, f, consumer, all, expectedEvents)
	timed := readCommitProfileTimings(t, ctx, catalog, consumer, []commitConsumerLoadFixture{slow}, []commitProfileEvent{{id: eventID}}, produced)[0]
	var claimDue, claimed time.Time
	var reserved bool
	if err := catalog.QueryRow(ctx, `SELECT due_at,claimed_at,reserved FROM commit_relay_trace_polls
		WHERE source_id=$1::uuid AND claimed_at>=$2 AND claimed_at<=$3 ORDER BY claimed_at LIMIT 1`,
		id, deferredAt, timed.receiptAt).Scan(&claimDue, &claimed, &reserved); err != nil {
		t.Fatal(err)
	}
	if reserved || !claimDue.Equal(due) || claimed.Before(restarted) {
		t.Fatalf("first claim after restart did not preserve the general handoff: due=%v claim=%v reserved=%t", claimDue, claimed, reserved)
	}
	logCommitFleetSlowTLS(t, struct {
		DuePreserved     bool  `json:"restart_due_preserved"`
		RestartToClaimMS int64 `json:"restart_to_general_claim_ms"`
		ClaimToReceiptMS int64 `json:"general_claim_to_receipt_ms"`
	}{true, claimed.Sub(restarted).Milliseconds(), timed.receiptAt.Sub(claimed).Milliseconds()})
}

func logCommitFleetSlowTLS(t *testing.T, record any) {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Commit fleet slow TLS %s", data)
}
