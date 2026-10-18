package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type mcpAdmissionHistory struct {
	State        string                     `json:"state"`
	DeploymentID string                     `json:"deployment_id"`
	Entries      []api.MCPAdmissionEvidence `json:"entries"`
	// WindowLimit is retained for JSON compatibility; it now counts MCP receipts.
	WindowLimit    int                 `json:"window_limit"`
	NextCursor     string              `json:"next_cursor,omitempty"`
	Paginated      bool                `json:"paginated,omitempty"`
	LatestAccepted *mcpAdmissionLatest `json:"latest_accepted,omitempty"`
	LatestRejected *mcpAdmissionLatest `json:"latest_rejected,omitempty"`
}

type mcpAdmissionLatest struct {
	State          string                    `json:"state"` // available, empty, unavailable
	Receipt        *api.MCPAdmissionEvidence `json:"receipt,omitempty"`
	ReceiptPath    string                    `json:"receipt_path,omitempty"`
	InspectCommand string                    `json:"inspect_command,omitempty"`
}

func collectMCPAdmissionHistory(ctx context.Context, client *Client, deploymentID string, limit int) mcpAdmissionHistory {
	result := mcpAdmissionHistory{State: "unknown", DeploymentID: deploymentID, Entries: []api.MCPAdmissionEvidence{}, WindowLimit: limit, Paginated: true}
	page, err := client.ListMCPAdmissionHistory(ctx, deploymentID, "", "", limit)
	if err == nil && page.Limit == limit && len(page.Items) <= limit && validMCPAdmissionCursor(page.NextCursor) {
		result.State = "available"
		result.NextCursor = page.NextCursor
		for _, e := range page.Items {
			if !validMCPAdmissionEvidence(&e, deploymentID) {
				result.State = "unknown"
				continue
			}
			result.Entries = append(result.Entries, e)
		}
	}
	accepted := collectLatestMCPAdmission(ctx, client, deploymentID, "accepted")
	rejected := collectLatestMCPAdmission(ctx, client, deploymentID, "rejected")
	result.LatestAccepted = &accepted
	result.LatestRejected = &rejected
	if accepted.State == "unavailable" || rejected.State == "unavailable" {
		result.State = "unknown"
	}
	return result
}
func validMCPAdmissionCursor(cursor string) bool {
	if cursor == "" {
		return true
	}
	value, err := strconv.ParseInt(cursor, 10, 64)
	return err == nil && value > 0
}
func collectLatestMCPAdmission(ctx context.Context, client *Client, deploymentID, outcome string) mcpAdmissionLatest {
	result := mcpAdmissionLatest{State: "unavailable"}
	page, err := client.ListMCPAdmissionHistory(ctx, deploymentID, "", outcome, 1)
	if err != nil || page.Limit != 1 || len(page.Items) > 1 || !validMCPAdmissionCursor(page.NextCursor) {
		return result
	}
	if len(page.Items) == 0 {
		if page.NextCursor == "" {
			result.State = "empty"
		}
		return result
	}
	evidence := page.Items[0]
	if !validMCPAdmissionEvidence(&evidence, deploymentID) || evidence.Outcome != outcome {
		return result
	}
	result.State = "available"
	result.Receipt = &evidence
	result.ReceiptPath = "/v1/mcp/admissions/" + evidence.ID
	result.InspectCommand = "gregale mcp access history --evidence " + evidence.ID
	return result
}
func writeLatestMCPAdmission(outcome string, latest *mcpAdmissionLatest) {
	if latest == nil {
		return
	}
	_, _ = fmt.Fprintf(osStdout, "  Latest %s: %s\n", outcome, latest.State)
	if latest.Receipt != nil {
		_, _ = fmt.Fprintf(osStdout, "    %s %s\n    Receipt: %s\n    Inspect: %s\n", latest.Receipt.CapturedAt.Format(time.RFC3339), latest.Receipt.Reason, latest.ReceiptPath, latest.InspectCommand)
	}
}
func cmdMCPAccessHistory(args []string) int {
	fs := newFlagSet("mcp-access-history", flag.ContinueOnError)
	deployment := fs.String("deployment", "", "deployment id to inspect (promotion or recovery target)")
	limit := fs.Int("limit", 50, "MCP receipts per page, 1–50")
	cursor := fs.String("cursor", "", "next cursor from a previous page")
	outcome := fs.String("outcome", "", "filter accepted or rejected receipts")
	receipt := fs.String("evidence", "", "inspect an exact evidence UUID instead of deployment history")
	timeout := fs.Duration("timeout", 10*time.Second, "overall read deadline, at most 2m")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	target := *deployment
	if *receipt != "" {
		target = *receipt
	}
	parsed, err := uuid.Parse(target)
	if err != nil || !deploymentIDPatternForHistory(target) || (*receipt != "" && (*deployment != "" || *cursor != "" || *outcome != "")) || (*outcome != "" && *outcome != "accepted" && *outcome != "rejected") || fs.NArg() != 0 || *limit < 1 || *limit > 50 || *timeout <= 0 || *timeout > 2*time.Minute {
		return printErr("Invalid MCP admission history", errors.New("supply --deployment or --evidence with a UUID, --limit 1–50 and --timeout up to 2m"))
	}
	if *cursor != "" {
		if value, err := strconv.ParseInt(*cursor, 10, 64); err != nil || value <= 0 {
			return printErr("Invalid MCP admission cursor", errors.New("cursor must be a positive continuation position"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if *receipt != "" {
		evidence, err := client.GetMCPAdmissionReceipt(ctx, parsed.String())
		if err != nil || evidence.ID != parsed.String() || !validMCPAdmissionEvidence(&evidence, evidence.DeploymentID) {
			return printErr("MCP admission evidence unavailable", errors.New("could not read a valid receipt"))
		}
		return writeMCPAdmissionHistory(mcpAdmissionHistory{State: "available", DeploymentID: evidence.DeploymentID, Entries: []api.MCPAdmissionEvidence{evidence}, WindowLimit: 1, Paginated: true})
	}
	page, err := client.ListMCPAdmissionHistory(ctx, parsed.String(), *cursor, *outcome, *limit)
	history := mcpAdmissionHistory{State: "unknown", DeploymentID: parsed.String(), Entries: []api.MCPAdmissionEvidence{}, WindowLimit: *limit, Paginated: true}
	if err == nil && page.Limit >= 1 && page.Limit <= 50 {
		history.State = "available"
		history.NextCursor = page.NextCursor
		for _, e := range page.Items {
			if !validMCPAdmissionEvidence(&e, parsed.String()) {
				history.State = "unknown"
				continue
			}
			history.Entries = append(history.Entries, e)
		}
	}
	return writeMCPAdmissionHistory(history)
}
func deploymentIDPatternForHistory(id string) bool { return len(id) == 32 || len(id) == 36 }
func writeMCPAdmissionHistory(history mcpAdmissionHistory) int {
	if jsonOutput {
		if code := jsonOut(writeJSON(history)); code != 0 {
			return code
		}
	} else {
		if history.Paginated {
			_, _ = fmt.Fprintf(osStdout, "MCP admission evidence for %s: %s\n", history.DeploymentID, history.State)
		} else {
			_, _ = fmt.Fprintf(osStdout, "MCP admission evidence for %s: %s (recent %d audit rows)\n", history.DeploymentID, history.State, history.WindowLimit)
		}
		writeLatestMCPAdmission("accepted", history.LatestAccepted)
		writeLatestMCPAdmission("rejected", history.LatestRejected)
		if history.NextCursor != "" {
			_, _ = fmt.Fprintf(osStdout, "  Next page: --cursor %s\n", history.NextCursor)
		}
		for _, entry := range history.Entries {
			_, _ = fmt.Fprintf(osStdout, "  %s: %s %s (%d%% traffic, %d gateways, evidence %s)\n", entry.CapturedAt.Format(time.RFC3339), entry.Outcome, entry.Reason, entry.TrafficPercent, len(entry.Gateways), entry.ID)
		}
		if history.State == "available" && len(history.Entries) == 0 {
			if history.Paginated {
				_, _ = fmt.Fprintln(osStdout, "  No matching retained MCP admission evidence.")
			} else {
				_, _ = fmt.Fprintln(osStdout, "  No MCP admission evidence in this recent audit window.")
			}
		}
	}
	if history.State != "available" {
		return 1
	}
	return 0
}
func attachMCPAdmissionHistory(ctx context.Context, client *Client, report *mcpCatalogStatusReport) {
	for i := range report.Deployments {
		history := collectMCPAdmissionHistory(ctx, client, report.Deployments[i].DeploymentID, 50)
		report.Deployments[i].Admission = &history
	}
}

func validMCPAdmissionEvidence(evidence *api.MCPAdmissionEvidence, deploymentID string) bool {
	if evidence == nil || evidence.Version != 1 || evidence.DeploymentID != deploymentID || evidence.CapturedAt.IsZero() || evidence.TrafficPercent < 0 || evidence.TrafficPercent > 100 {
		return false
	}
	if _, err := uuid.Parse(evidence.ID); err != nil {
		return false
	}
	for i, digest := range []string{evidence.ExpectedFingerprint, evidence.ObservedFingerprint} {
		if i == 1 && digest == "" {
			continue
		}
		raw, err := hex.DecodeString(digest)
		if err != nil || len(raw) != 32 || digest != strings.ToLower(digest) {
			return false
		}
	}
	if evidence.ExpectedRevision != nil && *evidence.ExpectedRevision <= 0 {
		return false
	}
	if evidence.ObservedRevision != nil && *evidence.ObservedRevision < 0 {
		return false
	}
	switch evidence.Outcome {
	case "accepted":
		return evidence.Reason == ""
	case "rejected":
		return api.IsMCPPromotionConditionFailure(evidence.Reason) || evidence.Reason == api.CodeTrafficServingChanged || evidence.Reason == "traffic_update_failed"
	}
	return false
}
