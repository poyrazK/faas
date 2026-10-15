package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func cmdObsContext(args []string) int {
	fs := flag.NewFlagSet(subObsContext, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	incident := fs.String("incident", "", "incident ID: type/resource-id")
	limit := fs.Int("limit", api.ObsIncidentContextLimitDefault, "maximum related resources")
	historyLimit := fs.Int("history-limit", api.ObsIncidentEvidenceLimitDefault, "maximum related timeline entries")
	cursor := fs.String("cursor", "", "opaque next_cursor from this incident")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	adminToken := fs.String("admin-token", "", "admin bearer (default: $FAAS_ADMIN_TOKEN)")
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	kind, id, _ := strings.Cut(*incident, "/")
	if fs.NArg() != 0 || operatorobs.ValidateReference(kind, id) != nil || *limit < 1 || *limit > api.ObsIncidentContextLimitMax || *historyLimit < 1 || *historyLimit > api.ObsIncidentEvidenceLimitMax || *timeout <= 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs context: a valid --incident, resource/history limits, and positive --timeout are required")
		return 2
	}
	token := *adminToken
	if token == "" {
		token = os.Getenv("FAAS_ADMIN_TOKEN")
	}
	if token == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs context: --admin-token (or $FAAS_ADMIN_TOKEN) required")
		return 2
	}
	query := url.Values{"limit": {strconv.Itoa(*limit)}, "history_limit": {strconv.Itoa(*historyLimit)}}
	if *cursor != "" {
		query.Set("cursor", *cursor)
	}
	body, code := fetchObsSnapshot(subObsContext, "/v1/admin/obs/incidents/"+kind+"/"+id+"/context?"+query.Encode(), token, *timeout)
	if code != 0 {
		return code
	}
	var response api.ObsIncidentContextResponse
	if err := json.Unmarshal(body, &response); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs context: decode response:", err)
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(response)
	}
	writeObsContextHuman(osStdout, response)
	return 0
}

func writeObsContextHuman(w io.Writer, r api.ObsIncidentContextResponse) {
	_, _ = fmt.Fprintf(w, "incident_id=%s incident_status=%s incident_observed_at=%s current_relationships_as_of=%s\n", r.IncidentID, r.IncidentStatus, r.IncidentObservedAt.UTC().Format(time.RFC3339), r.AsOf.UTC().Format(time.RFC3339))
	_, _ = fmt.Fprintf(w, "scope=%s basis=%s reason=%s (current potential exposure; causation is not established)\n", r.Scope.State, r.Scope.Basis, r.Scope.Reason)
	if r.Counts != nil {
		c := r.Counts
		_, _ = fmt.Fprintf(w, "related_apps=%d tenants=%d deployments=%d nodes=%d jobs=%d job_runs=%d explicitly_failed_resources=%d\n", c.Apps, c.Tenants, c.Deployments, c.Nodes, c.Jobs, c.JobRuns, c.FailedResources)
	} else {
		_, _ = fmt.Fprintln(w, "counts=unknown")
	}
	if len(r.UnavailableSources) > 0 {
		_, _ = fmt.Fprintf(w, "unavailable_sources=%s\n", strings.Join(r.UnavailableSources, ","))
	}
	for _, item := range r.Resources {
		_, _ = fmt.Fprintf(w, "  kind=%s id=%s status=%s relationship=%s confirmed_failure=%t\n", item.Kind, item.ID, item.Status, item.Relationship, item.ConfirmedFailure)
	}
	_, _ = fmt.Fprintf(w, "next_cursor=%s timeline_scope=%s since=%s until=%s timeline_truncated=%t\n", r.NextCursor, r.TimelineScope, r.Since.UTC().Format(time.RFC3339), r.Until.UTC().Format(time.RFC3339), r.TimelineTruncated)
	for _, item := range r.Timeline {
		e := item.Entry
		_, _ = fmt.Fprintf(w, "  resource=%s/%s match=%s source=%s id=%s at=%s kind=%s status=%s trace_path=%s\n", item.ResourceKind, item.ResourceID, item.Match, e.Source, e.ID, e.At.UTC().Format(time.RFC3339), e.Kind, e.Status, e.TracePath)
	}
}
