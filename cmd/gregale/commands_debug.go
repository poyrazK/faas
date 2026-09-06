// commands_debug.go — ADR-127 PR-B CLI surface for the
// production debugger. Mirrors commands_invocations.go's shape:
// 3-word subcommands, --since / --route / --source / --mirror /
// --limit flags, --json / --ndjson output modes.
//
// Subcommand surface:
//
//	gregale debug requests list <slug> [--since <dur>] [--route <pattern>] [--limit N]
//	gregale debug requests get <slug> <req_id>
//	gregale debug requests replay <slug> <req_id>
//	gregale debug regressions <slug> [--since <dur>]
//	gregale debug compare <slug> --source <id> --mirror <id> [--route <pattern>] [--since <dur>]
//
// PR-B ships the list/regressions/compare/replay verbs; "get"
// surfaces a single request's metadata by id (read from the
// list endpoint and filtered locally — the underlying API has
// no GET /debug/requests/{id} endpoint today; the dashboard
// reads from the list).

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

// debugCmdUsage is the canonical usage text. Mirrors the shape of
// commands_invocations.go's PrintUsage strings.
const debugCmdUsage = "usage: gregale debug <requests|regressions|compare> ..."

// debugCmdDocsTopic is the docs topic slug for the debug
// namespace. Resolves to cli_meta.go's "debug" cliCommand entry;
// TestUsageDocSlugParity scans PrintUsage calls for this string.
const debugCmdDocsTopic = "debug"

func cmdDebug(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, debugCmdUsage, debugCmdDocsTopic)
		return 1
	}
	switch args[0] {
	case "requests":
		return cmdDebugRequests(args[1:])
	case "regressions":
		return cmdDebugRegressions(args[1:])
	case "compare":
		return cmdDebugCompare(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown debug subcommand %q\n", args[0])
	return 1
}

func cmdDebugRequests(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale debug requests <list|get|replay> ...", debugCmdDocsTopic)
		return 1
	}
	switch args[0] {
	case subList:
		return cmdDebugRequestsList(args[1:])
	case "get":
		return cmdDebugRequestsGet(args[1:])
	case "replay":
		return cmdDebugRequestsReplay(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown debug requests subcommand %q\n", args[0])
	return 1
}

// cmdDebugRequestsList renders the recent request telemetry for
// a slug. PR-A's ListAppDebugRequests backs this verb.
func cmdDebugRequestsList(args []string) int {
	fs := flag.NewFlagSet("debug requests list", flag.ContinueOnError)
	since := fs.String("since", "", "lookback window (e.g. 30m, 24h, 3d)")
	route := fs.String("route", "", "route filter (exact match)")
	limit := fs.Int("limit", 20, "max rows (1..200)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale debug requests list [--since D] [--route P] [--limit N] <slug>", debugCmdDocsTopic)
		return 1
	}
	slug := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListAppDebugRequests(context.Background(), slug, *since)
	if err != nil {
		return printErr("Could not list debug requests", err)
	}
	if *route != "" {
		filtered := resp.Requests[:0]
		for _, r := range resp.Requests {
			if r.Route == *route {
				filtered = append(filtered, r)
			}
		}
		resp.Requests = filtered
	}
	if *limit > 0 && len(resp.Requests) > *limit {
		resp.Requests = resp.Requests[:*limit]
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderDebugRequestsTable(os.Stdout, resp)
	return 0
}

// cmdDebugRequestsGet renders a single request's metadata by id.
func cmdDebugRequestsGet(args []string) int {
	if len(args) != 2 {
		PrintUsage(os.Stderr, "usage: gregale debug requests get <slug> <req_id>", debugCmdDocsTopic)
		return 1
	}
	slug, reqID := args[0], args[1]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetAppDebugRequest(context.Background(), slug, reqID)
	if err != nil {
		return printErr("Could not get debug request", err)
	}
	return jsonOut(writeJSON(resp))
}

// cmdDebugRequestsReplay queues a replay. PR-B returns a
// stable "queued" status (PR-A2 wires the actual mirror
// invocation).
func cmdDebugRequestsReplay(args []string) int {
	if len(args) != 2 {
		PrintUsage(os.Stderr, "usage: gregale debug requests replay <slug> <req_id>", debugCmdDocsTopic)
		return 1
	}
	slug, reqID := args[0], args[1]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ReplayAppDebugRequest(context.Background(), slug, reqID)
	if err != nil {
		return printErr("Could not queue replay", err)
	}
	if resp.Status == "" {
		resp.Status = "queued"
	}
	return jsonOut(writeJSON(resp))
}

// cmdDebugRegressions renders the active regression observations
// for a slug. Powers the dashboard regression banner feed.
func cmdDebugRegressions(args []string) int {
	fs := flag.NewFlagSet("debug regressions", flag.ContinueOnError)
	since := fs.String("since", "", "lookback window (e.g. 30m, 24h, 3d)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale debug regressions [--since D] <slug>", debugCmdDocsTopic)
		return 1
	}
	slug := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListAppDebugRegressions(context.Background(), slug, *since)
	if err != nil {
		return printErr("Could not list regressions", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderDebugRegressionsTable(os.Stdout, resp)
	return 0
}

// cmdDebugCompare renders the per-route compare between two
// deployments. POSTs the body shape and renders the merged
// per-route stats.
func cmdDebugCompare(args []string) int {
	fs := flag.NewFlagSet("debug compare", flag.ContinueOnError)
	since := fs.String("since", "", "lookback window (e.g. 30m, 24h, 3d)")
	route := fs.String("route", "", "route filter (exact match)")
	source := fs.String("source", "", "source deployment id")
	mirror := fs.String("mirror", "", "mirror deployment id")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *source == "" || *mirror == "" {
		PrintUsage(os.Stderr, "usage: gregale debug compare --source <id> --mirror <id> [--route P] [--since D] <slug>", debugCmdDocsTopic)
		return 1
	}
	slug := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.CompareAppDebugDeployments(context.Background(), slug, *source, *mirror, *route, *since, "")
	if err != nil {
		return printErr("Could not compare deployments", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderDebugCompareTable(os.Stdout, resp)
	return 0
}

func renderDebugRequestsTable(w io.Writer, resp api.DebugTelemetryListResponse) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tROUTE\tMETHOD\tSTATUS\tLATENCY_MS\tCOLD\tRECEIVED_AT")
	for _, r := range resp.Requests {
		cold := ""
		if r.ColdBoot {
			cold = "yes"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
			r.ID, r.Route, r.Method, r.Status, r.LatencyMS, cold, r.ReceivedAt)
	}
	_ = tw.Flush()
}

func renderDebugRegressionsTable(w io.Writer, resp api.DebugRegressionsResponse) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "DEPLOYMENT\tROUTE\tFACTOR\tP95_MS\tP95_BASE_MS\tAFFECTED\tLAST_DETECTED")
	for _, r := range resp.Regressions {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
			r.DeploymentID, r.Route, r.Factor, r.P95MS, r.P95BaseMS, r.AffectedCount, r.LastDetectedAt)
	}
	_ = tw.Flush()
}

func renderDebugCompareTable(w io.Writer, resp api.DebugCompareResponse) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "Source: %s\n", resp.Source)
	_, _ = fmt.Fprintf(tw, "Mirror: %s\n", resp.Mirror)
	_, _ = fmt.Fprintln(tw, "ROUTE\tSOURCE_P95\tMIRROR_P95\tDELTA")
	for _, r := range resp.Routes {
		delta := r.MirrorP95 - r.SourceP95
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%+d\n", r.Route, r.SourceP95, r.MirrorP95, delta)
	}
	_ = tw.Flush()
}
