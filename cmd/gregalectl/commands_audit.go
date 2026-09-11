// commands_audit.go — read-only operator trace correlation.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const dispatchAudit = "audit"

var auditTraceIDShape = regexp.MustCompile(`^[0-9a-f]{32}$`)

func cmdAuditDispatch(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit: missing subcommand; want trace")
		return 2
	}
	if args[0] != "trace" {
		_, _ = fmt.Fprintf(osStderr, "gregalectl audit: unknown subcommand %q\n", args[0])
		return 2
	}
	return cmdAuditTrace(args[1:])
}

func cmdAuditTrace(args []string) int {
	fs := flag.NewFlagSet("trace", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	traceID := fs.String("trace-id", "", "OTel 32-char lowercase hex trace id")
	limit := fs.Int("limit", api.ObsAdminEventsLimitDefault, "maximum intents and events to return (1..500)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit trace: positional arguments are not accepted")
		return 2
	}
	cleanTraceID := strings.TrimSpace(*traceID)
	if !auditTraceIDShape.MatchString(cleanTraceID) {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit trace: --trace-id is required and must match ^[0-9a-f]{32}$")
		return 2
	}
	if *limit < 1 || *limit > api.ObsAdminEventsLimitMax {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit trace: --limit must be between 1 and 500")
		return 2
	}
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit trace:", err)
		return 1
	}
	path := "/v1/admin/obs/traces/" + url.PathEscape(cleanTraceID) + "?limit=" + strconv.Itoa(*limit)
	var response api.ObsTraceLookupResponse
	if err := newOperatorHTTPClient(&sess).doJSON(context.Background(), http.MethodGet, path, nil, &response, false, nil); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit trace:", err)
		return 1
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	printAuditTrace(response)
	return 0
}

func printAuditTrace(response api.ObsTraceLookupResponse) {
	_, _ = fmt.Fprintf(osStdout, "trace_id=%s generated_at=%s intents=%d events=%d\n",
		response.TraceID, response.GeneratedAt.Format(time.RFC3339), len(response.Intents), len(response.Events))
	type timelineRow struct {
		at   time.Time
		line string
	}
	rows := make([]timelineRow, 0, len(response.Intents)+len(response.Events))
	for _, intent := range response.Intents {
		line := fmt.Sprintf("intent id=%s kind=%s status=%s target=%s actor=%s reason=%q",
			intent.IntentID, intent.Kind, intent.Status, intent.TargetID, intent.ActorID, intent.Reason)
		if intent.Error != "" {
			line += fmt.Sprintf(" error=%q", intent.Error)
		}
		rows = append(rows, timelineRow{at: intent.RequestedAt, line: line})
	}
	for _, event := range response.Events {
		line := fmt.Sprintf("event id=%d kind=%s actor=%s subject=%s", event.ID, event.Kind, event.Actor, event.Subject)
		if len(event.Data) > 0 {
			line += " data=" + string(event.Data)
		}
		rows = append(rows, timelineRow{at: event.At, line: line})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at.Before(rows[j].at) })
	for _, row := range rows {
		_, _ = fmt.Fprintf(osStdout, "%s %s\n", row.at.Format(time.RFC3339Nano), row.line)
	}
}
