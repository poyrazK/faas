package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// `gregale flag experiment layer|holdout ...` (ADR 20261119173702 §2). The
// tree names alias `gregale flags experiments layers|holdout <verb>`.

const (
	flagsExperimentLayerVerbs   = "list|create|delete"
	flagsExperimentHoldoutVerbs = "get|set|report"
)

// flagsExperimentScopeFlags parses --project and --environment and the
// positionals of an experiments subgroup verb.
func flagsExperimentScopeFlags(name string, args []string, extra func(*flag.FlagSet)) (project, environment string, positionals []string, ok bool) {
	fs := newFlagSet(name, flag.ContinueOnError)
	p := fs.String("project", "", "project slug")
	e := fs.String("environment", "production", "named project environment")
	if extra != nil {
		extra(fs)
	}
	positionals, err := parseInterleaved(fs, args)
	return *p, *e, positionals, err == nil
}

func cmdFlagsExperimentLayers(args []string) int {
	path := cliTreeSpellPath("flags experiments layers")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "gregale "+path+" "+flagsExperimentLayerVerbs, "flags")
		return 1
	}
	verb, args := args[0], args[1:]
	project, environment, positionals, ok := flagsExperimentScopeFlags("flags-experiments-layers-"+verb, args, nil)
	if !ok {
		return 1
	}
	want := 1
	if verb == "list" {
		want = 0
	}
	if project == "" || len(positionals) != want {
		needs := " and a layer KEY"
		if want == 0 {
			needs = ""
		}
		PrintUsage(os.Stderr, fmt.Sprintf("gregale %s %s needs --project%s", path, verb, needs), "flags")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	switch verb {
	case "list":
		list, err := client.ListProjectFlagExperimentLayers(ctx, project, environment)
		return printFlagsExperimentLayers(list, err)
	case "create":
		list, err := client.CreateProjectFlagExperimentLayer(ctx, project, environment, positionals[0])
		return printFlagsExperimentLayers(list, err)
	case "delete":
		if err := client.DeleteProjectFlagExperimentLayer(ctx, project, environment, positionals[0]); err != nil {
			return printErr("Layer operation failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"deleted": positionals[0]}))
		}
		_, _ = fmt.Fprintf(osStdout, "Deleted layer %s\n", positionals[0])
		return 0
	}
	printCommandValidation(os.Stderr, "gregale %s: unknown subcommand %q (%s)\n", path, verb, flagsExperimentLayerVerbs)
	return 1
}

func printFlagsExperimentLayers(list api.FlagExperimentLayerList, err error) int {
	if err != nil {
		return printErr("Layer operation failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(list))
	}
	if len(list.Layers) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No layers. Create one with gregale %s KEY --project P\n", cliTreeSpellPath("flags experiments layers create"))
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "LAYER\tFREE\tEXPERIMENTS")
	for _, l := range list.Layers {
		held := "-"
		for i, s := range l.Slices {
			if i == 0 {
				held = ""
			} else {
				held += ", "
			}
			held += fmt.Sprintf("%s [%.2f%%, %.2f%%)", s.Experiment, float64(s.From)/100, float64(s.To)/100)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%.2f%%\t%s\n", l.Key, float64(l.Free)/100, held)
	}
	_ = tw.Flush()
	return 0
}

func cmdFlagsExperimentHoldout(args []string) int {
	path := cliTreeSpellPath("flags experiments holdout")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "gregale "+path+" "+flagsExperimentHoldoutVerbs, "flags")
		return 1
	}
	verb, args := args[0], args[1:]
	var event, from, until string
	project, environment, positionals, ok := flagsExperimentScopeFlags("flags-experiments-holdout-"+verb, args, func(fs *flag.FlagSet) {
		fs.StringVar(&event, "event", "", "report: the event whose senders count as converted")
		fs.StringVar(&from, "from", "", "report: start (RFC 3339; default 30 days ago)")
		fs.StringVar(&until, "until", "", "report: end (RFC 3339; default now)")
	})
	if !ok {
		return 1
	}
	want := 0
	if verb == "set" {
		want = 1
	}
	if project == "" || len(positionals) != want || (verb == "report" && event == "") {
		PrintUsage(os.Stderr, fmt.Sprintf("gregale %s %s needs --project%s", path, verb, map[string]string{
			"set": " and the PERCENT of customers to hold out (0 to 20)", "report": " and --event"}[verb]), "flags")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	switch verb {
	case "get":
		h, err := client.GetProjectFlagExperimentHoldout(ctx, project, environment)
		return printFlagsExperimentHoldout(h, err)
	case "set":
		pct, err := strconv.ParseFloat(positionals[0], 64)
		if err != nil || pct < 0 || pct > 20 {
			printCommandValidation(os.Stderr, "gregale %s set: PERCENT is a number from 0 to 20\n", path)
			return 1
		}
		h, err := client.SetProjectFlagExperimentHoldout(ctx, project, environment, int(pct*100+0.5))
		return printFlagsExperimentHoldout(h, err)
	case "report":
		var start, end time.Time
		for _, t := range []struct {
			raw string
			dst *time.Time
		}{{from, &start}, {until, &end}} {
			if t.raw == "" {
				continue
			}
			if *t.dst, err = time.Parse(time.RFC3339, t.raw); err != nil {
				printCommandValidation(os.Stderr, "gregale %s report: --from and --until are RFC 3339 times\n", path)
				return 1
			}
		}
		r, err := client.GetProjectFlagExperimentHoldoutReport(ctx, project, environment, event, start, end)
		return printFlagsExperimentHoldoutReport(r, err)
	}
	printCommandValidation(os.Stderr, "gregale %s: unknown subcommand %q (%s)\n", path, verb, flagsExperimentHoldoutVerbs)
	return 1
}

func printFlagsExperimentHoldout(h api.FlagExperimentHoldout, err error) int {
	if err != nil {
		return printErr("Holdout operation failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(h))
	}
	if h.BasisPoints == 0 {
		_, _ = fmt.Fprintln(osStdout, "No holdout: every customer can join experiments.")
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "Holdout: %.2f%% of customers see every experiment's control.\n", float64(h.BasisPoints)/100)
	return 0
}

func printFlagsExperimentHoldoutReport(r api.FlagExperimentHoldoutReport, err error) int {
	if err != nil {
		return printErr("Holdout report failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(r))
	}
	if r.BasisPoints == 0 {
		_, _ = fmt.Fprintln(osStdout, "No holdout is set, so there is nothing to compare.")
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "Customers sending %s, %s to %s:\n", r.Event, r.From.Format(time.DateOnly), r.Until.Format(time.DateOnly))
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "GROUP\tCUSTOMERS\tCONVERTED\tRATE")
	for _, a := range []api.FlagExperimentArm{r.Held, r.Exposed} {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%.2f%%\n", a.Variant, a.Exposed, a.Converted, 100*a.Rate)
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(osStdout, "Experimentation effect (exposed minus held): %+.2f pts, 95%% interval [%+.2f, %+.2f]\n", 100*r.Difference, 100*r.Low, 100*r.High)
	if r.Truncated {
		_, _ = fmt.Fprintln(osStdout, "The customer lists hit the analytics bound; the report covers part of the range.")
	}
	return 0
}

// printFlagsExperimentBandit prints a bandit's posterior.
func printFlagsExperimentBandit(res api.FlagExperimentResults) {
	b := res.Bandit
	if b == nil {
		return
	}
	_, _ = fmt.Fprintln(osStdout, "\nBandit (Thompson sampling; no p-values):")
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ARM\tWEIGHT\tP(BEST)\tEXPECTED LOSS")
	for _, a := range b.Arms {
		_, _ = fmt.Fprintf(tw, "%s\t%.0f%%\t%.3f\t%.4f\n", a.Variant, float64(a.Weight)/100, a.BestProbability, a.ExpectedLoss)
	}
	_ = tw.Flush()
	if b.WeightedAt != nil {
		_, _ = fmt.Fprintf(osStdout, "Weights last moved %s.\n", b.WeightedAt.Format(time.RFC3339))
	}
}

// printFlagsExperimentSegments prints results split by a cohort.
func printFlagsExperimentSegments(res api.FlagExperimentResults) {
	for _, s := range res.Segments {
		who := "not in " + s.Cohort
		if s.Members {
			who = "in " + s.Cohort
		}
		_, _ = fmt.Fprintf(osStdout, "\nCustomers %s (exploratory; Benjamini-Hochberg adjusted p-values):\n", who)
		tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "VARIANT\tCUSTOMERS\tRATE\tLIFT\tP-VALUE\tSTATUS")
		_, _ = fmt.Fprintf(tw, "%s (control)\t%d\t%.2f%%\t-\t-\t-\n", s.Control.Variant, s.Control.Exposed, 100*s.Control.Rate)
		for _, v := range s.Variants {
			lift, p := "-", "-"
			if v.Lift != nil {
				lift, p = fmt.Sprintf("%+.2f pts", 100**v.Lift), fmt.Sprintf("%.3g", *v.PValue)
			}
			_, _ = fmt.Fprintf(tw, "%s\t%d\t%.2f%%\t%s\t%s\t%s\n", v.Variant, v.Exposed, 100*v.Rate, lift, p, v.Status)
		}
		_ = tw.Flush()
	}
}
