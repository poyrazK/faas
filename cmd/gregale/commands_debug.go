// commands_debug.go — ADR-127 PR-B CLI surface for the
// production debugger. Mirrors commands_invocations.go's shape:
// 3-word subcommands, --since / --route / --source / --mirror /
// --limit flags, --json / --ndjson output modes.
//
// Subcommand surface:
//
//	gregale debug requests list <slug> [--since <dur>] [--route <pattern>] [--deployment-id UUID] [--status N] [--cold-boot true|false] [--consumer-id UUID|__anonymous__] [--min-latency-ms N] [--cursor C] [--limit N]
//	gregale debug requests export <slug> [--since <dur>] [--route <pattern>] [--format ndjson|csv] [--limit N] [--output PATH]
//	gregale debug requests watch <slug> [--since <dur>] [--route <pattern>] [--deployment-id UUID] [--status N] [--cold-boot true|false] [--consumer-id UUID|__anonymous__] [--min-latency-ms N] [--limit N] [--interval D] [--once]
//	gregale debug requests get <slug> <req_id>
//	gregale debug requests show <slug> <req_id>
//	gregale debug requests evidence <slug> <req_id>
//	gregale debug requests replay <slug> <req_id>
//	gregale debug coverage <slug> [--since <dur>]
//	gregale debug running <slug> [--since <dur>] [--limit <n>]
//	gregale debug bundle <slug> <req_id> [--since <dur>] [--source <id> --mirror <id>] [--output PATH]
//	gregale debug regressions watch <slug> [--since <dur>] [--interval D] [--once]
//	gregale debug regressions <slug> [--since <dur>]
//	gregale debug regressions --all [--since <dur>]
//	gregale debug regressions <acknowledge|dismiss|resolve|reopen> <slug> --deployment-id UUID --route P [--dismissed-until RFC3339]
//	gregale debug regressions rollback <slug> [--to <deployment_id>] --yes
//	gregale debug compare <slug> --source <id> --mirror <id> [--route <pattern>] [--since <dur>] [--until <timestamp>]
//
// The request verbs are direct API lookups: "get" returns metadata,
// "evidence" returns bounded span evidence plus the deterministic
// explanation, and "replay" queues metadata-only execution through the
// request's enabled mirror rule.

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// debugCmdUsage is the canonical usage text. Mirrors the shape of
// commands_invocations.go's PrintUsage strings.
const debugCmdUsage = "usage: gregale debug <requests|coverage|running|regressions|compare|bundle> ..."

const debugRequestsCmdUsage = "usage: gregale debug requests <list|export|watch|get|show|evidence|replay> ..."

// debugCmdDocsTopic is the docs topic slug for the debug
// namespace. Resolves to cli_meta.go's "debug" cliCommand entry;
// TestUsageDocSlugParity scans PrintUsage calls for this string.
const debugCmdDocsTopic = "debug"

func cmdDebug(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, debugCmdUsage, debugCmdDocsTopic)
		return 1
	}
	if args[0] == "--help" || args[0] == "-h" {
		PrintUsage(os.Stderr, debugCmdUsage+"\n\n  requests list     list recent request telemetry\n  requests watch    watch request telemetry for new or changed rows\n  requests export   export metadata-only request telemetry\n  requests get      show one request's metadata\n  requests show     show request timeline and evidence\n  requests evidence show request evidence and explanation\n  requests replay   queue a request replay\n  coverage          show observed debugger signal coverage\n  running           explain why an app is still running\n  regressions       list detected regressions (use --all for every app)\n  regressions watch watch live regression events (--poll for polling)\n  regressions acknowledge|dismiss|resolve|reopen change regression triage state\n  compare           compare two deployments\n  bundle            export a redacted incident bundle with coverage", debugCmdDocsTopic)
		return 0
	}
	switch args[0] {
	case "requests":
		return cmdDebugRequests(args[1:])
	case "coverage":
		return cmdDebugCoverage(args[1:])
	case "running":
		return cmdDebugRunning(args[1:])
	case "regressions":
		return cmdDebugRegressions(args[1:])
	case "compare":
		return cmdDebugCompare(args[1:])
	case "bundle":
		return cmdDebugBundle(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown debug subcommand %q\n", args[0])
	return 1
}

// cmdDebugCoverage renders the observed debugger signal coverage for a slug.
// The human form makes the distinction between aggregate rows and represented
// requests explicit; --json is the stable automation format.
func cmdDebugCoverage(args []string) int {
	fs := newFlagSet("debug coverage", flag.ContinueOnError)
	since := fs.String("since", "", "lookback window (e.g. 30m, 24h, 3d)")
	flagArgs, positional := normalizeDebugFlagArgs(args, map[string]bool{"since": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if len(positional) != 1 {
		PrintUsage(os.Stderr, "usage: gregale debug coverage [--since D] <slug>", debugCmdDocsTopic)
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetAppDebugCoverage(context.Background(), positional[0], *since)
	if err != nil {
		return printErr("Could not get debug coverage", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderDebugCoverage(osStdout, resp)
	return 0
}

func cmdDebugRequests(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, debugRequestsCmdUsage, debugCmdDocsTopic)
		return 1
	}
	if args[0] == "--help" || args[0] == "-h" {
		PrintUsage(os.Stderr, debugRequestsCmdUsage+"\n\n  list      list recent request telemetry\n  export    export metadata-only request telemetry\n  watch     watch request telemetry for new or changed rows\n  get       show one request's metadata\n  show      show request timeline and evidence\n  evidence  show request evidence and explanation\n  replay    queue a request replay", debugCmdDocsTopic)
		return 0
	}
	switch args[0] {
	case subList:
		return cmdDebugRequestsList(args[1:])
	case "export":
		return cmdDebugRequestsExport(args[1:])
	case "watch":
		return cmdDebugRequestsWatch(args[1:])
	case "get":
		return cmdDebugRequestsGet(args[1:])
	case "show":
		return cmdDebugRequestsEvidence(args[1:])
	case "evidence":
		return cmdDebugRequestsEvidence(args[1:])
	case "replay":
		return cmdDebugRequestsReplay(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown debug requests subcommand %q\n", args[0])
	return 1
}

// cmdDebugRequestsEvidence renders bounded span evidence and the server's
// deterministic explanation for one request. Human output is the default;
// --json remains the stable machine-readable representation.
func cmdDebugRequestsEvidence(args []string) int {
	if len(args) != 2 {
		PrintUsage(os.Stderr, "usage: gregale debug requests evidence <slug> <req_id>", debugCmdDocsTopic)
		return 1
	}
	slug, reqID := args[0], args[1]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetAppDebugRequestEvidence(context.Background(), slug, reqID)
	if err != nil {
		return printErr("Could not get debug request evidence", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderDebugRequestEvidence(osStdout, resp)
	return 0
}

// cmdDebugRequestsList renders the recent request telemetry for
// a slug. PR-A's ListAppDebugRequests backs this verb.
func cmdDebugRequestsList(args []string) int {
	fs := newFlagSet("debug requests list", flag.ContinueOnError)
	since := fs.String("since", "", "lookback window (e.g. 30m, 24h, 3d)")
	route := fs.String("route", "", "route filter (exact match)")
	deploymentID := fs.String("deployment-id", "", "deployment UUID filter")
	status := fs.Int("status", 0, "exact HTTP status filter (100..599)")
	coldBoot := fs.String("cold-boot", "", "cold-start filter (true or false)")
	consumerID := fs.String("consumer-id", "", "consumer UUID or __anonymous__")
	minLatencyMS := fs.Int("min-latency-ms", 0, "minimum latency bucket in milliseconds")
	cursor := fs.String("cursor", "", "opaque cursor from the previous page")
	limit := fs.Int("limit", 20, "max rows (1..200)")
	all := fs.Bool("all", false, "walk every retained page")
	flagArgs, positional := normalizeDebugFlagArgs(args, map[string]bool{
		"since": true, "route": true, "deployment-id": true, "status": true,
		"cold-boot": true, "consumer-id": true, "min-latency-ms": true,
		"cursor": true, "limit": true, "all": false,
	})
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if len(positional) != 1 {
		PrintUsage(os.Stderr, "usage: gregale debug requests list [--all] [--since D] [--route P] [--deployment-id UUID] [--status N] [--cold-boot true|false] [--consumer-id UUID|__anonymous__] [--min-latency-ms N] [--cursor C] [--limit N] <slug>", debugCmdDocsTopic)
		return 1
	}
	if *limit < 1 || *limit > 200 {
		fmt.Fprintln(os.Stderr, "--limit must be between 1 and 200")
		return 1
	}
	options, err := debugTelemetryOptionsFromFlags(*since, *route, *deploymentID, *status, *coldBoot, *consumerID, *minLatencyMS, *cursor, *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	slug := positional[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if *all {
		requests, err := client.ListAppDebugRequestsAll(ctx, slug, options)
		if err != nil {
			return printErr("Could not list all debug requests", err)
		}
		if jsonOutput {
			return jsonOut(writeNDJSON(requests))
		}
		renderDebugRequestsTable(osStdout, api.DebugTelemetryListResponse{
			Since: *since, Complete: true, Requests: requests,
		})
		return 0
	}
	resp, err := client.ListAppDebugRequestsWithOptions(ctx, slug, options)
	if err != nil {
		return printErr("Could not list debug requests", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderDebugRequestsTable(osStdout, resp)
	return 0
}

// cmdDebugRequestsExport writes the bounded metadata-only export already
// exposed by the API. A file is owner-readable so an exported request log is
// not accidentally left world-readable on a shared machine.
func cmdDebugRequestsExport(args []string) int {
	fs := newFlagSet("debug requests export", flag.ContinueOnError)
	since := fs.String("since", "", "lookback window (e.g. 30m, 24h, 3d)")
	route := fs.String("route", "", "route filter (exact match)")
	format := fs.String("format", "ndjson", "export format (ndjson or csv)")
	limit := fs.Int("limit", 10000, "max rows (1..10000)")
	output := fs.String("output", "", "write to PATH instead of stdout (use - for stdout)")
	flagArgs, positional := normalizeDebugFlagArgs(args, map[string]bool{
		"since": true, "route": true, "format": true, "limit": true, "output": true,
	})
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if len(positional) != 1 {
		PrintUsage(os.Stderr, "usage: gregale debug requests export [--since D] [--route P] [--format ndjson|csv] [--limit N] [--output PATH] <slug>", debugCmdDocsTopic)
		return 1
	}
	if *format != "ndjson" && *format != "csv" {
		return printErr("Invalid export format", fmt.Errorf("--format must be ndjson or csv"))
	}
	if *limit < 1 || *limit > 10000 {
		return printErr("Invalid export limit", fmt.Errorf("--limit must be between 1 and 10000"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	data, err := client.ExportAppDebugRequests(context.Background(), positional[0], api.DebugTelemetryExportOptions{
		Since: *since, Route: *route, Format: *format, Limit: *limit,
	})
	if err != nil {
		return printErr("Could not export debug requests", err)
	}
	if *output == "" || *output == "-" {
		_, err = osStdout.Write(data)
		if err != nil {
			return printErr("Could not write debug request export", err)
		}
		return 0
	}
	if err := writeDebugExportFile(*output, data); err != nil {
		return printErr("Could not write debug request export", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{
			"path": *output, "format": *format, "bytes": len(data),
		}))
	}
	PrintOK(osStdout, "Wrote %s debug request export (%d bytes) to %s.", *format, len(data), *output)
	return 0
}

func writeDebugExportFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// debugTelemetryOptionsFromFlags converts the CLI's optional string form for
// cold_boot into the pointer form used by the SDK, preserving false versus an
// omitted filter. The server repeats UUID validation so scripts get the same
// canonical validation even when they bypass the CLI.
func debugTelemetryOptionsFromFlags(since, route, deploymentID string, status int, coldBoot, consumerID string, minLatencyMS int, cursor string, limit int) (api.DebugTelemetryListOptions, error) {
	if status != 0 && (status < 100 || status > 599) {
		return api.DebugTelemetryListOptions{}, fmt.Errorf("--status must be between 100 and 599")
	}
	if minLatencyMS < 0 || minLatencyMS > 86_400_000 {
		return api.DebugTelemetryListOptions{}, fmt.Errorf("--min-latency-ms must be between 0 and 86400000")
	}
	var coldBootValue *bool
	if raw := strings.TrimSpace(coldBoot); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return api.DebugTelemetryListOptions{}, fmt.Errorf("--cold-boot must be true or false")
		}
		coldBootValue = &value
	}
	return api.DebugTelemetryListOptions{
		Since: since, Route: route, DeploymentID: deploymentID, Status: status,
		ColdBoot: coldBootValue, ConsumerID: consumerID, MinLatencyMS: minLatencyMS,
		Cursor: cursor, Limit: limit,
	}, nil
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
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderDebugRequestMetadata(osStdout, resp)
	return 0
}

// cmdDebugRequestsReplay queues a metadata-only replay through the request's
// enabled mirror rule. The response includes the durable invocation ID.
func cmdDebugRequestsReplay(args []string) int {
	fs := newFlagSet("debug requests replay", flag.ContinueOnError)
	wait := fs.Bool("wait", false, "wait for the replay invocation to reach a terminal state")
	timeout := fs.Duration("timeout", time.Minute, "maximum wait time (1s..1h)")
	interval := fs.Duration("interval", time.Second, "poll interval (250ms..1m)")
	flagArgs, positional := normalizeDebugFlagArgs(args, map[string]bool{
		"wait": false, "timeout": true, "interval": true,
	})
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if len(positional) != 2 {
		PrintUsage(os.Stderr, "usage: gregale debug requests replay [--wait] [--timeout D] [--interval D] <slug> <req_id>", debugCmdDocsTopic)
		return 1
	}
	if *timeout < time.Second || *timeout > time.Hour {
		return printErr("Invalid replay timeout", fmt.Errorf("--timeout must be between 1s and 1h"))
	}
	if *interval < 250*time.Millisecond || *interval > time.Minute {
		return printErr("Invalid replay interval", fmt.Errorf("--interval must be between 250ms and 1m"))
	}
	slug, reqID := positional[0], positional[1]
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
	if !*wait {
		if jsonOutput {
			return jsonOut(writeJSON(resp))
		}
		renderDebugReplayQueued(osStdout, resp)
		return 0
	}
	invocation, err := waitForDebugReplay(context.Background(), client, resp.MirrorInvocationID, *timeout, *interval)
	if err != nil {
		return printErr("Could not wait for debug replay", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{
			"replay": resp, "invocation": invocation,
		}))
	}
	renderDebugReplayQueued(osStdout, resp)
	_, _ = fmt.Fprintln(osStdout)
	renderInvocation(osStdout, invocation)
	if invocation.State == "failed" || invocation.State == "dead_letter" {
		return 3
	}
	if invocation.State == "cancelled" {
		return 1
	}
	return 0
}

func renderDebugReplayQueued(w io.Writer, resp api.DebugReplayResponse) {
	_, _ = fmt.Fprintf(w, "Replay queued: %s\n", resp.MirrorInvocationID)
	_, _ = fmt.Fprintf(w, "Status:        %s\n", resp.Status)
	_, _ = fmt.Fprintf(w, "Poll with:     gregale invocations get %s\n", resp.MirrorInvocationID)
}

func waitForDebugReplay(ctx context.Context, client *api.Client, id string, timeout, interval time.Duration) (api.Invocation, error) {
	if id == "" {
		return api.Invocation{}, fmt.Errorf("replay response did not include an invocation id")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		invocation, err := client.GetInvocation(ctx, id)
		if err != nil {
			return api.Invocation{}, err
		}
		if debugInvocationTerminal(invocation.State) {
			return invocation, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if ctx.Err() == context.DeadlineExceeded {
				return api.Invocation{}, fmt.Errorf("timed out after %s waiting for invocation %s", timeout, id)
			}
			return api.Invocation{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func debugInvocationTerminal(state string) bool {
	switch state {
	case "completed", "failed", "dead_letter", "cancelled":
		return true
	default:
		return false
	}
}

// cmdDebugRegressions renders the active regression observations
// for a slug. Powers the dashboard regression banner feed.
func cmdDebugRegressions(args []string) int {
	if len(args) > 0 && args[0] == "rollback" {
		return cmdDebugRegressionRollback(args[1:])
	}
	if len(args) > 0 && args[0] == "watch" {
		return cmdDebugRegressionsWatch(args[1:])
	}
	if len(args) > 0 {
		switch args[0] {
		case "ack", "acknowledge", "dismiss", "resolve", "reopen":
			return cmdDebugRegressionAction(args[1:], args[0])
		}
	}
	fs := newFlagSet("debug regressions", flag.ContinueOnError)
	since := fs.String("since", "", "lookback window (e.g. 30m, 24h, 3d)")
	all := fs.Bool("all", false, "list regressions for every app in the account")
	flagArgs, positional := normalizeDebugFlagArgs(args, map[string]bool{"since": true, "all": false})
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if (*all && len(positional) != 0) || (!*all && len(positional) != 1) {
		PrintUsage(os.Stderr, "usage: gregale debug regressions [--all] [--since D] [<slug>]", debugCmdDocsTopic)
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *all {
		return cmdDebugRegressionsAll(context.Background(), client, *since)
	}
	slug := positional[0]
	resp, err := client.ListAppDebugRegressions(context.Background(), slug, *since)
	if err != nil {
		return printErr("Could not list regressions", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderDebugRegressionsTable(osStdout, resp)
	return 0
}

// cmdDebugRegressionAction changes the triage state of one regression. The
// deployment and route form the observation key, so actions stay precise even
// when several routes regress on the same deployment.
func cmdDebugRegressionAction(args []string, action string) int {
	if action == "ack" {
		action = "acknowledge"
	}
	fs := newFlagSet("debug regressions "+action, flag.ContinueOnError)
	deploymentID := fs.String("deployment-id", "", "regression deployment UUID")
	route := fs.String("route", "", "regression route")
	dismissedUntil := fs.String("dismissed-until", "", "dismissal expiry (RFC3339; default 24h)")
	flagArgs, positional := normalizeDebugFlagArgs(args, map[string]bool{
		"deployment-id": true, "route": true, "dismissed-until": true,
	})
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if len(positional) != 1 {
		PrintUsage(os.Stderr, "usage: gregale debug regressions "+action+" [--deployment-id UUID] [--route P] [--dismissed-until RFC3339] <slug>", debugCmdDocsTopic)
		return 1
	}
	if *deploymentID == "" || *route == "" {
		return printErr("Incomplete debugger regression key", fmt.Errorf("--deployment-id and --route are required"))
	}
	if action != "dismiss" && *dismissedUntil != "" {
		return printErr("Invalid dismissal expiry", fmt.Errorf("--dismissed-until is only valid with dismiss"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.UpdateAppDebugRegression(context.Background(), positional[0], api.DebugRegressionActionRequest{
		DeploymentID:   *deploymentID,
		Route:          *route,
		Action:         action,
		DismissedUntil: *dismissedUntil,
	})
	if err != nil {
		return printErr("Could not update debugger regression", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Regression %s for %s is now %s.", resp.Regression.Route, positional[0], resp.Regression.State)
	return 0
}

// cmdDebugRegressionRollback makes the remediation step discoverable from
// the debugger while retaining the existing rollback API and its explicit
// confirmation guard. Without --to the platform selects its normal rollback
// target; --to is useful when the regression table identifies the intended
// prior deployment directly.
func cmdDebugRegressionRollback(args []string) int {
	fs := newFlagSet("debug regressions rollback", flag.ContinueOnError)
	to := fs.String("to", "", "target superseded deployment id (default: most recent)")
	yes := fs.Bool("yes", false, "confirm the rollback")
	flagArgs, positional := normalizeDebugFlagArgs(args, map[string]bool{"to": true, "yes": false})
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if len(positional) != 1 {
		PrintUsage(os.Stderr, "usage: gregale debug regressions rollback [--to DEPLOYMENT_ID] --yes <slug>", debugCmdDocsTopic)
		return 1
	}
	if !*yes {
		return printErr("Rollback not confirmed", fmt.Errorf("pass --yes to roll back from the debugger"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	deployment, err := client.RollbackTo(context.Background(), positional[0], *to)
	if err != nil {
		return printErr("Debugger rollback failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(deployment))
	}
	if *to != "" {
		PrintOK(osStdout, "Rolled back %s to %s via debugger.", positional[0], *to)
	} else {
		PrintOK(osStdout, "Rolled back %s to %s via debugger.", positional[0], deployment.ID)
	}
	return 0
}

// cmdDebugCompare renders the per-route compare between two
// deployments. POSTs the body shape and renders the merged
// per-route stats.
func cmdDebugCompare(args []string) int {
	fs := newFlagSet("debug compare", flag.ContinueOnError)
	since := fs.String("since", "", "lookback window (e.g. 30m, 24h, 3d)")
	until := fs.String("until", "", "end of comparison window (RFC3339; default now)")
	route := fs.String("route", "", "route filter (exact match)")
	source := fs.String("source", "", "source deployment id")
	mirror := fs.String("mirror", "", "mirror deployment id")
	flagArgs, positional := normalizeDebugFlagArgs(args, map[string]bool{
		"since":  true,
		"until":  true,
		"route":  true,
		"source": true,
		"mirror": true,
	})
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if len(positional) != 1 || *source == "" || *mirror == "" {
		PrintUsage(os.Stderr, "usage: gregale debug compare --source <id> --mirror <id> [--route P] [--since D] [--until RFC3339] <slug>", debugCmdDocsTopic)
		return 1
	}
	if *until != "" {
		if _, err := time.Parse(time.RFC3339, *until); err != nil {
			return printErr("Invalid comparison end", fmt.Errorf("--until must be an RFC3339 timestamp"))
		}
	}
	slug := positional[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.CompareAppDebugDeployments(context.Background(), slug, *source, *mirror, *route, *since, *until)
	if err != nil {
		return printErr("Could not compare deployments", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderDebugCompareTable(osStdout, resp)
	return 0
}

func renderDebugRequestsTable(w io.Writer, resp api.DebugTelemetryListResponse) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tROUTE\tMETHOD\tSTATUS\tLATENCY_MS\tCOUNT\tCOLD\tCONSUMER\tRECEIVED_AT")
	for _, r := range resp.Requests {
		cold := ""
		if r.ColdBoot {
			cold = "yes"
		}
		consumer := r.ConsumerID
		if consumer == "" {
			consumer = "anonymous"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%s\t%s\t%s\n",
			r.ID, r.Route, r.Method, r.Status, r.LatencyMS, r.Count, cold, consumer, r.ReceivedAt)
	}
	_ = tw.Flush()
	if resp.RetentionClamped {
		_, _ = fmt.Fprintln(w, "window clamped to the plan's telemetry retention")
	}
	if resp.Complete {
		_, _ = fmt.Fprintln(w, "page complete for the retained window")
	} else if resp.NextCursor != "" {
		_, _ = fmt.Fprintf(w, "more rows available; next_cursor=%s\n", resp.NextCursor)
	}
}

func renderDebugRequestMetadata(w io.Writer, r api.DebugTelemetryRequestItem) {
	_, _ = fmt.Fprintf(w, "Request:    %s\n", r.ID)
	_, _ = fmt.Fprintf(w, "Route:      %s %s\n", r.Method, r.Route)
	_, _ = fmt.Fprintf(w, "Status:     %d\n", r.Status)
	_, _ = fmt.Fprintf(w, "Latency:    %d ms\n", r.LatencyMS)
	_, _ = fmt.Fprintf(w, "Count:      %d\n", r.Count)
	_, _ = fmt.Fprintf(w, "Deployment: %s\n", r.DeploymentID)
	if r.InstanceID != "" {
		_, _ = fmt.Fprintf(w, "Instance:   %s\n", r.InstanceID)
	}
	if r.WakeID != "" {
		_, _ = fmt.Fprintf(w, "Wake:       %s\n", r.WakeID)
	}
	if r.TraceID != nil && *r.TraceID != "" {
		_, _ = fmt.Fprintf(w, "Trace:      %s\n", *r.TraceID)
	}
	if r.ConsumerID != "" {
		_, _ = fmt.Fprintf(w, "Consumer:   %s\n", r.ConsumerID)
	}
	if r.ColdBoot {
		_, _ = fmt.Fprintln(w, "Cold boot:  yes")
	}
	if r.Guest != nil {
		_, _ = fmt.Fprintf(w, "Guest:      %s · %d ms · %s\n", r.Guest.Runtime, r.Guest.DurationMS, r.Guest.Outcome)
	}
	_, _ = fmt.Fprintf(w, "Received:   %s\n", r.ReceivedAt)
}

func renderDebugCoverage(w io.Writer, resp api.DebugCoverageResponse) {
	_, _ = fmt.Fprintf(w, "Debugger coverage · window %s → %s\n", resp.WindowStart, resp.WindowEnd)
	_, _ = fmt.Fprintf(w, "app %s · since %s · plan retention %d days\n", resp.AppID, resp.Since, resp.PlanRetentionDays)
	_, _ = fmt.Fprintf(w, "telemetry rows: %d · represented requests: %d · errors: %d\n", resp.TelemetryRows, resp.RepresentedRequests, resp.ErrorRequests)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SIGNAL\tROWS\tREQUESTS\tRATE")
	for _, item := range []struct {
		name   string
		signal api.DebugCoverageSignal
	}{
		{name: "trace linked", signal: resp.TraceLinked},
		{name: "span evidence", signal: resp.SpanEvidence},
		{name: "wake evidence", signal: resp.WakeEvidence},
		{name: "guest evidence", signal: resp.GuestEvidence},
	} {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%.1f%%\n", item.name, item.signal.Rows, item.signal.Requests, item.signal.RatePct)
	}
	_ = tw.Flush()
	if resp.OldestTelemetryAt == "" {
		_, _ = fmt.Fprintln(w, "observed range: no telemetry in this window")
	} else {
		_, _ = fmt.Fprintf(w, "observed range: %s → %s\n", resp.OldestTelemetryAt, resp.LatestTelemetryAt)
	}
}

// renderDebugRequestEvidence keeps the request investigation loop useful
// without requiring jq. It deliberately renders only the bounded fields
// returned by the evidence endpoint; request payloads and raw attributes are
// not part of this surface.
func renderDebugRequestEvidence(w io.Writer, resp api.DebugRequestEvidenceResponse) {
	r := resp.Request
	_, _ = fmt.Fprintf(w, "%s %s · HTTP %d · %d ms\n", r.Method, r.Route, r.Status, r.LatencyMS)
	_, _ = fmt.Fprintf(w, "request %s", r.ID)
	if r.DeploymentID != "" {
		_, _ = fmt.Fprintf(w, " · deployment %s", r.DeploymentID)
	}
	if r.InstanceID != "" {
		_, _ = fmt.Fprintf(w, " · instance %s", r.InstanceID)
	}
	if r.WakeID != "" {
		_, _ = fmt.Fprintf(w, " · wake %s", r.WakeID)
	}
	if r.TraceID != nil && *r.TraceID != "" {
		_, _ = fmt.Fprintf(w, " · trace %s", *r.TraceID)
	}
	_, _ = fmt.Fprintln(w)
	if r.ReceivedAt != "" {
		_, _ = fmt.Fprintf(w, "received %s", r.ReceivedAt)
		if r.Count > 1 {
			_, _ = fmt.Fprintf(w, " · collapsed count %d", r.Count)
		}
		_, _ = fmt.Fprintln(w)
	}
	if r.ColdBoot {
		_, _ = fmt.Fprintln(w, "signal cold boot")
	}
	if r.Guest != nil {
		_, _ = fmt.Fprintf(w, "guest: %s · %d ms · %s", r.Guest.Runtime, r.Guest.DurationMS, r.Guest.Outcome)
		if r.Guest.ErrorClass != "" {
			_, _ = fmt.Fprintf(w, " · error %s", r.Guest.ErrorClass)
		}
		_, _ = fmt.Fprintln(w)
	}
	if headline := strings.TrimSpace(resp.Explanation.Headline); headline != "" {
		_, _ = fmt.Fprintf(w, "explanation: %s\n", headline)
	}

	if len(resp.Timeline) == 0 {
		_, _ = fmt.Fprintln(w, "timeline: no retained markers")
	} else {
		_, _ = fmt.Fprintln(w, "TIMELINE")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "AT\tPHASE\tEVENT\tDETAILS")
		for _, event := range resp.Timeline {
			at := event.At
			if event.Approximate {
				at = "~" + at
			}
			details := event.Summary
			if event.DurationMS > 0 {
				details += fmt.Sprintf(" · %d ms", event.DurationMS)
			}
			if event.Status > 0 {
				details += fmt.Sprintf(" · HTTP %d", event.Status)
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", at, event.Phase, event.Kind, details)
		}
		_ = tw.Flush()
	}

	if len(resp.Correlation.Stages) > 0 {
		_, _ = fmt.Fprintln(w, "CORRELATION")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "PHASE\tSTATUS\tDURATION_MS\tEVIDENCE\tDETAILS")
		for _, stage := range resp.Correlation.Stages {
			duration := "-"
			if stage.DurationMS > 0 {
				duration = fmt.Sprintf("%d", stage.DurationMS)
			}
			evidence := "-"
			if stage.EvidenceCount > 0 {
				evidence = fmt.Sprintf("%d", stage.EvidenceCount)
			}
			details := stage.Reason
			if details == "" {
				details = "-"
			}
			if stage.Approximate {
				details = "~ " + details
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", stage.Phase, stage.Status, duration, evidence, details)
		}
		_ = tw.Flush()
		if !resp.Correlation.Complete {
			_, _ = fmt.Fprintln(w, "correlation incomplete: missing or partial stages are shown above")
		}
	}

	if len(resp.Spans) == 0 {
		_, _ = fmt.Fprintln(w, "span evidence: no linked OTel spans")
	} else {
		_, _ = fmt.Fprintln(w, "SPAN EVIDENCE")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "SPAN\tKIND\tDURATION_MS\tSTATUS\tDB_FINGERPRINT")
		for _, span := range resp.Spans {
			status := span.Status
			if status == "" {
				status = "-"
			}
			dbFingerprint := span.DBStatement
			if dbFingerprint == "" {
				dbFingerprint = "-"
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", span.Name, span.Kind, span.DurationNanos/1_000_000, status, dbFingerprint)
		}
		_ = tw.Flush()
		if resp.SpansTruncated {
			_, _ = fmt.Fprintln(w, "span evidence truncated to the slowest retained spans")
		}
	}
	if resp.GeneratedAt != "" {
		_, _ = fmt.Fprintf(w, "evidence generated %s\n", resp.GeneratedAt)
	}
}

// normalizeDebugFlagArgs lets the debug commands accept a slug either before
// or after flags. The standard flag package stops parsing at the first
// positional argument, which made the documented `... <slug> --since ...`
// form silently ignore filters. Only known value-taking flags consume the
// following argument; unknown flags are left for flag.FlagSet to report.
func normalizeDebugFlagArgs(args []string, valueFlags map[string]bool) (flagArgs, positional []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flagArgs = append(flagArgs, arg)
			name := strings.TrimLeft(arg, "-")
			if strings.IndexByte(name, '=') < 0 && valueFlags[name] && i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
			continue
		}
		positional = append(positional, arg)
	}
	return flagArgs, positional
}

func renderDebugRegressionsTable(w io.Writer, resp api.DebugRegressionsResponse) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "DEPLOYMENT\tROUTE\tSTATE\tFACTOR\tP95_MS\tP95_BASE_MS\tAFFECTED\tLAST_DETECTED")
	for _, r := range resp.Regressions {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
			r.DeploymentID, r.Route, r.State, r.Factor, r.P95MS, r.P95BaseMS, r.AffectedCount, r.LastDetectedAt)
	}
	_ = tw.Flush()
}

func renderDebugCompareTable(w io.Writer, resp api.DebugCompareResponse) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "Source: %s\n", resp.Source)
	_, _ = fmt.Fprintf(tw, "Mirror: %s\n", resp.Mirror)
	_, _ = fmt.Fprintln(tw, "ROUTE\tSOURCE_P50\tSOURCE_P95\tSOURCE_P99\tSOURCE_N\tMIRROR_P50\tMIRROR_P95\tMIRROR_P99\tMIRROR_N\tP95_DELTA")
	for _, r := range resp.Routes {
		delta := r.MirrorP95 - r.SourceP95
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%+d\n",
			r.Route, r.SourceP50, r.SourceP95, r.SourceP99, r.SourceN,
			r.MirrorP50, r.MirrorP95, r.MirrorP99, r.MirrorN, delta)
	}
	_ = tw.Flush()
}
