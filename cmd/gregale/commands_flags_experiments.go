package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

// `gregale flag experiment ...` (ADR 20261115163113 §6, ADR 20261116190208).
// The tree names alias `gregale flags experiments <verb>`.

const flagsExperimentVerbs = "list|create|get|start|stop|ship|delete|results|layers|holdout"

func cmdFlagsExperiments(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "gregale "+cliTreeSpellPath("flags experiments")+" "+flagsExperimentVerbs, "flags")
		return 1
	}
	verb, args := args[0], args[1:]
	switch verb {
	case "layers":
		return cmdFlagsExperimentLayers(args)
	case "holdout":
		return cmdFlagsExperimentHoldout(args)
	}
	fs := newFlagSet("flags-experiments-"+verb, flag.ContinueOnError)
	project := fs.String("project", "", "project slug")
	environment := fs.String("environment", "production", "named project environment")
	flagKey := fs.String("flag", "", "create: the variant flag")
	rule := fs.String("rule", "", "create: the flag's weighted rule ID")
	control := fs.String("control", "", "create: the baseline variant")
	metric := fs.String("metric", "", "create: the metric event name")
	kind := fs.String("metric-kind", "", "create: conversion (default), mean (mean event value per customer), funnel, retention, ratio or percentile")
	unit := fs.String("unit", "", "create: customer (default) or subject (end users of a rule with rollout_unit subject)")
	layer := fs.String("layer", "", "create: exclusive layer the experiment joins")
	traffic := fs.String("traffic", "", "create: percent of the layer the experiment holds")
	allocation := fs.String("allocation", "", "create: fixed (default) or bandit")
	denominator := fs.String("denominator-event", "", "create: a ratio metric's denominator event")
	percentile := fs.Int("percentile", 0, "create: a percentile metric's percentile, 50 to 99")
	segment := fs.String("segment", "", "results: split the results by a cohort's members")
	var steps repeatedFlag
	fs.Var(&steps, "step", "create: funnel step event (repeatable, 2 to 6)")
	window := fs.String("window", "", "create: funnel window after first exposure, 1h to 720h")
	returnEvent := fs.String("return-event", "", "create: retention return event")
	period := fs.String("period", "", "create: retention period, day or week")
	periodNumber := fs.Int("period-number", 0, "create: retention period N after exposure, 1 to 12")
	noCuped := fs.Bool("no-cuped", false, "create: turn CUPED variance reduction off")
	lift := fs.String("mixing-lift", "", "create: mSPRT mixing lift (absolute, default 0.02; standardized for mean, default 0.1)")
	maxErrors := fs.String("max-error-increase", "", "create: server error rate guardrail tolerance (absolute, default 0.005)")
	maxSlow := fs.String("max-slow-increase", "", "create: p95 latency guardrail tolerance, as the share of requests slower than control's p95 (default 0.02)")
	noGuardrails := fs.Bool("no-guardrails", false, "create: turn both guardrails off")
	start := fs.Bool("start", false, "create: start at once")
	var notify repeatedFlag
	fs.Var(&notify, "notify", "create: notification channel ID for the experiment's decisions and problems (repeatable; default: the environment's, see gregale flag notify)")
	positionals, err := parseInterleaved(fs, args)
	if err != nil {
		return 1
	}
	want, needs := 1, " and an experiment KEY"
	switch verb {
	case "list":
		want, needs = 0, ""
	case "ship":
		want, needs = 2, " an experiment KEY and the VARIANT to ship"
	}
	// Literal counts, so the positional guard sees the check before any call.
	if *project == "" || (want == 0 && len(positionals) != 0) || (want == 1 && len(positionals) != 1) || (want == 2 && len(positionals) != 2) {
		PrintUsage(os.Stderr, fmt.Sprintf("gregale %s needs --project%s", cliTreeSpellPath("flags experiments "+verb), needs), "flags")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, key := context.Background(), ""
	if want >= 1 {
		key = positionals[0]
	}
	switch verb {
	case "list":
		return flagsExperimentsList(ctx, client, *project, *environment)
	case "create":
		req := api.FlagExperimentCreateRequest{Key: key, Flag: *flagKey, RuleID: *rule, Control: *control, MetricEvent: *metric,
			MetricKind: *kind, Start: *start, Unit: *unit, Layer: *layer, Allocation: *allocation, DenominatorEvent: *denominator, Percentile: *percentile,
			NotifyChannelIDs: notify}
		if *traffic != "" {
			pct, err := strconv.ParseFloat(*traffic, 64)
			if err != nil || pct <= 0 || pct > 100 {
				printCommandValidation(os.Stderr, "gregale %s: --traffic is a percent of the layer, above 0 and up to 100\n", cliTreeSpellPath("flags experiments create"))
				return 1
			}
			req.Traffic = int(pct*100 + 0.5)
		}
		flagsExperimentMetric(&req, steps, *window, *returnEvent, *period, *periodNumber, *noCuped)
		guardrails := &api.FlagExperimentGuardrailsRequest{ServerErrorRate: &api.FlagExperimentGuardrailRequest{Disabled: *noGuardrails},
			P95Latency: &api.FlagExperimentGuardrailRequest{Disabled: *noGuardrails}}
		for _, n := range []struct {
			flag, value string
			dst         **float64
		}{
			{"--mixing-lift", *lift, &req.MixingLift},
			{"--max-error-increase", *maxErrors, &guardrails.ServerErrorRate.MaxIncrease},
			{"--max-slow-increase", *maxSlow, &guardrails.P95Latency.MaxIncrease},
		} {
			if n.value == "" {
				continue
			}
			v, err := strconv.ParseFloat(n.value, 64)
			if err != nil {
				printCommandValidation(os.Stderr, "gregale %s: %s is a number in (0, 1]\n", cliTreeSpellPath("flags experiments create"), n.flag)
				return 1
			}
			*n.dst = &v
		}
		if *noGuardrails || *maxErrors != "" || *maxSlow != "" {
			req.Guardrails = guardrails
		}
		x, err := client.CreateProjectFlagExperiment(ctx, *project, *environment, req)
		return flagsExperimentOut(x, err)
	case "ship":
		out, err := client.ShipProjectFlagExperiment(ctx, *project, *environment, key, positionals[1])
		if err != nil {
			return printErr("Experiment operation failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		_, _ = fmt.Fprintf(osStdout, "Shipped %s: rule %s of %s now serves %s to every customer it matches (flags version %d); experiment %s is %s\n",
			positionals[1], out.Experiment.RuleID, out.Experiment.Flag, positionals[1], out.ConfigVersion, key, out.Experiment.Status)
		return 0
	case "get":
		x, err := client.GetProjectFlagExperiment(ctx, *project, *environment, key)
		return flagsExperimentOut(x, err)
	case "start":
		x, err := client.StartProjectFlagExperiment(ctx, *project, *environment, key)
		return flagsExperimentOut(x, err)
	case "stop":
		x, err := client.StopProjectFlagExperiment(ctx, *project, *environment, key)
		return flagsExperimentOut(x, err)
	case "delete":
		if err := client.DeleteProjectFlagExperiment(ctx, *project, *environment, key); err != nil {
			return printErr("Experiment operation failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"deleted": key}))
		}
		_, _ = fmt.Fprintf(osStdout, "Deleted experiment %s\n", key)
		return 0
	case "results":
		res, err := client.ProjectFlagExperimentResults(ctx, *project, *environment, key)
		if *segment != "" {
			res, err = client.ProjectFlagExperimentResultsBySegment(ctx, *project, *environment, key, *segment)
		}
		if err != nil {
			return printErr("Experiment operation failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(res))
		}
		printFlagsExperimentResults(res)
		return 0
	}
	printCommandValidation(os.Stderr, "gregale flag experiments: unknown subcommand %q (%s)\n", verb, flagsExperimentVerbs)
	return 1
}

func flagsExperimentsList(ctx context.Context, client *Client, project, environment string) int {
	list, err := client.ListProjectFlagExperiments(ctx, project, environment)
	if err != nil {
		return printErr("Experiment operation failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(list))
	}
	if len(list.Experiments) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No experiments. Create one with gregale %s KEY --flag F --rule R --control V --metric EVENT --project %s\n", cliTreeSpellPath("flags experiments create"), project)
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KEY\tFLAG\tRULE\tCONTROL\tMETRIC\tSTATUS")
	for _, x := range list.Experiments {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", x.Key, x.Flag, x.RuleID, x.Control, x.MetricEvent, x.Status)
	}
	_ = tw.Flush()
	return 0
}

func flagsExperimentOut(x api.FlagExperiment, err error) int {
	if err != nil {
		return printErr("Experiment operation failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(x))
	}
	_, _ = fmt.Fprintf(osStdout, "Experiment %s on %s/%s (control %s, metric %s): %s\n", x.Key, x.Flag, x.RuleID, x.Control, flagsExperimentMetricText(x), x.Status)
	if x.StopReason == "guardrail_breach" {
		_, _ = fmt.Fprintf(osStdout, "Stopped by a guardrail: %v\n", x.Breach)
	}
	return 0
}

// flagsExperimentMetric adds a funnel or retention definition and the CUPED
// switch (ADR 20261119102741); apid validates them.
func flagsExperimentMetric(req *api.FlagExperimentCreateRequest, steps []string, window, returnEvent, period string, periodNumber int, noCuped bool) {
	if len(steps) > 0 || window != "" {
		f := &api.FlagExperimentFunnel{Window: window}
		for _, s := range steps {
			f.Steps = append(f.Steps, api.InsightStepRequest{Event: s})
		}
		req.Funnel = f
	}
	if returnEvent != "" || period != "" || periodNumber != 0 {
		req.Retention = &api.FlagExperimentRetention{Return: api.InsightStepRequest{Event: returnEvent}, Period: period, PeriodNumber: periodNumber}
	}
	if noCuped {
		off := false
		req.Cuped = &off
	}
}

// flagsExperimentMetricText names an experiment's metric.
func flagsExperimentMetricText(x api.FlagExperiment) string {
	switch {
	case x.Funnel != nil:
		names := make([]string, 0, len(x.Funnel.Steps))
		for _, s := range x.Funnel.Steps {
			names = append(names, s.Event)
		}
		return fmt.Sprintf("funnel %s within %s", strings.Join(names, " > "), x.Funnel.Window)
	case x.Retention != nil:
		return fmt.Sprintf("retention %s in %s %d", x.Retention.Return.Event, x.Retention.Period, x.Retention.PeriodNumber)
	}
	switch x.MetricKind {
	case "ratio":
		return fmt.Sprintf("ratio %s per %s", x.MetricEvent, x.DenominatorEvent)
	case "percentile":
		return fmt.Sprintf("p%d of %s", x.Percentile, x.MetricEvent)
	}
	return x.MetricKind + " " + x.MetricEvent
}

func printFlagsExperimentCuped(res api.FlagExperimentResults) {
	c := res.Cuped
	if !c.Applied {
		if c.Reason != "" && c.Reason != "off" {
			_, _ = fmt.Fprintf(osStdout, "CUPED not applied (%s); results are unadjusted.\n", strings.ReplaceAll(c.Reason, "_", " "))
		}
		return
	}
	_, _ = fmt.Fprintf(osStdout, "CUPED: adjusted for each customer's %d days before the start; variance reduced by %.0f%%.\n", c.PreDays, 100*c.VarianceReduction)
	for _, v := range res.Variants {
		if u := v.Unadjusted; u != nil {
			_, _ = fmt.Fprintf(osStdout, "  %s unadjusted: lift %+.4g, interval [%+.4g, %+.4g], p %.3g\n", v.Variant, u.Lift, u.Low, u.High, u.PValue)
		}
	}
}

func printFlagsExperimentResults(res api.FlagExperimentResults) {
	x := res.Experiment
	_, _ = fmt.Fprintf(osStdout, "Experiment %s (%s): %s against control %s, metric %s\n", x.Key, x.Status, x.Flag, x.Control, flagsExperimentMetricText(x))
	if res.SampleRatioMismatch {
		_, _ = fmt.Fprintf(osStdout, "Sample ratio mismatch (p=%.2g): customers were not allocated as configured; results are not trustworthy.\n", res.SampleRatioPValue)
	}
	mean := x.MetricKind == "mean"
	rate := func(v float64) string {
		if mean {
			return fmt.Sprintf("%.4g", v)
		}
		return fmt.Sprintf("%.2f%%", 100*v)
	}
	num := func(v float64) string {
		if mean {
			return fmt.Sprintf("%+.4g", v)
		}
		return fmt.Sprintf("%+.2f", 100*v)
	}
	diff := func(v float64) string {
		if mean {
			return num(v)
		}
		return num(v) + " pts"
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	header := "VARIANT\tCUSTOMERS\tCONVERTED\tRATE\tLIFT\t95% INTERVAL\tP-VALUE\tSTATUS"
	if mean {
		header = "VARIANT\tCUSTOMERS\tNON-ZERO\tMEAN\tLIFT\t95% INTERVAL\tP-VALUE\tSTATUS"
	}
	_, _ = fmt.Fprintln(tw, header)
	c := res.Control
	_, _ = fmt.Fprintf(tw, "%s (control)\t%d\t%d\t%s\t-\t-\t-\t-\n", c.Variant, c.Exposed, c.Converted, rate(c.Rate))
	for _, v := range res.Variants {
		lift, interval, p := "-", "-", "-"
		if v.Lift != nil {
			lift = diff(*v.Lift)
			interval = fmt.Sprintf("[%s, %s]", num(*v.Low), num(*v.High))
			p = fmt.Sprintf("%.3g", *v.PValue)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n", v.Variant, v.Exposed, v.Converted, rate(v.Rate), lift, interval, p, v.Status)
	}
	_ = tw.Flush()
	if res.CrossedUnits > 0 {
		_, _ = fmt.Fprintf(osStdout, "%d customers saw more than one variant and are left out.\n", res.CrossedUnits)
	}
	if res.FixedHorizon {
		_, _ = fmt.Fprintln(osStdout, "Percentile results are fixed-horizon: read them once, at the planned end.")
	}
	printFlagsExperimentCuped(res)
	printFlagsExperimentBandit(res)
	printFlagsExperimentSegments(res)
	printFlagsExperimentGuardrails(res)
	r := res.Recommendation
	if r.Action == "" {
		return
	}
	_, _ = fmt.Fprintf(osStdout, "\nRecommendation (%s): %s\n", r.Action, r.Summary)
	for _, cmd := range r.Commands {
		_, _ = fmt.Fprintf(osStdout, "  %s\n", cmd)
	}
}

func printFlagsExperimentGuardrails(res api.FlagExperimentResults) {
	if len(res.Guardrails) == 0 && len(res.Latency) == 0 {
		return
	}
	_, _ = fmt.Fprintln(osStdout, "\nGuardrails (variant minus control, 99% interval):")
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "GUARDRAIL\tVARIANT\tCONTROL\tVARIANT RATE\tDIFFERENCE\tINTERVAL\tTOLERANCE\tSTATUS")
	for _, g := range res.Guardrails {
		for _, v := range g.Variants {
			d, interval := "-", "-"
			if v.Difference != nil {
				d = fmt.Sprintf("%+.2f pts", 100**v.Difference)
				interval = fmt.Sprintf("[%+.2f, %+.2f]", 100**v.Low, 100**v.High)
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%.2f%%\t%.2f%%\t%s\t%s\t+%.2f pts\t%s\n", g.Name, v.Variant, 100*v.ControlRate, 100*v.VariantRate, d, interval, 100*g.Tolerance, v.Status)
		}
	}
	_ = tw.Flush()
	if t := res.Experiment.LatencyThresholdMS; t != nil {
		_, _ = fmt.Fprintf(osStdout, "Slow requests are slower than control's p95 of %d ms.\n", *t)
	}
	for _, l := range res.Latency {
		_, _ = fmt.Fprintf(osStdout, "p95 latency %s: %d ms over %d requests\n", l.Variant, l.P95MS, l.Requests)
	}
}
