// commands_ai_traces.go — `gregale ai traces`: an integration's AI traces,
// the calls of one agent task named by X-Gregale-AI-Trace or traceparent,
// and one trace's calls in order (ADR 20261020202035).

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

const aiTracesUsage = "usage: gregale ai traces <integration-id> [trace-id] [--app ID] [--user ID] [--limit N]"

// cmdAITraces lists traces newest first, or shows one trace's calls.
func cmdAITraces(args []string) int {
	fs := newFlagSet("ai traces", flag.ContinueOnError)
	app := fs.String("app", "", "only traces whose first call came from this app (UUID)")
	user := fs.String("user", "", "only traces whose first call named this end user")
	limit := fs.Int("limit", 50, "traces to show, 1 to 200")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() < 1 || fs.NArg() > 2 || *limit < 1 ||
		*limit > api.OutboundAITracePageMax {
		return printErr("Invalid ai traces arguments", errors.New(aiTracesUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if fs.NArg() == 2 {
		return showAITrace(client, fs.Arg(0), fs.Arg(1))
	}
	list, err := client.ListOutboundAITraces(context.Background(), fs.Arg(0), api.OutboundAITraceQuery{
		AppID: *app, EndUser: *user, Limit: *limit})
	if err != nil {
		return printErr("Could not list AI traces", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(list))
	}
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "TRACE\tLAST CALL\tCALLS\tERRORS\tINPUT\tOUTPUT\tCOST\tTOOL CALLS\tREPEATED\tMODELS")
	for _, trace := range list.Items {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%s\t%d\t%d\t%s\n", trace.TraceID, trace.LastAt.Format("2006-01-02 15:04:05Z"),
			trace.Calls, trace.ErrorCalls, trace.InputTokens, trace.OutputTokens, financialMoney(trace.CostMillicents),
			trace.ToolCalls, trace.RepeatedToolCalls, strings.Join(trace.Models, ","))
	}
	_ = tw.Flush()
	return 0
}

// showAITrace prints one trace's totals and its calls, oldest first.
func showAITrace(client *api.Client, integrationID, traceID string) int {
	detail, err := client.GetOutboundAITrace(context.Background(), integrationID, traceID)
	if err != nil {
		return printErr("Could not read AI trace", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(detail))
	}
	trace := detail.Trace
	_, _ = fmt.Fprintf(osStdout, "Trace: %s   Calls: %d   Errors: %d   Cost: %s   Tool calls: %d   Repeated: %d\n", trace.TraceID,
		trace.Calls, trace.ErrorCalls, financialMoney(trace.CostMillicents), trace.ToolCalls, trace.RepeatedToolCalls)
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "TIME\tMODEL\tSTATUS\tINPUT\tOUTPUT\tCOST\tDURATION\tTTFB\tTOOLS")
	for _, call := range detail.Calls {
		ttfb := "-"
		if call.TimeToFirstByteMS != nil {
			ttfb = strconv.FormatInt(*call.TimeToFirstByteMS, 10) + "ms"
		}
		tools := "-"
		if len(call.ToolCalls) > 0 {
			tools = strings.Join(call.ToolCalls, ",")
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\t%dms\t%s\t%s\n", call.CreatedAt.Format("15:04:05.000Z"), call.Model,
			call.Status, call.InputTokens, call.OutputTokens, financialMoney(call.CostMillicents), call.DurationMS, ttfb, tools)
	}
	_ = tw.Flush()
	if detail.CallsTruncated {
		_, _ = fmt.Fprintf(osStdout, "Showing the first %d logged calls.\n", len(detail.Calls))
	}
	if len(detail.Calls) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No logged calls: the integration records the trace but keeps no request log.")
	}
	return 0
}

// applyTraceLimits applies the --trace-max-* flags to a policy request:
// "" keeps a limit, "none" clears it.
func applyTraceLimits(req *api.PutOutboundAIPolicyRequest, calls, cost, repeats string) error {
	var err error
	if req.TraceMaxCalls, err = optionalLimit(calls, req.TraceMaxCalls, boundedCount(api.MaxOutboundAITraceCalls)); err != nil {
		return err
	}
	if req.TraceMaxCostMillicents, err = optionalLimit(cost, req.TraceMaxCostMillicents, parseUSDMillicents); err != nil {
		return err
	}
	req.TraceMaxRepeatedToolCalls, err = optionalLimit(repeats, req.TraceMaxRepeatedToolCalls,
		boundedCount(api.MaxOutboundAITraceRepeatedToolCalls))
	return err
}

// boundedCount parses a whole count from 1 to maximum.
func boundedCount(maximum int64) func(string) (int64, error) {
	return func(value string) (int64, error) {
		count, err := strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(value), "_", ""), 10, 64)
		if err != nil || count < 1 || count > maximum {
			return 0, fmt.Errorf("%q must be a count from 1 to %d", value, maximum)
		}
		return count, nil
	}
}
