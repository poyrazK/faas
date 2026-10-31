package state

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAITraceStore = (*MemStore)(nil)

// ListOutboundAITraces mirrors the PgStore listing over seeded traces.
func (m *MemStore) ListOutboundAITraces(_ context.Context, accountID, integrationID string, query OutboundAITraceQuery) ([]OutboundAITrace, error) {
	if !query.valid() {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.memOutboundIntegrationOwned(accountID, integrationID) {
		return nil, ErrNotFound
	}
	traces := make([]OutboundAITrace, 0)
	for _, trace := range m.outboundAITraces[integrationID] {
		if (query.Before == nil || trace.LastAt.Before(*query.Before)) && (query.AppID == "" || trace.Caller == query.AppID) &&
			(query.EndUser == "" || trace.EndUser == query.EndUser) {
			traces = append(traces, trace)
		}
	}
	sort.SliceStable(traces, func(i, j int) bool {
		if !traces[i].LastAt.Equal(traces[j].LastAt) {
			return traces[i].LastAt.After(traces[j].LastAt)
		}
		return traces[i].TraceID < traces[j].TraceID
	})
	if page := boundedPage(query.Limit, api.OutboundAITracePageMax); len(traces) > page {
		traces = traces[:page]
	}
	return traces, nil
}

// GetOutboundAITrace mirrors the PgStore read: a seeded trace with its
// seeded request-log calls, oldest first.
func (m *MemStore) GetOutboundAITrace(_ context.Context, accountID, integrationID, traceID string) (OutboundAITraceDetail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.memOutboundIntegrationOwned(accountID, integrationID) {
		return OutboundAITraceDetail{}, ErrNotFound
	}
	for _, trace := range m.outboundAITraces[integrationID] {
		if trace.TraceID != traceID {
			continue
		}
		detail := OutboundAITraceDetail{Trace: trace, Calls: make([]OutboundAIRequestLogEntry, 0)}
		for _, entry := range m.outboundAIRequestLog[integrationID] {
			if entry.TraceID == traceID {
				detail.Calls = append(detail.Calls, entry)
			}
		}
		sort.SliceStable(detail.Calls, func(i, j int) bool { return detail.Calls[i].CreatedAt.Before(detail.Calls[j].CreatedAt) })
		if len(detail.Calls) > api.OutboundAITraceCallsMax {
			detail.Calls, detail.CallsTruncated = detail.Calls[:api.OutboundAITraceCallsMax], true
		}
		return detail, nil
	}
	return OutboundAITraceDetail{}, ErrNotFound
}

// SeedOutboundAITrace stands in for outboundd's trace writes in tests.
func (m *MemStore) SeedOutboundAITrace(integrationID string, traces ...OutboundAITrace) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.outboundAITraces == nil {
		m.outboundAITraces = make(map[string][]OutboundAITrace)
	}
	m.outboundAITraces[integrationID] = append(m.outboundAITraces[integrationID], traces...)
}
