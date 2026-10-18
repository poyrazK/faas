package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

const slosUsage = "usage: gregale slos <list|create|status|rm> --app <slug>"

// cmdSLOs implements `gregale slos` (ADR-747): customer-defined SLO
// definitions. Distinct from `gregale slo <slug>`, ADR-082's fixed panel.
func cmdSLOs(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, slosUsage, "slos")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdSLOsList(args[1:])
	case "create":
		return cmdSLOsCreate(args[1:])
	case subRm:
		return cmdSLOsRm(args[1:])
	case "status":
		return cmdSLOsStatus(args[1:])
	}
	printCommandValidation(os.Stderr, "unknown slos subcommand %q\n", args[0])
	PrintUsage(os.Stderr, slosUsage, "slos")
	return 1
}

func cmdSLOsList(args []string) int {
	fs := newFlagSet("slos list", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale slos list --app <slug>", "slos")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	slos, err := client.ListSLOs(context.Background(), *slug)
	if err != nil {
		return printErr("Could not list SLOs", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(slos))
	}
	renderSLOList(osStdout, slos)
	return 0
}

func renderSLOList(w io.Writer, slos []api.SLOResponse) {
	if len(slos) == 0 {
		_, _ = fmt.Fprintln(w, "No SLOs defined. Create one with `gregale slos create`.")
		return
	}
	_, _ = fmt.Fprintf(w, "%-36s  %-24s  %-26s  %s\n", "ID", "NAME", "OBJECTIVE", "WINDOW")
	for _, slo := range slos {
		_, _ = fmt.Fprintf(w, "%-36s  %-24s  %-26s  %dd\n", slo.ID, slo.Name, describeSLOObjective(slo), slo.WindowDays)
	}
}

// describeSLOObjective renders "99.9% available" or "99.5% under 250ms".
func describeSLOObjective(slo api.SLOResponse) string {
	if slo.SLI == "latency" {
		return fmt.Sprintf("%g%% under %dms", slo.ObjectivePct, slo.LatencyThresholdMS)
	}
	return fmt.Sprintf("%g%% available", slo.ObjectivePct)
}

func cmdSLOsCreate(args []string) int {
	fs := newFlagSet("slos create", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	name := fs.String("name", "", "SLO name (required)")
	sli := fs.String("sli", "availability", "availability or latency")
	threshold := fs.Int("latency-threshold-ms", 0, "latency threshold in ms (latency SLI only)")
	objective := fs.Float64("objective", 0, "objective percentage, e.g. 99.9 (required)")
	window := fs.Int("window-days", 30, "rolling window in days (7 or 30)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *name == "" || *objective == 0 || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale slos create --app <slug> --name <name> --objective <pct> [--sli availability|latency --latency-threshold-ms N] [--window-days 7|30]", "slos")
		return 1
	}
	req := api.CreateSLORequest{Name: *name, SLI: *sli, LatencyThresholdMS: *threshold, ObjectivePct: *objective, WindowDays: *window}
	if _, prob := api.ValidateCreateSLO(req); prob != nil {
		return printErr("Invalid SLO", fmt.Errorf("%s", prob.Detail))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	slo, err := client.CreateSLO(context.Background(), *slug, req)
	if err != nil {
		return printErr("Could not create SLO", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(slo))
	}
	_, _ = fmt.Fprintf(osStdout, "Created SLO %s (%s): %s over %d days\n", slo.Name, slo.ID, describeSLOObjective(slo), slo.WindowDays)
	return 0
}

func cmdSLOsRm(args []string) int {
	fs := newFlagSet("slos rm", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale slos rm --app <slug> <slo-id>", "slos")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteSLO(context.Background(), *slug, fs.Arg(0)); err != nil {
		return printErr("Could not delete SLO", err)
	}
	_, _ = fmt.Fprintf(osStdout, "Deleted SLO %s\n", fs.Arg(0))
	return 0
}
