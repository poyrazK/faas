package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type commitFleetPollAnchor struct {
	checked, due time.Time
}

func waitCommitFleetFaultStart(t *testing.T, ctx context.Context, catalog *pgxpool.Pool, sourceID, phase string) commitFleetPollAnchor {
	t.Helper()
	var anchor commitFleetPollAnchor
	var target time.Time
	waitCommitLoadCondition(t, ctx, "reference source poll for "+phase, func() (bool, error) {
		err := catalog.QueryRow(ctx, `SELECT last_checked_at, relay_next_poll_at FROM commit_sources
			WHERE id=$1::uuid AND relay_status='healthy' AND relay_poll_token IS NULL AND last_checked_at IS NOT NULL`,
			sourceID).Scan(&anchor.checked, &anchor.due)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		target = anchor.checked.Add(time.Second)
		if phase == "timeout-late" {
			target = anchor.due.Add(-time.Second)
		}
		return time.Until(target) >= 100*time.Millisecond, nil
	})
	waitCommitRelayTraceUntil(t, ctx, target)
	return anchor
}

type commitFleetControlReceipt struct {
	Source    int   `json:"source"`
	CommitMS  int64 `json:"commit_ms"`
	DueMS     int64 `json:"due_ms"`
	ClaimMS   int64 `json:"claim_ms"`
	TLSMS     int64 `json:"tls_ms"`
	ReceiptMS int64 `json:"receipt_ms"`
	Reserved  bool  `json:"reserved"`
}

type commitFleetReservedTimeout struct {
	Source         int    `json:"source"`
	ClaimMS        int64  `json:"claim_ms"`
	FinishedMS     int64  `json:"finished_ms"`
	PriorStatus    string `json:"prior_status"`
	Outcome        string `json:"outcome"`
	PriorConnectMS int    `json:"prior_connect_ms,omitempty"`
}

func reportCommitFleetTail(t *testing.T, ctx context.Context, catalog *pgxpool.Pool, readLog func() string, phase string,
	anchor commitFleetPollAnchor, affected, controls []commitConsumerLoadFixture, events []commitProfileEvent,
	timings []commitProfileTiming, start, restore time.Time) {
	t.Helper()
	relative := func(at time.Time) int64 { return at.Sub(start).Milliseconds() }
	connections := commitRelayTLSConnections(readLog())
	receipts := make([]commitFleetControlReceipt, 0, len(events))
	for i, event := range events {
		control := controls[event.source]
		var committed, due, claimed time.Time
		var reserved bool
		if err := control.customer.QueryRow(ctx, `SELECT pg_xact_commit_timestamp(xmin) FROM public.orders WHERE id=$1::uuid`,
			event.id).Scan(&committed); err != nil {
			t.Fatalf("healthy event %d commit timestamp: %v", i+1, err)
		}
		receipt := timings[i].receiptAt
		if err := catalog.QueryRow(ctx, `SELECT due_at,claimed_at,reserved FROM commit_relay_trace_polls
			WHERE source_id=$1::uuid AND claimed_at<=$2 ORDER BY claimed_at DESC LIMIT 1`,
			control.source.ID, receipt).Scan(&due, &claimed, &reserved); err != nil {
			t.Fatalf("healthy event %d poll: %v", i+1, err)
		}
		tls := firstCommitRelayTLSConnection(connections[control.customer.Config().ConnConfig.Database], claimed)
		if tls.IsZero() || claimed.Before(due) || receipt.Before(tls) || receipt.Before(committed) {
			t.Fatalf("healthy event %d invalid relay times: commit=%v due=%v claim=%v TLS=%v receipt=%v",
				i+1, committed, due, claimed, tls, receipt)
		}
		receipts = append(receipts, commitFleetControlReceipt{
			Source: event.source + 1, CommitMS: relative(committed), DueMS: relative(due),
			ClaimMS: relative(claimed), TLSMS: relative(tls), ReceiptMS: relative(receipt), Reserved: reserved,
		})
	}

	var reservedPolls int
	if err := catalog.QueryRow(ctx, `SELECT count(*) FROM commit_relay_trace_polls WHERE reserved AND claimed_at>=$1 AND claimed_at<$2`,
		start, restore).Scan(&reservedPolls); err != nil {
		t.Fatal(err)
	}
	if (reservedPolls > 0) != (os.Getenv("FAAS_COMMIT_RELAY_RESERVE_HEALTHY_WORKER") == "true") {
		t.Fatalf("reserved worker attribution disagrees with the scheduler setting: %d claims", reservedPolls)
	}
	affectedIDs := make([]string, len(affected))
	ordinals := make(map[string]int, len(affected))
	for i, source := range affected {
		affectedIDs[i] = source.source.ID
		ordinals[source.source.ID] = i + 1
	}
	rows, err := catalog.Query(ctx, `SELECT source_id::text,claimed_at,finished_at,prior_status,outcome,prior_connect_ms
		FROM commit_relay_trace_polls WHERE source_id::text=ANY($1) AND reserved
		AND outcome IN ('database_unavailable','reserved_connect_deferred')
		AND finished_at>$2 AND claimed_at<$3 ORDER BY claimed_at`, affectedIDs, start, restore)
	if err != nil {
		t.Fatal(err)
	}
	polls, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (commitFleetReservedTimeout, error) {
		var source string
		var claimed, finished time.Time
		var status, outcome string
		var priorConnectMS int
		err := row.Scan(&source, &claimed, &finished, &status, &outcome, &priorConnectMS)
		return commitFleetReservedTimeout{
			Source: ordinals[source], ClaimMS: relative(claimed), FinishedMS: relative(finished), PriorStatus: status, Outcome: outcome, PriorConnectMS: priorConnectMS,
		}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	var handoffs, generalClaims int
	if err := catalog.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE NOT next.reserved AND next.due_at=p.due_at)
		FROM commit_relay_trace_polls p LEFT JOIN LATERAL (
			SELECT reserved,due_at FROM commit_relay_trace_polls
			WHERE source_id=p.source_id AND claimed_at>=p.finished_at ORDER BY claimed_at LIMIT 1
		) next ON true
		WHERE p.source_id::text=ANY($1) AND p.outcome='reserved_connect_deferred' AND p.finished_at>$2 AND p.claimed_at<$3`,
		affectedIDs, start, restore).Scan(&handoffs, &generalClaims); err != nil {
		t.Fatal(err)
	}
	if handoffs != generalClaims || (handoffs > 0) != (os.Getenv("FAAS_COMMIT_RELAY_FAST_RESERVED_CONNECT") == "true") {
		t.Fatalf("expected each deferred source to return to general polling at its original due time: %d handoffs, %d matching claims", handoffs, generalClaims)
	}
	report, err := json.Marshal(struct {
		Phase              string                       `json:"phase"`
		RestoreMS          int64                        `json:"restore_ms"`
		ReferenceCheckedMS int64                        `json:"reference_checked_ms"`
		ReferenceDueMS     int64                        `json:"reference_due_ms"`
		ReservedPolls      int                          `json:"reserved_polls_during_outage"`
		ControlReceipts    []commitFleetControlReceipt  `json:"control_receipts"`
		ReservedTimeouts   []commitFleetReservedTimeout `json:"reserved_timeouts"`
		GeneralHandoffs    int                          `json:"general_handoffs"`
	}{phase, relative(restore), relative(anchor.checked), relative(anchor.due), reservedPolls, receipts, polls, handoffs})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Commit fleet tail trace %s", report)
}
