package state

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var auditTraceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// The FK-free audit table retains trace_id inside data, unlike events which
// has a dedicated column. Preserve a caller's explicit ID in that payload.
func auditLogDataWithTrace(entry AuditLog) (json.RawMessage, error) {
	if entry.TraceID == nil {
		return entry.Data, nil
	}
	if !auditTraceIDPattern.MatchString(*entry.TraceID) {
		return nil, fmt.Errorf("invalid audit trace id")
	}
	data := map[string]any{}
	if len(entry.Data) > 0 {
		if err := json.Unmarshal(entry.Data, &data); err != nil {
			return nil, fmt.Errorf("audit payload must be an object: %w", err)
		}
	}
	if data == nil {
		data = map[string]any{}
	}
	data["trace_id"] = *entry.TraceID
	return json.Marshal(data)
}

func auditLogHasTrace(entry AuditLog) bool {
	var data struct {
		TraceID string `json:"trace_id"`
	}
	return json.Unmarshal(entry.Data, &data) == nil && auditTraceIDPattern.MatchString(data.TraceID)
}

type OperatorOverviewActivity struct {
	OrgsTotal      int
	AuditEvents24h int
	Failures       []api.ObsOverviewFailureKind
}

type OperatorIncidentFilter struct {
	Since       time.Time
	StuckBefore time.Time
	StaleBefore time.Time
	Kind        string
	Severity    string
	BeforeAt    time.Time
	BeforeID    string
	Limit       int
}

func operatorIncidentEvidenceLimit(limit int) int {
	if limit < 1 {
		return api.ObsIncidentEvidenceLimitDefault + 1
	}
	if limit > api.ObsIncidentEvidenceLimitMax+1 {
		return api.ObsIncidentEvidenceLimitMax + 1
	}
	return limit
}

// OperatorObservabilityReader keeps fleet projections bounded at their source.
// Store embeds it so store adapters preserve these reads as well.
type OperatorObservabilityReader interface {
	OperatorIncidentContextReader
	// OperatorOverview reads all overview projections from one consistent snapshot.
	OperatorOverview(context.Context, time.Time) (api.ObsOverviewResponse, error)
	OperatorAuditTraceCoverage(context.Context, time.Time) (float64, error)
	OperatorOverviewActivity(context.Context, time.Time, time.Time) (OperatorOverviewActivity, error)
	OperatorTenantCounts(context.Context, string) (int, int, error)
	// OperatorTenantApps counts live, non-deleted deployments without reading
	// deployment history or issuing a separate query for each tenant app.
	OperatorTenantApps(context.Context, string) ([]api.ObsTenantApp, error)
	// OperatorCapacityProfiles returns at most the named profiles plus custom;
	// aggregation happens in the store rather than loading fleet workload rows.
	OperatorCapacityProfiles(context.Context) ([]api.ObsCapacityProfile, error)
	ListOperatorIncidents(context.Context, OperatorIncidentFilter) ([]api.ObsIncident, error)
	OperatorIncidentEvidence(context.Context, string, time.Time, int) ([]api.ObsIncidentEvidenceEntry, error)
}
