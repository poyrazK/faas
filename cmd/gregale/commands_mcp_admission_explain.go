package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type mcpAdmissionGatewayExplanation struct {
	NodeName   string     `json:"node_name"`
	Freshness  string     `json:"freshness"`
	Replay     string     `json:"replay"`
	Revision   *int64     `json:"revision"`
	ObservedAt *time.Time `json:"observed_at"`
}
type mcpAdmissionExplanation struct {
	State           string                           `json:"state"`
	Receipt         *api.MCPAdmissionEvidence        `json:"receipt,omitempty"`
	Explanation     string                           `json:"explanation"`
	Fingerprint     string                           `json:"fingerprint"`
	AccountRevision string                           `json:"account_revision"`
	GatewayEvidence string                           `json:"gateway_evidence"`
	Gateways        []mcpAdmissionGatewayExplanation `json:"gateways"`
	Current         *mcpConvergenceReport            `json:"current_convergence,omitempty"`
	CurrentState    string                           `json:"current_state"`
	Actions         []mcpOverviewAction              `json:"actions"`
}

func cmdMCPAccessExplain(args []string) int {
	fs := newFlagSet("mcp-access-explain", flag.ContinueOnError)
	evidence := fs.String("evidence", "", "retained admission evidence UUID")
	app := fs.String("app", "", "optional application slug for current convergence")
	timeout := fs.Duration("timeout", 10*time.Second, "overall read deadline, at most 2m")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	id, err := uuid.Parse(*evidence)
	if err != nil || !deploymentIDPatternForHistory(*evidence) || (*app != "" && !api.ValidAppSlug(*app)) || fs.NArg() != 0 || *timeout <= 0 || *timeout > 2*time.Minute {
		return printErr("Invalid MCP admission explanation", errors.New("supply --evidence UUID, optional --app slug, and --timeout up to 2m"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	return writeMCPAdmissionExplanation(collectMCPAdmissionExplanation(ctx, client, id.String(), *app))
}
func collectMCPAdmissionExplanation(ctx context.Context, client *Client, id, slug string) mcpAdmissionExplanation {
	report := mcpAdmissionExplanation{State: "unavailable", Explanation: "Could not read valid retained admission evidence.", Fingerprint: "unavailable", AccountRevision: "unavailable", GatewayEvidence: "unavailable", Gateways: []mcpAdmissionGatewayExplanation{}, CurrentState: "not_requested", Actions: []mcpOverviewAction{}}
	receipt, err := client.GetMCPAdmissionReceipt(ctx, id)
	if err != nil || receipt.ID != id || !validMCPAdmissionEvidence(&receipt, receipt.DeploymentID) {
		return report
	}
	if _, err := uuid.Parse(receipt.DeploymentID); err != nil {
		return report
	}
	seen := map[string]bool{}
	for _, gateway := range receipt.Gateways {
		if gateway.NodeName == "" || seen[gateway.NodeName] || (gateway.Revision != nil && *gateway.Revision < 0) {
			return report
		}
		seen[gateway.NodeName] = true
	}
	report.State = "available"
	report.Receipt = &receipt
	report.Explanation = mcpAdmissionReasonExplanation(receipt.Outcome, receipt.Reason)
	report.Actions = append(report.Actions, mcpOverviewAction{Label: "Inspect deployment traffic before retrying", Arguments: []string{"gregale", "deployment", receipt.DeploymentID}}, mcpOverviewAction{Label: "Inspect retained admission history", Arguments: []string{"gregale", "mcp", "access", "history", "--deployment", receipt.DeploymentID}})
	if receipt.ObservedFingerprint != "" {
		report.Fingerprint = "matched"
		if receipt.ObservedFingerprint != receipt.ExpectedFingerprint {
			report.Fingerprint = "changed"
		}
	}
	report.AccountRevision = "not_requested"
	report.GatewayEvidence = "not_requested"
	if receipt.ExpectedRevision != nil {
		report.AccountRevision = "unavailable"
		report.GatewayEvidence = "unavailable"
		if receipt.ObservedRevision != nil {
			report.AccountRevision = "matched"
			if *receipt.ExpectedRevision != *receipt.ObservedRevision {
				report.AccountRevision = "changed"
			}
			report.GatewayEvidence = "recorded"
			if len(receipt.Gateways) == 0 {
				report.GatewayEvidence = "no_serving_gateways"
			}
		}
	}
	for _, gateway := range receipt.Gateways {
		finding := mcpAdmissionGatewayExplanation{NodeName: gateway.NodeName, Freshness: "missing", Replay: "unverified", Revision: gateway.Revision, ObservedAt: gateway.ObservedAt}
		if gateway.ObservedAt != nil {
			finding.Freshness = "fresh"
			if gateway.ObservedAt.After(receipt.CapturedAt) {
				finding.Freshness = "future_dated"
			} else if gateway.ObservedAt.Before(receipt.CapturedAt.Add(-api.MCPPolicyGatewayFreshness)) {
				finding.Freshness = "stale"
			}
		}
		if gateway.Revision == nil {
			finding.Replay = "missing"
		} else if receipt.ObservedRevision != nil {
			finding.Replay = "applied"
			if *gateway.Revision < *receipt.ObservedRevision {
				finding.Replay = "behind"
			}
		}
		report.Gateways = append(report.Gateways, finding)
	}
	if slug != "" {
		report.CurrentState = "unavailable"
		deployment, err := client.GetDeployment(ctx, receipt.DeploymentID)
		app, appErr := client.GetApp(ctx, slug)
		if err == nil && appErr == nil && deployment.ID == receipt.DeploymentID && app.Slug == slug && app.ID != "" && deployment.AppID == app.ID {
			current := collectMCPConvergence(ctx, client, slug, app.ID)
			report.Current = &current
			if current.Snapshot != nil && !current.Snapshot.CapturedAt.IsZero() && (current.State == "active" || current.State == "pending" || current.State == "unverified" || current.State == "paused") && !current.Snapshot.CapturedAt.Before(receipt.CapturedAt) {
				report.CurrentState = "available"
			} else {
				current.State = "unknown"
				current.Snapshot = nil
			}
			report.Actions = append(report.Actions, mcpOverviewAction{Label: "Inspect current hosting state before a fresh verified release attempt", Arguments: []string{"gregale", "mcp", "overview", "--app", slug}})
		}
	}
	return report
}
func mcpAdmissionReasonExplanation(outcome, reason string) string {
	if outcome == "accepted" {
		return "The traffic transaction committed with the recorded admission checks. Later gateway or policy changes require a separate current observation."
	}
	switch reason {
	case api.CodeMCPAccessPolicyChanged:
		return "Stored access policy no longer matched the reviewed fingerprint. Review and audit the policy again before retrying."
	case api.CodeMCPAccountRevisionChanged:
		return "The account policy revision changed after review. Obtain a fresh audit before retrying."
	case api.CodeMCPGatewayEvidenceUnavailable:
		return "Serving gateway convergence could not be established. Inspect gateway membership and replay reporting before retrying."
	case api.CodeMCPGatewayEvidenceStale:
		return "At least one gateway report was missing, older than the freshness limit, or future-dated at the recorded decision time."
	case api.CodeMCPGatewayReplayPending:
		return "At least one serving gateway had not replayed the observed account revision at the recorded decision time."
	case api.CodeMCPAdmissionEvidenceUnavailable:
		return "Admission evidence could not be persisted. Inspect actual deployment traffic before retrying."
	case api.CodeTrafficServingChanged:
		return "Serving traffic changed concurrently. Inspect current traffic before attempting a freshly verified release."
	default:
		return "The traffic update failed. Inspect actual deployment traffic before retrying; the receipt does not contain the underlying private error."
	}
}
func writeMCPAdmissionExplanation(report mcpAdmissionExplanation) int {
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "MCP admission explanation: %s\n%s\n", report.State, report.Explanation)
		if receipt := report.Receipt; receipt != nil {
			_, _ = fmt.Fprintf(osStdout, "Decision at %s: %s %s (%d%% traffic)\nPolicy fingerprint: %s\n  Expected: %s\n  Observed: %s\nAccount revision: %s (expected %s, observed %s)\nGateway evidence: %s\n", receipt.CapturedAt.Format(time.RFC3339Nano), receipt.Outcome, receipt.Reason, receipt.TrafficPercent, report.Fingerprint, receipt.ExpectedFingerprint, receipt.ObservedFingerprint, report.AccountRevision, mcpAdmissionRevision(receipt.ExpectedRevision), mcpAdmissionRevision(receipt.ObservedRevision), report.GatewayEvidence)
			for _, gateway := range report.Gateways {
				_, _ = fmt.Fprintf(osStdout, "  %s: report %s at %s, revision %s, replay %s\n", mcpSafeTerminalText(gateway.NodeName), gateway.Freshness, mcpAdmissionObservedAt(gateway.ObservedAt), mcpAdmissionRevision(gateway.Revision), gateway.Replay)
			}
		}
		_, _ = fmt.Fprintf(osStdout, "Current observation (separate from this receipt): %s\n", report.CurrentState)
		if report.Current != nil {
			if snapshot := report.Current.Snapshot; snapshot != nil {
				_, _ = fmt.Fprintf(osStdout, "  Observed at %s\n", snapshot.CapturedAt.Format(time.RFC3339Nano))
			}
			writeMCPConvergence(report.Current)
		}
		for _, action := range report.Actions {
			_, _ = fmt.Fprintf(osStdout, "  %s: %s\n", action.Label, mcpOverviewCommand(action))
		}
	}
	if report.State != "available" || report.CurrentState == "unavailable" {
		return 1
	}
	return 0
}
func mcpAdmissionRevision(revision *int64) string {
	if revision == nil {
		return "not recorded"
	}
	return fmt.Sprint(*revision)
}

func mcpAdmissionObservedAt(at *time.Time) string {
	if at == nil {
		return "not recorded"
	}
	return at.Format(time.RFC3339Nano)
}
