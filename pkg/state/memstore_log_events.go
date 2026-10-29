package state

import (
	"context"
	"fmt"
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
		if !logEventMatches(event, normalized) {
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

// FacetLogEvents mirrors the PostgreSQL facet counts over the same predicate.
func (m *MemStore) FacetLogEvents(_ context.Context, filter LogEventFilter, routeLimit int) (LogEventFacets, error) {
	normalized, err := normalizeLogEventFilter(filter)
	if err != nil {
		return LogEventFacets{}, err
	}
	if routeLimit < 1 {
		routeLimit = 1
	}
	sources, levels, classes, routes := map[string]int64{}, map[string]int64{}, map[string]int64{}, map[string]int64{}
	var facets LogEventFacets
	m.mu.Lock()
	for _, event := range m.logEvents {
		if !logEventMatches(event, normalized) {
			continue
		}
		n := int64(max(event.Occurrences, 1))
		facets.Total += n
		sources[string(event.Source)] += n
		if event.Level != "" {
			levels[event.Level] += n
		}
		if event.Status != 0 {
			classes[fmt.Sprintf("%dxx", event.Status/100)] += n
		}
		if event.Route != "" {
			routes[event.Route] += n
		}
	}
	m.mu.Unlock()
	for dim, counts := range map[string]map[string]int64{"source": sources, "level": levels, "status": classes, "route": routes} {
		for value, count := range counts {
			facets.add(dim, value, count)
		}
	}
	facets.finish(routeLimit)
	return facets, nil
}

// ListAccountTraceLogEvents mirrors the PostgreSQL account-wide trace read.
func (m *MemStore) ListAccountTraceLogEvents(_ context.Context, filter AccountTraceLogFilter) ([]LogEvent, bool, error) {
	normalized, err := normalizeAccountTraceLogFilter(filter)
	if err != nil {
		return nil, false, err
	}
	if len(normalized.AppIDs) == 0 {
		return nil, false, nil
	}
	apps := make(map[string]bool, len(normalized.AppIDs))
	for _, appID := range normalized.AppIDs {
		apps[appID] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	rows := make([]LogEvent, 0, normalized.Limit+1)
	for _, event := range m.logEvents {
		if event.AccountID != normalized.AccountID || !apps[event.AppID] || event.TraceID != normalized.TraceID {
			continue
		}
		if normalized.Source != "" && event.Source != normalized.Source {
			continue
		}
		if event.OccurredAt.Before(normalized.Since) || !event.OccurredAt.Before(normalized.Until) {
			continue
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
