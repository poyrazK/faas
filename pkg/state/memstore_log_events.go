package state

import (
	"context"
	"sort"
	"strings"
	"time"
)

var _ LogEventStore = (*MemStore)(nil)

// InsertLogEvent mirrors the PostgreSQL idempotency key in memory. It is used
// by handler and ingestion tests; production writers use PgStore.
func (m *MemStore) InsertLogEvent(_ context.Context, event LogEvent) (LogEvent, error) {
	normalized, err := normalizeLogEventForInsert(event, time.Now())
	if err != nil {
		return LogEvent{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if normalized.SourceEventID != "" {
		for _, existing := range m.logEvents {
			if existing.AccountID == normalized.AccountID &&
				existing.AppID == normalized.AppID &&
				existing.Source == normalized.Source &&
				existing.SourceEventID == normalized.SourceEventID &&
				existing.OccurredAt.Equal(normalized.OccurredAt) {
				return cloneLogEvent(existing), nil
			}
		}
	}
	m.logEvents = append(m.logEvents, cloneLogEvent(normalized))
	return cloneLogEvent(normalized), nil
}

// ListLogEvents applies the same tenant predicates, filters, ordering, and
// tuple cursor as the PostgreSQL implementation.
func (m *MemStore) ListLogEvents(_ context.Context, filter LogEventFilter) ([]LogEvent, bool, error) {
	normalized, err := normalizeLogEventFilter(filter)
	if err != nil {
		return nil, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	rows := make([]LogEvent, 0, normalized.Limit+1)
	for _, event := range m.logEvents {
		if event.AccountID != normalized.AccountID || event.AppID != normalized.AppID {
			continue
		}
		if event.OccurredAt.Before(normalized.Since) || !event.OccurredAt.Before(normalized.Until) {
			continue
		}
		if normalized.Source != "" && event.Source != normalized.Source {
			continue
		}
		if normalized.DeploymentID != "" && event.DeploymentID != normalized.DeploymentID {
			continue
		}
		if normalized.RequestID != "" && event.RequestID != normalized.RequestID && event.TraceID != normalized.RequestID {
			continue
		}
		if normalized.Route != "" && event.Route != normalized.Route {
			continue
		}
		if normalized.Status != 0 && event.Status != normalized.Status {
			continue
		}
		if !normalized.BeforeAt.IsZero() {
			if event.OccurredAt.After(normalized.BeforeAt) ||
				(event.OccurredAt.Equal(normalized.BeforeAt) && strings.Compare(event.ID, normalized.BeforeID) >= 0) {
				continue
			}
		}
		rows = append(rows, cloneLogEvent(event))
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].OccurredAt.Equal(rows[j].OccurredAt) {
			return rows[i].OccurredAt.After(rows[j].OccurredAt)
		}
		return rows[i].ID > rows[j].ID
	})
	hasMore := len(rows) > normalized.Limit
	if hasMore {
		rows = rows[:normalized.Limit]
	}
	return rows, hasMore, nil
}
