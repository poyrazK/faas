package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

// `gregale flag experiment ...` (ADR 20261115163113 §6). The tree names
// alias `gregale flags experiments <verb>`.

const flagsExperimentVerbs = "list|create|get|start|stop|delete|results"

func cmdFlagsExperiments(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "gregale "+cliTreeSpellPath("flags experiments")+" "+flagsExperimentVerbs, "flags")
		return 1
	}
	verb, args := args[0], args[1:]
	fs := newFlagSet("flags-experiments-"+verb, flag.ContinueOnError)
	project := fs.String("project", "", "project slug")
	environment := fs.String("environment", "production", "named project environment")
	flagKey := fs.String("flag", "", "create: the variant flag")
	rule := fs.String("rule", "", "create: the flag's weighted rule ID")
	control := fs.String("control", "", "create: the baseline variant")
	metric := fs.String("metric", "", "create: the conversion event name")
	lift := fs.String("mixing-lift", "", "create: mSPRT mixing lift (absolute, default 0.02)")
	start := fs.Bool("start", false, "create: start at once")
	positionals, err := parseInterleaved(fs, args)
	if err != nil {
		return 1
	}
	want := 1
	if verb == "list" {
		want = 0
	}
	if *project == "" || len(positionals) != want {
		PrintUsage(os.Stderr, fmt.Sprintf("gregale %s needs --project%s", cliTreeSpellPath("flags experiments "+verb), map[bool]string{true: " and an experiment KEY"}[want == 1]), "flags")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, key := context.Background(), ""
	if want == 1 {
		key = positionals[0]
	}
	switch verb {
	case "list":
		return flagsExperimentsList(ctx, client, *project, *environment)
	case "create":
		req := api.FlagExperimentCreateRequest{Key: key, Flag: *flagKey, RuleID: *rule, Control: *control, MetricEvent: *metric, Start: *start}
		if *lift != "" {
			v, err := strconv.ParseFloat(*lift, 64)
			if err != nil {
				printCommandValidation(os.Stderr, "gregale %s: --mixing-lift is a number in (0, 1]\n", cliTreeSpellPath("flags experiments create"))
				return 1
			}
			req.MixingLift = &v
		}
		x, err := client.CreateProjectFlagExperiment(ctx, *project, *environment, req)
		return flagsExperimentOut(x, err)
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
	_, _ = fmt.Fprintf(osStdout, "Experiment %s on %s/%s (control %s, metric %s): %s\n", x.Key, x.Flag, x.RuleID, x.Control, x.MetricEvent, x.Status)
	return 0
}

func printFlagsExperimentResults(res api.FlagExperimentResults) {
	x := res.Experiment
	_, _ = fmt.Fprintf(osStdout, "Experiment %s (%s): %s against control %s, metric %s\n", x.Key, x.Status, x.Flag, x.Control, x.MetricEvent)
	if res.SampleRatioMismatch {
		_, _ = fmt.Fprintf(osStdout, "Sample ratio mismatch (p=%.2g): customers were not allocated as configured; results are not trustworthy.\n", res.SampleRatioPValue)
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "VARIANT\tCUSTOMERS\tCONVERTED\tRATE\tLIFT\t95% INTERVAL\tP-VALUE\tSTATUS")
	c := res.Control
	_, _ = fmt.Fprintf(tw, "%s (control)\t%d\t%d\t%.2f%%\t-\t-\t-\t-\n", c.Variant, c.Exposed, c.Converted, 100*c.Rate)
	for _, v := range res.Variants {
		lift, interval, p := "-", "-", "-"
		if v.Lift != nil {
			lift = fmt.Sprintf("%+.2f pts", 100**v.Lift)
			interval = fmt.Sprintf("[%+.2f, %+.2f]", 100**v.Low, 100**v.High)
			p = fmt.Sprintf("%.3g", *v.PValue)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%.2f%%\t%s\t%s\t%s\t%s\n", v.Variant, v.Exposed, v.Converted, 100*v.Rate, lift, interval, p, v.Status)
	}
	_ = tw.Flush()
	if res.CrossedUnits > 0 {
		_, _ = fmt.Fprintf(osStdout, "%d customers saw more than one variant and are left out.\n", res.CrossedUnits)
	}
}
