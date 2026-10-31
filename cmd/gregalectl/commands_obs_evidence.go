package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func cmdObsEvidence(args []string) int {
	fs := flag.NewFlagSet(subObsEvidence, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	incident := fs.String("incident", "", "inbox incident ID (type/resource-id)")
	limit := fs.Int("limit", api.ObsIncidentEvidenceLimitDefault, "maximum history entries (1..100)")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	adminToken := fs.String("admin-token", "", obsAdminTokenUsage)
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	kind, id, _ := strings.Cut(*incident, "/")
	if fs.NArg() != 0 || operatorobs.ValidateReference(kind, id) != nil || *limit < 1 || *limit > api.ObsIncidentEvidenceLimitMax || *timeout <= 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs evidence: --incident type/resource-id, --limit 1..100, and a positive --timeout are required")
		return 2
	}
	cred, ok := resolveObsCredential("obs evidence", *adminToken)
	if !ok {
		return 2
	}
	body, code := fetchObsSnapshot(subObsEvidence, operatorobs.EvidencePath(kind, id)+"?limit="+strconv.Itoa(*limit), cred, *timeout)
	if code != 0 {
		return code
	}
	var response api.ObsIncidentEvidenceResponse
	if err := json.Unmarshal(body, &response); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs evidence: decode response:", err)
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(response)
	}
	writeObsEvidenceHuman(osStdout, response)
	return 0
}

func writeObsEvidenceHuman(w io.Writer, response api.ObsIncidentEvidenceResponse) {
	if len(response.UnavailableSources) > 0 {
		_, _ = fmt.Fprintf(w, "unavailable_sources=%s (evidence snapshot is incomplete)\n", strings.Join(response.UnavailableSources, ","))
	}
	item := response.Incident
	_, _ = fmt.Fprintf(w, "incident_id=%s current_status=%s severity=%s error_code=%s generated_at=%s\n",
		item.ID, item.Status, item.Severity, response.ErrorCode, response.GeneratedAt.UTC().Format(time.RFC3339))
	_, _ = fmt.Fprintf(w, "history=%d limit=%d since=%s truncated=%t\n", len(response.History), response.Limit,
		response.Since.UTC().Format(time.RFC3339), response.Truncated)
	for _, entry := range response.History {
		_, _ = fmt.Fprintf(w, "  source=%s id=%s at=%s kind=%s status=%s trace_path=%s\n",
			entry.Source, entry.ID, entry.At.UTC().Format(time.RFC3339), entry.Kind, entry.Status, entry.TracePath)
	}
	if response.TriageHistory != nil {
		writeObsTriageHistoryHuman(w, *response.TriageHistory)
	} else if item.Type == "platform_alert" {
		_, _ = fmt.Fprintln(w, "triage_history=unknown")
	}
	if response.Suppression != nil {
		_, _ = fmt.Fprintf(w, "suppression=%s silenced=%s inhibited=%s reason=%s (current Alertmanager observation; delivery unknown)\n", response.Suppression.State, suppressionBool(response.Suppression.Silenced), suppressionBool(response.Suppression.Inhibited), response.Suppression.Reason)
	} else if item.Type == "platform_alert" {
		_, _ = fmt.Fprintln(w, "suppression=unknown")
	}
	_, _ = fmt.Fprintf(w, "action_path=%s audit_path=%s runbook_url=%s\n", item.ActionPath, item.AuditPath, item.RunbookURL)
}

func suppressionBool(value *bool) string {
	if value == nil {
		return "unknown"
	}
	return strconv.FormatBool(*value)
}
