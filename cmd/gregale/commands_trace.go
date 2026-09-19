package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// traceLookupOutput is the account-scoped envelope for `gregale trace`.
// Evidence remains app-scoped on the API; the CLI composes those bounded,
// tenant-authorized responses into one trace view.
type traceLookupOutput struct {
	TraceID        string                   `json:"trace_id"`
	Matches        []traceLookupMatch       `json:"matches"`
	Spans          []api.DebugTelemetrySpan `json:"spans"`
	SpansTruncated bool                     `json:"spans_truncated"`
	Partial        bool                     `json:"partial,omitempty"`
	Errors         []traceLookupError       `json:"errors,omitempty"`
}

type traceLookupMatch struct {
	App     string                        `json:"app"`
	Request api.DebugTelemetryRequestItem `json:"request"`
}

type traceLookupError struct {
	App    string `json:"app"`
	Detail string `json:"detail"`
}

// cmdTrace locates one W3C trace id across every app in the caller's account,
// using the existing redacted debugger evidence endpoint. This is deliberately
// a CLI composition first: it makes the capability available without adding a
// second trace storage system or widening the API's tenant boundary.
func cmdTrace(args []string) int {
	if len(args) != 1 || args[0] == "--help" || args[0] == "-h" {
		PrintUsage(osStderr, "usage: gregale trace <trace-id>", "trace")
		return 1
	}
	traceID := strings.TrimSpace(args[0])
	if !validTraceID(traceID) {
		return printErr("Invalid trace id", fmt.Errorf("trace id must be 32 lowercase hexadecimal characters"))
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	apps, err := client.ListApps(ctx)
	if err != nil {
		return printErr("Could not list apps for trace lookup", err)
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Slug < apps[j].Slug })

	result := traceLookupOutput{
		TraceID: traceID,
		Matches: make([]traceLookupMatch, 0),
		Spans:   make([]api.DebugTelemetrySpan, 0),
	}
	seenSpans := make(map[string]struct{})
	for _, app := range apps {
		resp, err := client.GetAppDebugRequestEvidence(ctx, app.Slug, traceID)
		if err != nil {
			if traceLookupNotFound(err) {
				continue
			}
			result.Partial = true
			result.Errors = append(result.Errors, traceLookupError{App: app.Slug, Detail: err.Error()})
			continue
		}

		result.Matches = append(result.Matches, traceLookupMatch{App: app.Slug, Request: resp.Request})
		result.SpansTruncated = result.SpansTruncated || resp.SpansTruncated
		for _, span := range resp.Spans {
			if span.SpanID != "" {
				if _, exists := seenSpans[span.SpanID]; exists {
					continue
				}
				seenSpans[span.SpanID] = struct{}{}
			}
			result.Spans = append(result.Spans, span)
		}
	}
	sort.SliceStable(result.Matches, func(i, j int) bool { return result.Matches[i].App < result.Matches[j].App })
	sort.SliceStable(result.Spans, func(i, j int) bool {
		if result.Spans[i].DurationNanos != result.Spans[j].DurationNanos {
			return result.Spans[i].DurationNanos > result.Spans[j].DurationNanos
		}
		if result.Spans[i].Name != result.Spans[j].Name {
			return result.Spans[i].Name < result.Spans[j].Name
		}
		return result.Spans[i].SpanID < result.Spans[j].SpanID
	})

	if jsonOutput {
		code := jsonOut(writeJSON(result))
		if code != 0 {
			return code
		}
		if len(result.Matches) == 0 {
			return 1
		}
		return 0
	}

	if len(result.Matches) == 0 {
		if result.Partial {
			_, _ = fmt.Fprintf(osStdout, "Trace %s was not found; some apps could not be queried.\n", traceID)
		} else {
			_, _ = fmt.Fprintf(osStdout, "No retained request evidence for trace %s.\n", traceID)
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "TRACE %s · %d app match(es) · %d span(s)\n", traceID, len(result.Matches), len(result.Spans))
		_, _ = fmt.Fprintln(osStdout, "MATCHES")
		for _, match := range result.Matches {
			request := match.Request
			_, _ = fmt.Fprintf(osStdout, "  %s · %s %s · HTTP %d · %d ms · telemetry row %s\n",
				match.App, request.Method, request.Route, request.Status, request.LatencyMS, request.ID)
		}
		if len(result.Spans) == 0 {
			_, _ = fmt.Fprintln(osStdout, "span tree: no linked OTel spans")
		} else {
			_, _ = fmt.Fprintln(osStdout, "SPAN TREE")
			roots := buildDebugTraceRoots(result.Spans)
			for i, root := range roots {
				renderDebugTraceNode(osStdout, root, "", i == len(roots)-1, make(map[*debugTraceNode]bool))
			}
			if result.SpansTruncated {
				_, _ = fmt.Fprintln(osStdout, "span tree truncated to the slowest retained spans")
			}
		}
	}
	for _, item := range result.Errors {
		_, _ = fmt.Fprintf(osStderr, "trace %s: %s\n", item.App, item.Detail)
	}
	if result.Partial {
		return 3
	}
	if len(result.Matches) == 0 {
		return 1
	}
	return 0
}

func validTraceID(value string) bool {
	if len(value) != 32 || value == "00000000000000000000000000000000" {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16 && value == strings.ToLower(value)
}

func traceLookupNotFound(err error) bool {
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Problem.Status == 404 || apiErr.Problem.Code == api.CodeNotFound
}
