package state

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func cloneScheduleOccurrence(o ScheduleOccurrence) ScheduleOccurrence {
	o.SchedulePolicy = *workpolicy.Clone(&o.SchedulePolicy)
	o.StartDeadlineAt = cloneTimePtr(o.StartDeadlineAt)
	o.StartedAt = cloneTimePtr(o.StartedAt)
	o.FinishedAt = cloneTimePtr(o.FinishedAt)
	return o
}

func (m *MemStore) ScheduleOccurrenceListByJob(_ context.Context, jobID string, limit int, before string) ([]ScheduleOccurrence, error) {
	return m.listScheduleOccurrences(jobID, limit, before, true)
}

func (m *MemStore) ScheduleOccurrenceListByCron(_ context.Context, cronID string, limit int, before string) ([]ScheduleOccurrence, error) {
	return m.listScheduleOccurrences(cronID, limit, before, false)
}

func (m *MemStore) listScheduleOccurrences(resourceID string, limit int, before string, jobs bool) ([]ScheduleOccurrence, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]ScheduleOccurrence, 0)
	for _, occurrence := range m.scheduleOccurrences {
		if (jobs && occurrence.JobID == resourceID) || (!jobs && occurrence.CronID == resourceID) {
			rows = append(rows, cloneScheduleOccurrence(occurrence))
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ScheduledFor.Equal(rows[j].ScheduledFor) {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].ScheduledFor.After(rows[j].ScheduledFor)
	})
	start := 0
	if before != "" {
		start = len(rows)
		for i := range rows {
			if rows[i].ID == before {
				start = i + 1
				break
			}
		}
	}
	if start >= len(rows) {
		return []ScheduleOccurrence{}, nil
	}
	end := start + limit
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end], nil
}

var _ ScheduleOccurrenceHistoryStore = (*MemStore)(nil)
