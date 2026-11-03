package state

import (
	"context"
	"errors"
	"sort"
	"time"
)

// TerminalInstanceBatchDeleter deletes a bounded batch of terminal instance
// rows (ADR 20261103131546). schedd's hourly retention listed every expired
// STOPPED/FAILED row in the fleet and deleted them one statement at a time on
// every scheduler, each racing the others over the same rows.
type TerminalInstanceBatchDeleter interface {
	DeleteTerminalInstancesOlderThan(ctx context.Context, states []State, threshold time.Time, limit int) (int64, error)
}

var (
	_ TerminalInstanceBatchDeleter = (*PgStore)(nil)
	_ TerminalInstanceBatchDeleter = (*MemStore)(nil)
)

// DeleteTerminalInstancesOlderThan deletes up to limit rows in states whose
// terminal_at is before threshold, oldest first, with the predicate of
// ListInstancesInTerminalStatesOlderThan: a row a job task references is
// kept. Rows another sweep has locked are skipped, so schedulers sweeping at
// once split the work instead of repeating it.
func (s *PgStore) DeleteTerminalInstancesOlderThan(ctx context.Context, states []State, threshold time.Time, limit int) (int64, error) {
	if limit <= 0 || len(states) == 0 {
		return 0, nil
	}
	stateStrs := make([]string, len(states))
	for i, st := range states {
		stateStrs[i] = string(st)
	}
	tag, err := s.pool.Exec(ctx, `
		with doomed as (
			select id
			  from instances
			 where state = any($1)
			   and terminal_at is not null
			   and terminal_at < $2
			   and not exists (select 1 from job_tasks t where t.instance_id = instances.id)
			 order by terminal_at asc, id asc
			 for update skip locked
			 limit $3
		)
		delete from instances i
		 using doomed d
		 where i.id = d.id
		   and i.state = any($1)
		   and i.terminal_at < $2`, stateStrs, threshold, limit)
	if err != nil {
		return 0, mapErr(err)
	}
	return tag.RowsAffected(), nil
}

// DeleteTerminalInstancesOlderThan mirrors PgStore.DeleteTerminalInstancesOlderThan.
func (m *MemStore) DeleteTerminalInstancesOlderThan(ctx context.Context, states []State, threshold time.Time, limit int) (int64, error) {
	if limit <= 0 || len(states) == 0 {
		return 0, nil
	}
	rows, err := m.ListInstancesInTerminalStatesOlderThan(ctx, states, threshold)
	if err != nil {
		return 0, err
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].TerminalAt, rows[j].TerminalAt
		if a != nil && b != nil && !a.Equal(*b) {
			return a.Before(*b)
		}
		return rows[i].ID < rows[j].ID
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	var deleted int64
	for _, row := range rows {
		if err := m.DeleteInstance(ctx, row.ID); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}
