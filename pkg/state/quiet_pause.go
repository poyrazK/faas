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

// QuietPausedUsageStore records and reads the customer-visible quiet pause
// usage: per instance-minute, the MB-seconds meterd did not bill because the
// ledger proved the VM quiet-paused (ADR-20261023171905 rollout step 3).
// PgStore and MemStore implement it; meterd and apid type-assert and skip
// it without it.
type QuietPausedUsageStore interface {
	// RecordQuietPausedUsage records one instance-minute's unbilled
	// paused MB-seconds. The first positive value wins, as for
	// mb_seconds, so a caught-up minute never changes what the customer
	// already saw.
	RecordQuietPausedUsage(ctx context.Context, accountID, appID, instanceID string, minute time.Time, pausedMBSeconds int64) error
	// QuietPausedMBSecondsByMonth returns an account's unbilled paused
	// MB-seconds for the UTC calendar month containing month.
	QuietPausedMBSecondsByMonth(ctx context.Context, accountID string, month time.Time) (int64, error)
}

var (
	_ QuietPausedUsageStore = (*PgStore)(nil)
	_ QuietPausedUsageStore = (*MemStore)(nil)
)

// quietPausedMonth returns the half-open UTC calendar month containing t.
func quietPausedMonth(t time.Time) (start, end time.Time) {
	t = t.UTC()
	start = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0)
}

// RecordQuietPausedUsage implements QuietPausedUsageStore.
func (s *PgStore) RecordQuietPausedUsage(ctx context.Context, accountID, appID, instanceID string, minute time.Time, pausedMBSeconds int64) error {
	if pausedMBSeconds <= 0 {
		return nil
	}
	arg := sqlc.RecordQuietPausedUsageParams{
		Minute: pgTime(minute.UTC().Truncate(time.Minute)), PausedMbSeconds: pausedMBSeconds,
	}
	var err error
	if arg.AccountID, err = parsePgUUID(accountID); err != nil {
		return fmt.Errorf("state: record quiet paused usage: account %q: %w", accountID, err)
	}
	if arg.AppID, err = parsePgUUID(appID); err != nil {
		return fmt.Errorf("state: record quiet paused usage: app %q: %w", appID, err)
	}
	if arg.InstanceID, err = parsePgUUID(instanceID); err != nil {
		return fmt.Errorf("state: record quiet paused usage: instance %q: %w", instanceID, err)
	}
	if err := sqlc.New().RecordQuietPausedUsage(ctx, s.pool, arg); err != nil {
		return fmt.Errorf("state: record quiet paused usage for instance %s: %w", instanceID, mapErr(err))
	}
	return nil
}

// QuietPausedMBSecondsByMonth implements QuietPausedUsageStore.
func (s *PgStore) QuietPausedMBSecondsByMonth(ctx context.Context, accountID string, month time.Time) (int64, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return 0, fmt.Errorf("state: quiet paused usage: account %q: %w", accountID, err)
	}
	start, end := quietPausedMonth(month)
	n, err := sqlc.New().QuietPausedMBSecondsByMonth(ctx, s.pool, sqlc.QuietPausedMBSecondsByMonthParams{
		AccountID: account, MonthStart: pgTime(start), MonthEnd: pgTime(end),
	})
	if err != nil {
		return 0, fmt.Errorf("state: quiet paused usage for account %s: %w", accountID, mapErr(err))
	}
	return n, nil
}

// RecordQuietPausedUsage implements QuietPausedUsageStore. A new row
// mirrors the SQL insert: zero billed usage, owned by the app's billing
// subject.
func (m *MemStore) RecordQuietPausedUsage(_ context.Context, accountID, appID, instanceID string, minute time.Time, pausedMBSeconds int64) error {
	if pausedMBSeconds <= 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := minute.UTC().Truncate(time.Minute)
	for i := range m.usage {
		if m.usage[i].InstanceID == instanceID && m.usage[i].Minute.Equal(key) {
			if m.usage[i].PausedMBSeconds == 0 {
				m.usage[i].PausedMBSeconds = pausedMBSeconds
			}
			return nil
		}
	}
	accountID = m.appBillingSubjectLocked(accountID, appID)
	m.usage = append(m.usage, usageMinute{
		AccountID: accountID, AppID: appID, MeterKind: "app", InstanceID: instanceID,
		Minute: key, PausedMBSeconds: pausedMBSeconds,
	})
	m.recomputeMonthLocked(accountID, appID, key)
	return nil
}

// QuietPausedMBSecondsByMonth implements QuietPausedUsageStore.
func (m *MemStore) QuietPausedMBSecondsByMonth(_ context.Context, accountID string, month time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	start, end := quietPausedMonth(month)
	var n int64
	for _, u := range m.usage {
		if u.AccountID == accountID && !u.Minute.Before(start) && u.Minute.Before(end) {
			n += u.PausedMBSeconds
		}
	}
	return n, nil
}
