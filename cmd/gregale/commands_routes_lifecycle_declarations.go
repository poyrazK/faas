package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/routelifecycle"
)

func cmdRoutesLifecycleDeclarations(args []string) int {
	flags, positional := splitArgsForFlags(args, "fail-on-findings")
	fs := newFlagSet("routes lifecycle declarations", flag.ContinueOnError)
	from := fs.String("from-deployment", "", "serving baseline UUID")
	to := fs.String("to-deployment", "", "candidate UUID")
	output := fs.String("out", "", "save report JSON to a new file")
	fail := fs.Bool("fail-on-findings", false, "exit 1 for lifecycle regressions or incomplete/review-required declarations")
	if fs.Parse(flags) != nil {
		return 1
	}
	_, a := uuid.Parse(*from)
	_, b := uuid.Parse(*to)
	if len(positional) != 1 || !validCLISlug(positional[0]) || a != nil || b != nil || *from == *to || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale routes lifecycle declarations <slug> --from-deployment UUID --to-deployment UUID [--out PATH] [--fail-on-findings] [--json]", "cli")
		return 1
	}
	if *output != "" {
		if _, err := os.Lstat(*output); err == nil || !errors.Is(err, os.ErrNotExist) {
			return routeImpactError("Invalid --out", errors.New("choose a new file path"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	baseline, err := readRouteMigrationDeploymentSetAllowEmpty(ctx, client, map[string]string{positional[0]: *from}, true)
	if err != nil {
		return printErr("Could not read baseline", err)
	}
	candidate, err := readRouteMigrationDeploymentSetAllowEmpty(ctx, client, map[string]string{positional[0]: *to}, true)
	if err != nil {
		return printErr("Could not read candidate", err)
	}
	bytes := func(loaded routeMigrationLoadedDeployment) []byte {
		if loaded.spec == nil {
			return nil
		}
		raw, _ := json.Marshal(loaded.spec.Raw)
		return raw
	}
	review := routelifecycle.Compare(bytes(baseline[positional[0]]), bytes(candidate[positional[0]]), time.Now().UTC())
	report := struct {
		App       string                           `json:"app"`
		Baseline  routeMigrationDeploymentEvidence `json:"baseline"`
		Candidate routeMigrationDeploymentEvidence `json:"candidate"`
		Review    routelifecycle.Review            `json:"review"`
		Caveats   []string                         `json:"caveats"`
	}{positional[0], baseline[positional[0]].evidence, candidate[positional[0]].evidence, review, []string{"Review hashes identify the normalized JSON compared; baseline and candidate evidence retains captured-document provenance.", "Declaration checks do not prove migration readiness or authorize removal.", "Successor changes require a matching server-owned lifecycle compatibility receipt; saving this advisory report does not create one.", "Server enforcement uses the existing canary route gate mode and applies when a canary advances."}}
	if *output != "" {
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return printErr("Could not encode declaration review", err)
		}
		if err := writeRoutePolicyPlan(*output, append(raw, '\n')); err != nil {
			return printErr("Could not save declaration review", err)
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Lifecycle declaration review for %s: %s\n", previewReportText(positional[0]), review.Outcome)
		for _, f := range review.Findings {
			_, _ = fmt.Fprintf(osStdout, "%s %s: %s (%s)\n", previewReportText(f.Method), previewReportText(f.Path), f.Code, f.Severity)
		}
	}
	if *fail && review.Blocking() {
		return 1
	}
	return 0
}
