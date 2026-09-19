package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
)

// cmdTrace locates one W3C trace id through the durable account-scoped trace
// index. The API joins retained request evidence with queue lifecycle rows so
// the CLI can show cross-service propagation without app fan-out.
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
	result, err := client.GetAccountTrace(ctx, traceID)
	if err != nil {
		return printErr("Could not look up trace", err)
	}

	if jsonOutput {
		code := jsonOut(writeJSON(result))
		if code != 0 {
			return code
		}
		if len(result.Matches) == 0 && len(result.Invocations) == 0 {
			return 1
		}
		return 0
	}

	if len(result.Matches) == 0 && len(result.Invocations) == 0 {
		if result.Partial {
			_, _ = fmt.Fprintf(osStdout, "Trace %s was not found; some trace data could not be queried.\n", traceID)
		} else {
			_, _ = fmt.Fprintf(osStdout, "No retained request evidence for trace %s.\n", traceID)
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "TRACE %s · %d app match(es) · %d queue invocation(s) · %d span(s)\n", traceID, len(result.Matches), len(result.Invocations), len(result.Spans))
		_, _ = fmt.Fprintln(osStdout, "MATCHES")
		for _, match := range result.Matches {
			request := match.Request
			_, _ = fmt.Fprintf(osStdout, "  %s · %s %s · HTTP %d · %d ms · telemetry row %s\n",
				match.App, request.Method, request.Route, request.Status, request.LatencyMS, request.ID)
		}
		if len(result.Invocations) > 0 {
			_, _ = fmt.Fprintln(osStdout, "QUEUE")
			for _, inv := range result.Invocations {
				_, _ = fmt.Fprintf(osStdout, "  %s · %s %s · state=%s · attempts=%d · queued_at=%s\n",
					inv.App, inv.Source, inv.ID, inv.State, inv.Attempts, inv.CreatedAt)
			}
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
	if len(result.Matches) == 0 && len(result.Invocations) == 0 {
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
