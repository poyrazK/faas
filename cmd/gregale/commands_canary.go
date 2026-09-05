// commands_canary.go — SAFE-RELEASES-OBS read-only canary simulation.
//
// `gregale canary simulate <slug>` projects the existing one-hour app metrics
// onto a candidate canary preset. It never mutates an app or deployment.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api/canary"
	"github.com/onebox-faas/faas/pkg/appmetrics"
)

const canarySimulateUsage = "usage: gregale canary simulate <slug> [--canary-preset PRESET]"

func cmdCanary(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale canary simulate <slug> [--canary-preset PRESET]", "canary")
		return 1
	}
	switch args[0] {
	case "simulate":
		return cmdCanarySimulate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown canary subcommand %q\n", args[0])
		return 1
	}
}

func cmdCanarySimulate(args []string) int {
	fs := flag.NewFlagSet("canary simulate", flag.ContinueOnError)
	preset := fs.String("canary-preset", "balanced", "canary preset")

	// The documented spelling keeps the slug first, while peeling it off
	// here also allows the conventional flags-before-positionals form.
	var slug string
	flagArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		slug = args[0]
		flagArgs = args[1:]
	}
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if slug == "" && fs.NArg() > 0 {
		slug = fs.Arg(0)
	}
	if slug == "" || fs.NArg() > 1 {
		PrintUsage(os.Stderr, canarySimulateUsage, "canary")
		return 1
	}
	if !canary.AllowedCanaryPreset(*preset) || *preset == "none" || *preset == "custom" {
		return printErr("Invalid --canary-preset", fmt.Errorf("--canary-preset must be one of: slow, balanced, aggressive, 1-10-50-100; got %q", *preset))
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	metrics, err := client.GetAppMetrics(ctx, slug, "1h")
	if err != nil {
		return printErr("Could not fetch canary metrics", err)
	}
	if metrics.Source != "" && metrics.Source != appmetrics.SourcePrometheus {
		return printErr("Canary simulation unavailable", errors.New(metrics.Source))
	}

	report, err := canary.SimulateCanary(ctx, nil, metrics.AppID, *preset, nil,
		canary.WithAggregate(metrics.ErrorRatePct/100, metrics.LatencyP95MS),
		canary.WithObservedTraffic(metrics.RequestCount))
	if err != nil {
		return printErr("Could not simulate canary", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	if report.Note != "" {
		_, _ = fmt.Fprintf(osStdout, "Note: %s\n", report.Note)
	}
	_, _ = fmt.Fprintf(osStdout, "App:             %s\n", slug)
	_, _ = fmt.Fprintf(osStdout, "Preset:          %s\n", report.Preset)
	_, _ = fmt.Fprintf(osStdout, "Observed:        %d requests, %.2f%% errors, p95=%.1fms\n", report.ObservedTraffic, report.ObservedError*100, report.ObservedP95Ms)
	_, _ = fmt.Fprint(osStdout, report.FormatTable())
	return 0
}
