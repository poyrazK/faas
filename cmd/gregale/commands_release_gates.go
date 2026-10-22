package main

// ADR-20261019071734: release gates on customer signals.
// ADR-20261022082305: synthetic and metric_change gates.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdReleaseGates(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale release-gates <list|add|rm>", "release-gates")
		return 1
	}
	switch args[0] {
	case "list", "ls":
		return releaseGatesList(args[1:])
	case "add":
		return releaseGatesAdd(args[1:])
	case "rm", "delete":
		return releaseGatesRemove(args[1:])
	}
	fmt.Fprintf(os.Stderr, "gregale release-gates: unknown subcommand %q\n", args[0])
	return 1
}

// releaseGatesApp resolves --app from the flag or the linked project.
func releaseGatesApp(app, usage string) (string, bool) {
	resolved, err := resolveAppFlagOrContext(app)
	if err != nil && !errors.Is(err, errProjectContextNotFound) {
		printErr("Could not read local project context", err)
		return "", false
	}
	if resolved == "" {
		PrintUsage(os.Stderr, usage, "release-gates")
		return "", false
	}
	return resolved, true
}

func releaseGatesList(args []string) int {
	fs := newFlagSet("release-gates list", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (defaults to linked project app)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	slug, ok := releaseGatesApp(*app, "usage: gregale release-gates list [--app <slug>]")
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListReleaseGates(context.Background(), slug)
	if err != nil {
		return printErr("Could not list release gates", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if len(out.Gates) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No release gates. Add one with `gregale release-gates add`.")
		return 0
	}
	w := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tKIND\tFAILS WHEN\tMIN SAMPLES")
	for _, g := range out.Gates {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\n", g.Name, g.Kind, releaseGateRule(g), g.MinSamples)
	}
	_ = w.Flush()
	return 0
}

// releaseGateRule describes when a gate fails.
func releaseGateRule(g api.ReleaseGate) string {
	switch g.Kind {
	case "slo":
		return fmt.Sprintf("SLO %s burns at %dx or faster", g.SLO, g.MaxBurnRate)
	case "new_errors":
		maxGroups := 0
		if g.MaxNewGroups != nil {
			maxGroups = *g.MaxNewGroups
		}
		return fmt.Sprintf("more than %d new error groups of %d+ events", maxGroups, g.MinEvents)
	case "custom_metric":
		threshold := 0.0
		if g.Threshold != nil {
			threshold = *g.Threshold
		}
		return fmt.Sprintf("%s averages %s %s", g.Metric, g.Comparison, strconv.FormatFloat(threshold, 'g', -1, 64))
	case "synthetic":
		maxFailure := 0
		if g.MaxFailurePercent != nil {
			maxFailure = *g.MaxFailurePercent
		}
		return fmt.Sprintf("canary probes of check %s fail more than %d%% of the time, and more than on the stable revision", g.Check, maxFailure)
	case "metric_change":
		direction := "rises"
		if g.Comparison == "below" {
			direction = "drops"
		}
		return fmt.Sprintf("%s %s more than %d%% from before the rollout", g.Metric, direction, g.MaxChangePercent)
	}
	return g.Kind
}

func releaseGatesAdd(args []string) int {
	fs := newFlagSet("release-gates add", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (defaults to linked project app)")
	name := fs.String("name", "", "gate name")
	slo := fs.String("slo", "", "fail when this SLO burns too fast")
	maxBurn := fs.Int("max-burn-rate", 0, "SLO burn rate that fails the gate (default 10)")
	newErrors := fs.Bool("new-errors", false, "fail when the release introduces new error groups")
	maxGroups := fs.Int("max-new-groups", -1, "new error groups allowed (default 0)")
	minEvents := fs.Int("min-events", 0, "events before a new error group counts (default 3)")
	metric := fs.String("metric", "", "fail when this custom metric crosses a bound or moves too far")
	below := fs.String("below", "", "custom metric average that fails the gate when the average is lower")
	above := fs.String("above", "", "custom metric average that fails the gate when the average is higher")
	maxRise := fs.Int("max-rise", 0, "fail when the metric rises more than this percent from before the rollout")
	maxDrop := fs.Int("max-drop", 0, "fail when the metric drops more than this percent from before the rollout")
	check := fs.String("check", "", "probe the canary with this synthetic check")
	maxFailure := fs.Int("max-failure-percent", -1, "failed probes allowed, in percent (default 10)")
	minSamples := fs.Int("min-samples", 0, "requests, probes or metric values before the gate is judged")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	usage := "usage: gregale release-gates add --name NAME (--slo NAME [--max-burn-rate N] | --new-errors [--max-new-groups N] [--min-events N] | --metric NAME (--below V | --above V | --max-rise PCT | --max-drop PCT) | --check NAME [--max-failure-percent PCT]) [--min-samples N] [--app <slug>]"
	if rejectUnexpectedFlagArgs(fs) || *name == "" {
		PrintUsage(os.Stderr, usage, "release-gates")
		return 1
	}
	req, err := releaseGateRequest(releaseGateFlags{Name: *name, SLO: *slo, MaxBurn: *maxBurn, NewErrors: *newErrors,
		MaxGroups: *maxGroups, MinEvents: *minEvents, Metric: *metric, Below: *below, Above: *above, MaxRise: *maxRise,
		MaxDrop: *maxDrop, Check: *check, MaxFailurePercent: *maxFailure, MinSamples: *minSamples})
	if err != nil {
		return printErr("Invalid release gate", err)
	}
	slug, ok := releaseGatesApp(*app, usage)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.CreateReleaseGate(context.Background(), slug, req)
	if err != nil {
		return printErr("Could not add release gate", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	PrintOK(osStdout, "Release gate %s added: fails when %s.", out.Name, releaseGateRule(out))
	return 0
}

// releaseGateFlags are `release-gates add`'s flags. MaxGroups and
// MaxFailurePercent are -1 when unset.
type releaseGateFlags struct {
	Name, SLO            string
	MaxBurn              int
	NewErrors            bool
	MaxGroups, MinEvents int
	Metric, Below, Above string
	MaxRise, MaxDrop     int
	Check                string
	MaxFailurePercent    int
	MinSamples           int
}

// releaseGateRequest builds a create request from exactly one gate kind's flags.
func releaseGateRequest(f releaseGateFlags) (api.CreateReleaseGateRequest, error) {
	req := api.CreateReleaseGateRequest{Name: f.Name, MinSamples: f.MinSamples}
	kinds := 0
	if f.SLO != "" {
		kinds++
		req.Kind, req.SLO, req.MaxBurnRate = "slo", f.SLO, f.MaxBurn
	}
	if f.NewErrors {
		kinds++
		req.Kind, req.MinEvents = "new_errors", f.MinEvents
		if f.MaxGroups >= 0 {
			maxGroups := f.MaxGroups
			req.MaxNewGroups = &maxGroups
		}
	}
	if f.Metric != "" {
		kinds++
		if err := metricGateRequest(&req, f); err != nil {
			return req, err
		}
	}
	if f.Check != "" {
		kinds++
		req.Kind, req.Check = "synthetic", f.Check
		if f.MaxFailurePercent >= 0 {
			maxFailure := f.MaxFailurePercent
			req.MaxFailurePercent = &maxFailure
		}
	}
	if kinds != 1 {
		return req, errors.New("choose exactly one of --slo, --new-errors, --metric or --check")
	}
	if req.Kind != "slo" && f.MaxBurn != 0 {
		return req, errors.New("--max-burn-rate applies to --slo")
	}
	if req.Kind != "new_errors" && (f.MaxGroups >= 0 || f.MinEvents != 0) {
		return req, errors.New("--max-new-groups and --min-events apply to --new-errors")
	}
	if f.Metric == "" && (f.Below != "" || f.Above != "" || f.MaxRise != 0 || f.MaxDrop != 0) {
		return req, errors.New("--below, --above, --max-rise and --max-drop apply to --metric")
	}
	if req.Kind != "synthetic" && f.MaxFailurePercent >= 0 {
		return req, errors.New("--max-failure-percent applies to --check")
	}
	return req, nil
}

// metricGateRequest fills a custom_metric gate (--below or --above) or a
// metric_change gate (--max-rise or --max-drop).
func metricGateRequest(req *api.CreateReleaseGateRequest, f releaseGateFlags) error {
	req.Metric = f.Metric
	set := 0
	for _, v := range []bool{f.Below != "", f.Above != "", f.MaxRise != 0, f.MaxDrop != 0} {
		if v {
			set++
		}
	}
	if set != 1 {
		return errors.New("--metric needs exactly one of --below, --above, --max-rise or --max-drop")
	}
	switch {
	case f.MaxRise != 0:
		req.Kind, req.Comparison, req.MaxChangePercent = "metric_change", "above", f.MaxRise
		return nil
	case f.MaxDrop != 0:
		req.Kind, req.Comparison, req.MaxChangePercent = "metric_change", "below", f.MaxDrop
		return nil
	}
	req.Kind = "custom_metric"
	raw, comparison := f.Below, "below"
	if f.Above != "" {
		raw, comparison = f.Above, "above"
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return fmt.Errorf("--%s must be a number", comparison)
	}
	req.Comparison, req.Threshold = comparison, &v
	return nil
}

func releaseGatesRemove(args []string) int {
	fs := newFlagSet("release-gates rm", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (defaults to linked project app)")
	var positional []string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		positional, args = args[:1], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}
	positional = append(positional, fs.Args()...)
	usage := "usage: gregale release-gates rm <name> [--app <slug>]"
	if len(positional) != 1 {
		PrintUsage(os.Stderr, usage, "release-gates")
		return 1
	}
	slug, ok := releaseGatesApp(*app, usage)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.DeleteReleaseGate(context.Background(), slug, positional[0])
	if err != nil {
		return printErr("Could not remove release gate", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	PrintOK(osStdout, "Release gate %s removed.", out.Name)
	return 0
}
