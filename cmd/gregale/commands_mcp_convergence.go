package main

import (
	"context"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
)

type mcpConvergenceAlert struct {
	ID              string `json:"id"`
	Metric          string `json:"metric"`
	State           string `json:"state"`
	LastFiredAt     string `json:"last_fired_at,omitempty"`
	LastEvaluatedAt string `json:"last_evaluated_at,omitempty"`
}
type mcpConvergenceReport struct {
	State       string                            `json:"state"`
	Snapshot    *api.MCPPolicyConvergenceSnapshot `json:"snapshot,omitempty"`
	AlertsState string                            `json:"alerts_state"`
	Alerts      []mcpConvergenceAlert             `json:"alerts"`
}

func collectMCPConvergence(ctx context.Context, client *Client, slug, appID string) mcpConvergenceReport {
	result := mcpConvergenceReport{State: "unknown", AlertsState: "unknown", Alerts: []mcpConvergenceAlert{}}
	status, err := client.GetAppsSlugAccountEdgePolicyStatus(ctx, slug)
	if err == nil && status.AppID == appID && status.EdgeRules.Scope == "account" {
		if status.MCPConvergence != nil {
			result.Snapshot = status.MCPConvergence
			result.State = status.MCPConvergence.State
		} else {
			result.State = status.EdgeRules.State
		}
	}
	rules, err := client.ListAlertRules(ctx, slug)
	if err == nil {
		result.AlertsState = "available"
		for _, rule := range rules {
			if rule.Enabled && rule.AppID == appID && api.IsMCPPolicyConvergenceMetric(rule.Metric) {
				result.Alerts = append(result.Alerts, mcpConvergenceAlert{ID: rule.ID, Metric: rule.Metric, State: rule.State, LastFiredAt: rule.LastFiredAt, LastEvaluatedAt: rule.LastEvaluatedAt})
			}
		}
	}
	return result
}
func writeMCPConvergence(report *mcpConvergenceReport) {
	if report == nil {
		return
	}
	_, _ = fmt.Fprintf(osStdout, "  Account policy convergence: %s\n", report.State)
	if snapshot := report.Snapshot; snapshot != nil {
		_, _ = fmt.Fprintf(osStdout, "  Gateway reports: %d serving, %d unconverged, %d stale (account revision %d)\n", snapshot.ServingGateways, snapshot.UnconvergedGateways, snapshot.StaleGateways, snapshot.DesiredRevision)
		if snapshot.AdmissionID != "" {
			_, _ = fmt.Fprintf(osStdout, "  Admission evidence: %s (%s)\n", snapshot.AdmissionID, snapshot.AdmissionHistoryPath)
		}
	}
	_, _ = fmt.Fprintf(osStdout, "  Convergence alerts: %s (%d enabled)\n", report.AlertsState, len(report.Alerts))
	for _, rule := range report.Alerts {
		_, _ = fmt.Fprintf(osStdout, "    %s: %s (last evaluated %s)\n", rule.Metric, rule.State, rule.LastEvaluatedAt)
	}
}
