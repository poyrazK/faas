package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CommitRelayObservation is an aggregate of the shared durable source health.
// Unknown sources are excluded from backlog totals, not counted as empty.
type CommitRelayObservation struct {
	EnabledSources         int64
	UnknownSources         int64
	FailingSources         int64
	PendingEvents          int64
	BlockedEvents          int64
	OldestPendingTimestamp float64
	CleanupUnknownSources  int64
	CleanupEligibleEvents  int64
}

func (s *PgStore) CommitRelayObservationSummary(ctx context.Context, freshAfter time.Time) (CommitRelayObservation, error) {
	row, err := sqlc.New().CommitRelayObservationSummary(ctx, s.pool, pgtype.Timestamptz{Time: freshAfter, Valid: true})
	return CommitRelayObservation{
		EnabledSources: row.EnabledSources, UnknownSources: row.UnknownSources, FailingSources: row.FailingSources,
		PendingEvents: row.PendingEvents, BlockedEvents: row.BlockedEvents, OldestPendingTimestamp: row.OldestPendingTimestamp,
		CleanupUnknownSources: row.CleanupUnknownSources, CleanupEligibleEvents: row.CleanupEligibleEvents,
	}, err
}

type CommitReceiptUsage struct {
	Count, PayloadJSONBytes        int64
	InitializedShards, TotalShards int64
	OldestReconciledAt             *time.Time
}

// CommitReceiptStorage reads the small usage ledger. Totals are authoritative
// after all 256 shards have been initialized.
func (s *PgStore) CommitReceiptStorage(ctx context.Context) (CommitReceiptUsage, error) {
	var result CommitReceiptUsage
	err := s.pool.QueryRow(ctx, `SELECT coalesce(sum(receipt_count),0)::bigint,coalesce(sum(payload_json_bytes),0)::bigint,
 count(*) FILTER(WHERE initialized)::bigint,count(*)::bigint,
 CASE WHEN count(*) FILTER(WHERE initialized AND reconciled_at IS NULL)=0
 THEN min(reconciled_at) FILTER(WHERE initialized) END
 FROM commit_receipt_usage`).Scan(&result.Count, &result.PayloadJSONBytes, &result.InitializedShards, &result.TotalShards, &result.OldestReconciledAt)
	return result, err
}

// ReconcileCommitReceiptStorage processes at most one bounded UUID range batch.
// A durable claim delays retry after interruption; the cursor and partial totals
// commit atomically with each batch. Receipt triggers update the scanned prefix
// under the same shard lock. READ COMMITTED is required for the batch snapshot.
func (s *PgStore) ReconcileCommitReceiptStorage(ctx context.Context) (bool, error) {
	const batchSize = 1000
	var shard int16
	var token string
	err := s.pool.QueryRow(ctx, `UPDATE commit_receipt_usage SET
 reconcile_token=gen_random_uuid(), retry_at=clock_timestamp()+interval '1 minute'
 WHERE shard=(SELECT shard FROM commit_receipt_usage
  WHERE (NOT initialized OR reconciled_at IS NULL OR reconciled_at < now()-interval '24 hours')
    AND retry_at<=clock_timestamp()
  ORDER BY retry_at,initialized,reconciled_at NULLS FIRST,shard
  LIMIT 1 FOR UPDATE SKIP LOCKED)
 RETURNING shard,reconcile_token::text`).Scan(&shard, &token)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var cursor *string
	var active bool
	var scannedCount, scannedBytes int64
	err = tx.QueryRow(ctx, `SELECT scan_in_progress,scan_cursor::text,scan_count,scan_payload_json_bytes
 FROM commit_receipt_usage WHERE shard=$1 AND reconcile_token=$2::uuid FOR UPDATE`, shard, token).
		Scan(&active, &cursor, &scannedCount, &scannedBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	first := fmt.Sprintf("%02x%s", shard, strings.Repeat("0", 30))
	last := fmt.Sprintf("%02x%s", shard, strings.Repeat("f", 30))
	comparison := ">="
	if active && cursor != nil {
		first, comparison = *cursor, ">"
	} else {
		scannedCount, scannedBytes = 0, 0
	}
	var count, bytes int64
	var lastID *string
	err = tx.QueryRow(ctx, `SELECT count(*)::bigint,coalesce(sum(bytes),0)::bigint,
 (array_agg(id ORDER BY id DESC))[1]::text FROM (
  SELECT id,octet_length(payload::text) AS bytes FROM commit_receipts
  WHERE id `+comparison+` $1::uuid AND id <= $2::uuid ORDER BY id LIMIT $3
 ) AS batch`, first, last, batchSize).Scan(&count, &bytes, &lastID)
	if err != nil {
		return false, err
	}
	if count < batchSize {
		_, err = tx.Exec(ctx, `UPDATE commit_receipt_usage SET receipt_count=$2,payload_json_bytes=$3,
 initialized=true,reconciled_at=clock_timestamp(),scan_in_progress=false,scan_cursor=NULL,
 scan_count=0,scan_payload_json_bytes=0,reconcile_token=NULL,retry_at=clock_timestamp() WHERE shard=$1`,
			shard, scannedCount+count, scannedBytes+bytes)
	} else {
		_, err = tx.Exec(ctx, `UPDATE commit_receipt_usage SET scan_in_progress=true,scan_cursor=$2::uuid,
 scan_count=$3,scan_payload_json_bytes=$4,reconcile_token=NULL,retry_at=clock_timestamp() WHERE shard=$1`,
			shard, lastID, scannedCount+count, scannedBytes+bytes)
	}
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
