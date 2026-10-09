package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

const syntheticsUsage = "usage: gregale synthetics <list|create|status|pause|resume|rm> --app <slug>"

// cmdSynthetics implements `gregale synthetics` (ADR-748): scheduled HTTP
// checks against an app's own hostname.
func cmdSynthetics(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, syntheticsUsage, "synthetics")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdSyntheticsList(args[1:])
	case "create":
		return cmdSyntheticsCreate(args[1:])
	case "pause":
		return cmdSyntheticsSetEnabled(args[1:], false)
	case "resume":
		return cmdSyntheticsSetEnabled(args[1:], true)
	case subRm:
		return cmdSyntheticsRm(args[1:])
	case "status":
		return cmdSyntheticsStatus(args[1:])
	}
	printCommandValidation(os.Stderr, "unknown synthetics subcommand %q\n", args[0])
	PrintUsage(os.Stderr, syntheticsUsage, "synthetics")
	return 1
}

func cmdSyntheticsList(args []string) int {
	fs := newFlagSet("synthetics list", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale synthetics list --app <slug>", "synthetics")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	checks, err := client.ListSyntheticChecks(context.Background(), *slug)
	if err != nil {
		return printErr("Could not list synthetic checks", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(checks))
	}
	renderSyntheticList(osStdout, checks)
	return 0
}

func renderSyntheticList(w io.Writer, checks []api.SyntheticCheckResponse) {
	if len(checks) == 0 {
		_, _ = fmt.Fprintln(w, "No synthetic checks. Create one with `gregale synthetics create`.")
		return
	}
	_, _ = fmt.Fprintf(w, "%-36s  %-20s  %-6s  %-8s  %-7s  %s\n", "ID", "NAME", "EVERY", "EXPECT", "STATE", "REQUEST")
	for _, c := range checks {
		state := "active"
		if !c.Enabled {
			state = "paused"
		}
		_, _ = fmt.Fprintf(w, "%-36s  %-20s  %-6s  %-8s  %-7s  %s %s\n", c.ID, c.Name, fmt.Sprintf("%dm", c.IntervalSeconds/60), describeExpected(c.ExpectedStatus), state, c.Method, c.URL)
	}
}

func describeExpected(status int) string {
	if status == 0 {
		return "2xx"
	}
	return fmt.Sprint(status)
}

func cmdSyntheticsCreate(args []string) int {
	fs := newFlagSet("synthetics create", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	name := fs.String("name", "", "check name (required)")
	path := fs.String("path", "/", "path on the app, e.g. /healthz")
	method := fs.String("method", "GET", "GET or HEAD")
	expect := fs.Int("expect-status", 0, "exact expected status (default: any 2xx)")
	timeout := fs.Int("timeout-ms", api.SyntheticCheckDefaultTimeoutMS, "request timeout in ms, including any wake")
	every := fs.Int("every-minutes", 5, "interval in minutes (5, 15 or 60)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *name == "" || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale synthetics create --app <slug> --name <name> [--path /healthz] [--method GET|HEAD] [--expect-status N] [--timeout-ms N] [--every-minutes 5|15|60]", "synthetics")
		return 1
	}
	req, prob := api.NormalizeCreateSyntheticCheck(api.CreateSyntheticCheckRequest{
		Name: *name, Method: *method, Path: *path, ExpectedStatus: *expect, TimeoutMS: *timeout, IntervalSeconds: *every * 60,
	})
	if prob != nil {
		return printErr("Invalid synthetic check", fmt.Errorf("%s", prob.Detail))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	check, err := client.CreateSyntheticCheck(context.Background(), *slug, req)
	if err != nil {
		return printErr("Could not create synthetic check", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(check))
	}
	_, _ = fmt.Fprintf(osStdout, "Created check %s (%s): %s %s every %d minutes, expecting %s\n", check.Name, check.ID, check.Method, check.URL, check.IntervalSeconds/60, describeExpected(check.ExpectedStatus))
	_, _ = fmt.Fprintln(osStdout, "Each probe is a normal request; one that wakes a parked app is billed like any other.")
	return 0
}

func cmdSyntheticsSetEnabled(args []string, enabled bool) int {
	verb := map[bool]string{true: "resume", false: "pause"}[enabled]
	fs := newFlagSet("synthetics "+verb, flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale synthetics "+verb+" --app <slug> <check-id>", "synthetics")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	check, err := client.UpdateSyntheticCheck(context.Background(), *slug, fs.Arg(0), api.UpdateSyntheticCheckRequest{Enabled: enabled})
	if err != nil {
		return printErr("Could not "+verb+" synthetic check", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(check))
	}
	_, _ = fmt.Fprintf(osStdout, "Check %s %sd\n", check.Name, verb)
	return 0
}

func cmdSyntheticsRm(args []string) int {
	fs := newFlagSet("synthetics rm", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale synthetics rm --app <slug> <check-id>", "synthetics")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteSyntheticCheck(context.Background(), *slug, fs.Arg(0)); err != nil {
		return printErr("Could not delete synthetic check", err)
	}
	_, _ = fmt.Fprintf(osStdout, "Deleted check %s\n", fs.Arg(0))
	return 0
}
