package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func cmdObsTriageHistory(args []string) int {
	fs := flag.NewFlagSet(subObsTriageHistory, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	incident := fs.String("incident", "", "platform_alert/episode-id")
	limit := fs.Int("limit", api.ObsAlertTriageHistoryLimitDefault, "maximum receipts (1..100)")
	page := fs.String("cursor", "", "opaque next_cursor for this episode (15 minute lifetime)")
	token := fs.String("admin-token", "", obsAdminTokenUsage)
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	kind, id, _ := strings.Cut(*incident, "/")
	if fs.NArg() != 0 || kind != "platform_alert" || operatorobs.ValidateReference(kind, id) != nil || *limit < 1 || *limit > api.ObsAlertTriageHistoryLimitMax || len(*page) > api.ObsAlertTriageHistoryCursorMaxBytes || *timeout <= 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs triage-history: valid --incident platform_alert/id, --limit 1..100, cursor and positive --timeout required")
		return 2
	}
	cred, ok := resolveObsCredential("obs triage-history", *token)
	if !ok {
		return 2
	}
	query := url.Values{"limit": {strconv.Itoa(*limit)}}
	if *page != "" {
		query.Set("cursor", *page)
	}
	body, code := fetchObsSnapshot(subObsTriageHistory, operatorobs.TriageHistoryPath(id)+"?"+query.Encode(), cred, *timeout)
	if code != 0 {
		return code
	}
	var response api.ObsAlertTriageHistory
	if err := json.Unmarshal(body, &response); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs triage-history: invalid response")
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(response)
	}
	writeObsTriageHistoryHuman(osStdout, response)
	return 0
}

func writeObsTriageHistoryHuman(w io.Writer, h api.ObsAlertTriageHistory) {
	_, _ = fmt.Fprintf(w, "triage_history incident_id=%s snapshot_revision=%d current_revision=%d coverage=%s available=%d missing=%d truncated=%t snapshot_at=%s\n", h.IncidentID, h.SnapshotRevision, h.CurrentRevision, h.Coverage.State, h.Coverage.AvailableEntries, h.Coverage.MissingEntries, h.Truncated, h.SnapshotAt.UTC().Format(time.RFC3339))
	for _, entry := range h.Items {
		_, _ = fmt.Fprintf(w, "  revision=%d action=%s at=%s acknowledged=%t owned=%t trace_path=%s", entry.Revision, entry.Action, entry.At.UTC().Format(time.RFC3339), entry.Acknowledged, entry.Owned, entry.TracePath)
		if entry.SnoozedUntil != nil {
			_, _ = fmt.Fprintf(w, " snoozed_until=%s", entry.SnoozedUntil.UTC().Format(time.RFC3339))
		}
		_, _ = fmt.Fprintln(w)
	}
	_, _ = fmt.Fprintf(w, "triage_history_path=%s next_cursor=%s\n", h.HistoryPath, h.NextCursor)
}
