package state

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// OutboundAITrace is one AI trace's totals (ADR 20261020202035). Caller and
// EndUser are its first call's.
type OutboundAITrace struct {
	TraceID           string
	Caller            string
	EndUser           string
	FirstAt, LastAt   time.Time
	Calls             int64
	ErrorCalls        int64
	UsageUnknownCalls int64
	InputTokens       int64
	OutputTokens      int64
	CostMillicents    int64
	ToolCalls         int64
	RepeatedToolCalls int64
	Models            []string
}

// OutboundAITraceQuery filters a trace listing: of one app (caller) or end
// user, last called before a cursor.
type OutboundAITraceQuery struct {
	AppID   string
	EndUser string
	Before  *time.Time
	Limit   int
}

// valid reports a query whose filters are an app UUID and an end-user ID,
// when set.
func (q OutboundAITraceQuery) valid() bool {
	if q.AppID != "" {
		if _, err := uuid.Parse(q.AppID); err != nil {
			return false
		}
	}
	return q.EndUser == "" || api.ValidOutboundAIUserID(q.EndUser)
}

// OutboundAITraceDetail is one trace with its logged calls, oldest first.
// CallsTruncated reports more logged calls than OutboundAITraceCallsMax.
type OutboundAITraceDetail struct {
	Trace          OutboundAITrace
	Calls          []OutboundAIRequestLogEntry
	CallsTruncated bool
}

// OutboundAITraceStore reads the traces outboundd records
// (ADR 20261020202035).
type OutboundAITraceStore interface {
	// ListOutboundAITraces lists an integration's traces by last call,
	// newest first.
	ListOutboundAITraces(ctx context.Context, accountID, integrationID string, query OutboundAITraceQuery) ([]OutboundAITrace, error)
	// GetOutboundAITrace reads one trace with its logged calls.
	GetOutboundAITrace(ctx context.Context, accountID, integrationID, traceID string) (OutboundAITraceDetail, error)
}
