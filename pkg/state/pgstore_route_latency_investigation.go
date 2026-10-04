package state

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/debugger"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func pgInvestigationLatency(ctx context.Context, db sqlc.DBTX, accountID, slug string, out *api.RouteHealthInvestigation) error {
	windows, err := json.Marshal(out.Windows)
	if err != nil {
		return fmt.Errorf("encode latency windows: %w", err)
	}
	rows, err := (&sqlc.Queries{}).RouteHealthLatencyEvidence(ctx, db, sqlc.RouteHealthLatencyEvidenceParams{AccountID: accountID, AppID: out.Report.AppID, CandidateID: out.Report.DeploymentID, StableID: out.Report.StableDeploymentID, Method: out.Selection.Method, Path: out.Selection.Path, CustomerID: out.Selection.CustomerID, CustomerGroupBy: out.Selection.CustomerGroupBy, Windows: windows, RowsLimit: api.RouteHealthLatencyEvidenceRowsLimit})
	if err != nil {
		return fmt.Errorf("read route latency evidence: %w", err)
	}
	for i := range out.Windows {
		w := &out.Windows[i]
		candidate, stable := []debugger.RouteLatencyRow{}, []debugger.RouteLatencyRow{}
		for _, row := range rows {
			if !row.Start.Time.Equal(w.Start) {
				continue
			}
			evidence := pgLatencyRow(row, slug)
			if row.DeploymentID == out.Report.DeploymentID {
				candidate = append(candidate, evidence)
			} else {
				stable = append(stable, evidence)
			}
		}
		w.Diagnostics = debugger.RouteLatencyDiagnostics(candidate, stable, w.Candidate.ObservedRows, w.Stable.ObservedRows)
	}
	return nil
}

func pgLatencyRow(row sqlc.RouteHealthLatencyEvidenceRow, slug string) debugger.RouteLatencyRow {
	out := debugger.RouteLatencyRow{Example: api.RouteHealthInvestigationExample{TelemetryID: row.TelemetryID, ReceivedAt: row.ReceivedAt.Time, Status: int(row.Status), LatencyMS: int64(row.LatencyMs), RepresentedRequests: int64(row.Count), TraceID: row.TraceID.String, EvidencePath: "/v1/apps/" + url.PathEscape(slug) + "/debug/requests/" + row.TelemetryID + "/evidence"}, Spans: row.SpansSummary, ColdBoot: row.ColdBoot, WakeID: row.WakeID.String}
	if row.GuestOutcome != "missing" {
		ms := int64(row.GuestDurationMs)
		out.GuestMS = &ms
	}
	if row.WakeBootMs >= 0 {
		out.WakeBootMS = &row.WakeBootMs
	}
	return out
}
