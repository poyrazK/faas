package mcplisting

// adr: 20261128135329 — the tool safety scan at listing publish: scan what
// the listing publishes, refuse unreviewed high-severity findings, and keep
// the scan and the seller's review on the listing.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/mcptoolscan"
	"github.com/onebox-faas/faas/pkg/state"
)

// ScanDocsURL documents the tool safety scan.
const ScanDocsURL = "https://gregale.dev/docs/api-monetization#tool-safety-scan"

// ScanListing runs the tool safety scan over what a listing publishes: its
// contract, or for a listing without one its tool snapshot.
func ScanListing(l state.MCPListing) (mcptoolscan.Report, error) {
	if len(l.Contract) > 0 {
		c, err := mcphosting.ReadContract(bytes.NewReader(l.Contract))
		if err != nil {
			return mcptoolscan.Report{}, fmt.Errorf("read MCP listing contract: %w", err)
		}
		return mcptoolscan.Scan(c), nil
	}
	c := mcphosting.Contract{Tools: make([]mcphosting.Tool, 0, len(l.Tools))}
	for _, t := range l.Tools {
		tool := mcphosting.Tool{Name: t.Name, Description: t.Description, InputSchema: map[string]any{"type": "object"}}
		if t.Title != "" {
			tool.Annotations, _ = json.Marshal(map[string]string{"title": t.Title})
		}
		c.Tools = append(c.Tools, tool)
	}
	return mcptoolscan.Scan(c), nil
}

// ApplyToolScan scans the listing about to be published. High findings
// refuse the publish (422 mcp_tool_scan_blocked) unless the request carries
// the seller's review reason; reviewer names who gave it. The scan and the
// review are stored on the returned listing.
func ApplyToolScan(req api.PutMCPListingRequest, l state.MCPListing, reviewer string) (state.MCPListing, *api.Problem) {
	reason := strings.TrimSpace(req.ScanReviewReason)
	if n := len(reason); n > api.MCPContractReasonMax || !utf8.ValidString(reason) {
		return state.MCPListing{}, invalid(fmt.Sprintf("scan_review_reason must be valid UTF-8 of at most %d bytes", api.MCPContractReasonMax)).
			WithLimit(api.MCPContractReasonMax, int64(n))
	}
	report, err := ScanListing(l)
	if err != nil {
		// Validate already read the contract; one that no longer reads is
		// the request's problem.
		return state.MCPListing{}, invalid("the contract could not be scanned: " + err.Error())
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return state.MCPListing{}, invalid("the tool scan could not be encoded")
	}
	high := report.High()
	if high > 0 && reason == "" {
		return state.MCPListing{}, scanBlocked(report, high)
	}
	l.ToolScan, l.ToolScanHigh, l.ToolScanReview, l.ToolScanReviewedBy = raw, high, "", ""
	if high > 0 {
		l.ToolScanReview, l.ToolScanReviewedBy = reason, reviewer
	}
	return l, nil
}

func scanBlocked(report mcptoolscan.Report, high int) *api.Problem {
	var findings []api.MCPToolScanFinding
	for _, f := range report.Findings {
		if f.Severity == mcptoolscan.SeverityHigh {
			findings = append(findings, api.MCPToolScanFinding(f))
		}
	}
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeMCPToolScanBlocked, "Tool safety scan found high-severity issues",
		fmt.Sprintf("%d high-severity finding(s) in the tools customers' models will read; change the tools, or publish with scan_review_reason saying why they are safe", high)).
		WithToolScan(findings).WithDocs(ScanDocsURL)
}

// ToolScanView is the seller's view of a stored scan; nil when none.
func ToolScanView(raw json.RawMessage, at *time.Time, review string) *api.MCPToolScan {
	report, ok := readReport(raw)
	if !ok {
		return nil
	}
	v := &api.MCPToolScan{RuleSet: report.RuleSet, Truncated: report.Truncated, Review: review, Findings: []api.MCPToolScanFinding{}}
	if at != nil {
		v.ScannedAt = *at
	}
	for _, f := range report.Findings {
		switch f.Severity {
		case mcptoolscan.SeverityHigh:
			v.High++
		case mcptoolscan.SeverityMedium:
			v.Medium++
		default:
			v.Low++
		}
		v.Findings = append(v.Findings, api.MCPToolScanFinding(f))
	}
	return v
}

// ToolScanBadge is what the public listing shows of a stored scan; nil when
// none.
func ToolScanBadge(raw json.RawMessage, at *time.Time) *api.MCPToolScanBadge {
	report, ok := readReport(raw)
	if !ok {
		return nil
	}
	b := &api.MCPToolScanBadge{RuleSet: report.RuleSet, Status: api.MCPToolScanClean}
	if at != nil {
		b.ScannedAt = *at
	}
	if high := report.High(); high > 0 {
		// A stored scan with high findings was admitted by a review.
		b.Status, b.ReviewedHigh = api.MCPToolScanReviewed, high
	}
	return b
}

func readReport(raw json.RawMessage) (mcptoolscan.Report, bool) {
	if len(raw) == 0 {
		return mcptoolscan.Report{}, false
	}
	var r mcptoolscan.Report
	if err := json.Unmarshal(raw, &r); err != nil || r.RuleSet == 0 {
		return mcptoolscan.Report{}, false
	}
	return r, true
}
