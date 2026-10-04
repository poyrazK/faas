package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ JobBillingWindowStore = (*PgStore)(nil)

// ListJobInstancesInBillingWindow includes terminal instances whose residency
// overlaps the sampled interval. Current lifecycle state cannot erase usage.
func (s *PgStore) ListJobInstancesInBillingWindow(ctx context.Context, start, end time.Time) ([]JobBillingInstance, error) {
	if start.IsZero() || !start.Before(end) || end.Sub(start) > time.Minute {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().JobInstancesInBillingWindow(ctx, s.pool, sqlc.JobInstancesInBillingWindowParams{WindowStart: financialTimestamp(start), WindowEnd: financialTimestamp(end)})
	if err != nil {
		return nil, fmt.Errorf("state: job billing window: %w", err)
	}
	out := make([]JobBillingInstance, 0, len(rows))
	for _, row := range rows {
		out = append(out, JobBillingInstance{Instance: Instance{ID: pgUUIDString(row.ID), State: row.State, RAMMB: int(row.RamMb), JobID: pgUUIDString(row.JobID), Kind: "job_task"}, AccountID: pgUUIDString(row.AccountID)})
	}
	return out, nil
}
