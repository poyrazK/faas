package agents

// Sandbox session usage (ADR-20261027090013 step 11): vmmd's cumulative
// measured memory of a session VM, kept while agentd holds the session's
// lease. Nothing bills it yet.

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// SandboxUsage is a session VM's usage so far: Usage's fields, with
// PausedMillis counting frozen time too, FrozenMillis its frozen part, and
// Final set on the record sent at close.
type SandboxUsage struct {
	Usage
	FrozenMillis int64
	Final        bool
}

// RecordSandboxUsage keeps a session's latest usage record. It reports
// whether the record was kept: one older than the kept record, or after the
// final one, is not; neither is one from a lease this replica no longer
// holds, which the lease renewal finds out on its own.
func (s *Store) RecordSandboxUsage(ctx context.Context, id, token string, u SandboxUsage) (bool, error) {
	if u.Method < 1 || u.Method > math.MaxInt16 || u.Samples < 0 || u.ExpectedSamples > math.MaxInt32 || u.Samples > u.ExpectedSamples {
		return false, fmt.Errorf("record session usage: method %d or %d/%d samples out of range", u.Method, u.Samples, u.ExpectedSamples)
	}
	n, err := s.q.RecordSandboxSessionUsage(ctx, s.pool, sqlc.RecordSandboxSessionUsageParams{Method: int16(u.Method),
		Samples: int32(u.Samples), ExpectedSamples: int32(u.ExpectedSamples), MeasuredMs: u.MeasuredMillis, PausedMs: u.PausedMillis,
		FrozenMs: u.FrozenMillis, PrivateMbMs: u.PrivateMBMillis, UnpausedPrivateMbMs: u.UnpausedPrivateMBMillis,
		BillableMbMs: u.BillableMBMillis, PeakPrivateMb: u.PeakPrivateMB, ColdBoot: u.ColdBoot, Final: u.Final,
		SessionID: id, LeaseToken: token})
	if err != nil {
		return false, fmt.Errorf("record session usage: %w", err)
	}
	return n == 1, nil
}

// SandboxSessionUsage returns a session's recorded usage; found is false
// when none was.
func (s *Store) SandboxSessionUsage(ctx context.Context, accountID, id string) (u SandboxUsage, found bool, err error) {
	row, err := s.q.GetSandboxSessionUsage(ctx, s.pool, sqlc.GetSandboxSessionUsageParams{SessionID: id, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return SandboxUsage{}, false, nil
	}
	if err != nil {
		return SandboxUsage{}, false, fmt.Errorf("get session usage: %w", err)
	}
	return SandboxUsage{Usage: Usage{Method: int(row.Method), Samples: int(row.Samples), ExpectedSamples: int(row.ExpectedSamples),
		MeasuredMillis: row.MeasuredMs, PausedMillis: row.PausedMs, PrivateMBMillis: row.PrivateMbMs,
		UnpausedPrivateMBMillis: row.UnpausedPrivateMbMs, BillableMBMillis: row.BillableMbMs, PeakPrivateMB: row.PeakPrivateMb,
		ColdBoot: row.ColdBoot}, FrozenMillis: row.FrozenMs, Final: row.Final}, true, nil
}
