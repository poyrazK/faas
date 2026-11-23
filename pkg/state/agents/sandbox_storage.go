package agents

// Sandbox storage metering (ADR-20261122132055 §8): agentd records, once
// per UTC hour, each account's paused sessions and the bytes they keep
// stored. Metered dark: nothing prices or bills the hours until the
// GB-month price is set.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// SandboxStorageHour is one account's metered hour.
type SandboxStorageHour struct {
	Hour           time.Time
	PausedSessions int
	CaptureBytes   int64
	VolumeBytes    int64
}

// RecordSandboxStorageHour records this UTC hour's stored bytes of every
// account with paused sessions, unless an earlier sweep of the hour did. It
// returns the accounts recorded.
func (s *Store) RecordSandboxStorageHour(ctx context.Context) (int, error) {
	n, err := s.q.RecordSandboxStorageHour(ctx, s.pool)
	if err != nil {
		return 0, fmt.Errorf("record sandbox storage hour: %w", err)
	}
	return int(n), nil
}

// SandboxStorageHours lists the account's metered hours in [from, to),
// oldest first.
func (s *Store) SandboxStorageHours(ctx context.Context, accountID string, from, to time.Time) ([]SandboxStorageHour, error) {
	rows, err := s.q.ListSandboxStorageHours(ctx, s.pool, sqlc.ListSandboxStorageHoursParams{AccountID: accountID,
		FromHour: pgtype.Timestamptz{Time: from, Valid: true}, ToHour: pgtype.Timestamptz{Time: to, Valid: true}})
	if err != nil {
		return nil, fmt.Errorf("list sandbox storage hours: %w", err)
	}
	out := make([]SandboxStorageHour, 0, len(rows))
	for _, r := range rows {
		out = append(out, SandboxStorageHour{Hour: r.Hour.Time.UTC(), PausedSessions: int(r.PausedSessions),
			CaptureBytes: r.CaptureBytes, VolumeBytes: r.VolumeBytes})
	}
	return out, nil
}
