package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// QuietPause is one quiet pause in the ledger (ADR-20261023171905). EndedAt
// is zero while the pause is open.
type QuietPause struct {
	InstanceID string
	StartedAt  time.Time
	EndedAt    time.Time
}

// QuietPausedSeconds is an instance's proven quiet-paused seconds in a
// window. Unproven reports an open pause whose node watermark had not reached
// the window's end, so part of the pause may be missing from Seconds.
type QuietPausedSeconds struct {
	Seconds  int64
	Unproven bool
}

// QuietPauseLedgerStore is the quiet pause ledger: schedd records the pauses
// vmmd reports and meterd subtracts the paused seconds it proves from billed
// residency (ADR-20261023171905). PgStore implements it; callers type-assert
// and leave the feature off without it.
type QuietPauseLedgerStore interface {
	// RecordQuietPauses records one node report: its pauses, open (zero
	// EndedAt) or closed, and the instant up to which the report covers the
	// node. A pause only gains a later end and the watermark only advances.
	RecordQuietPauses(ctx context.Context, nodeID string, coveredUntil time.Time, pauses []QuietPause) error
	// OpenQuietPauses lists a node's open pauses.
	OpenQuietPauses(ctx context.Context, nodeID string) ([]QuietPause, error)
	// QuietPausedSeconds returns each instance's proven paused seconds in
	// [start, end).
	QuietPausedSeconds(ctx context.Context, start, end time.Time) (map[string]QuietPausedSeconds, error)
	// DeleteQuietPausesBefore deletes up to limit pauses that started before
	// cutoff and reports how many it deleted.
	DeleteQuietPausesBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}

var _ QuietPauseLedgerStore = (*PgStore)(nil)

// RecordQuietPauses implements QuietPauseLedgerStore.
func (s *PgStore) RecordQuietPauses(ctx context.Context, nodeID string, coveredUntil time.Time, pauses []QuietPause) error {
	node, err := parsePgUUID(nodeID)
	if err != nil {
		return fmt.Errorf("state: record quiet pauses: node %q: %w", nodeID, err)
	}
	arg := sqlc.RecordQuietPausesParams{
		NodeID:       node,
		CoveredUntil: pgTime(coveredUntil),
		InstanceIds:  make([]pgtype.UUID, 0, len(pauses)),
		StartedAts:   make([]pgtype.Timestamptz, 0, len(pauses)),
		EndedAts:     make([]pgtype.Timestamptz, 0, len(pauses)),
	}
	for _, p := range pauses {
		instance, err := parsePgUUID(p.InstanceID)
		if err != nil {
			return fmt.Errorf("state: record quiet pauses: instance %q: %w", p.InstanceID, err)
		}
		if p.StartedAt.IsZero() || (!p.EndedAt.IsZero() && p.EndedAt.Before(p.StartedAt)) {
			return fmt.Errorf("state: record quiet pauses: instance %s: invalid pause [%v, %v]", p.InstanceID, p.StartedAt, p.EndedAt)
		}
		ended := pgtype.Timestamptz{}
		if !p.EndedAt.IsZero() {
			ended = pgTime(p.EndedAt)
		}
		arg.InstanceIds = append(arg.InstanceIds, instance)
		arg.StartedAts = append(arg.StartedAts, pgTime(p.StartedAt))
		arg.EndedAts = append(arg.EndedAts, ended)
	}
	if err := sqlc.New().RecordQuietPauses(ctx, s.pool, arg); err != nil {
		return fmt.Errorf("state: record quiet pauses for node %s: %w", nodeID, mapErr(err))
	}
	return nil
}

// OpenQuietPauses implements QuietPauseLedgerStore.
func (s *PgStore) OpenQuietPauses(ctx context.Context, nodeID string) ([]QuietPause, error) {
	node, err := parsePgUUID(nodeID)
	if err != nil {
		return nil, fmt.Errorf("state: open quiet pauses: node %q: %w", nodeID, err)
	}
	rows, err := sqlc.New().OpenQuietPauses(ctx, s.pool, node)
	if err != nil {
		return nil, fmt.Errorf("state: open quiet pauses for node %s: %w", nodeID, mapErr(err))
	}
	out := make([]QuietPause, 0, len(rows))
	for _, r := range rows {
		out = append(out, QuietPause{InstanceID: r.InstanceID, StartedAt: r.StartedAt.Time})
	}
	return out, nil
}

// QuietPausedSeconds implements QuietPauseLedgerStore.
func (s *PgStore) QuietPausedSeconds(ctx context.Context, start, end time.Time) (map[string]QuietPausedSeconds, error) {
	if !start.Before(end) {
		return map[string]QuietPausedSeconds{}, nil
	}
	rows, err := sqlc.New().QuietPausedSeconds(ctx, s.pool, sqlc.QuietPausedSecondsParams{
		WindowStart: pgTime(start), WindowEnd: pgTime(end),
	})
	if err != nil {
		return nil, fmt.Errorf("state: quiet paused seconds: %w", mapErr(err))
	}
	out := make(map[string]QuietPausedSeconds, len(rows))
	for _, r := range rows {
		if r.PausedSeconds > 0 || r.Unproven {
			out[r.InstanceID] = QuietPausedSeconds{Seconds: max(r.PausedSeconds, 0), Unproven: r.Unproven}
		}
	}
	return out, nil
}

// DeleteQuietPausesBefore implements QuietPauseLedgerStore.
func (s *PgStore) DeleteQuietPausesBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	n, err := sqlc.New().DeleteQuietPausesBefore(ctx, s.pool, sqlc.DeleteQuietPausesBeforeParams{
		Cutoff: pgTime(cutoff), BatchLimit: int32(min(limit, 1<<30)),
	})
	if err != nil {
		return 0, fmt.Errorf("state: delete quiet pauses before %v: %w", cutoff, mapErr(err))
	}
	return n, nil
}
