package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

func cmdBillingBudgetPreview(args []string) int {
	fs := newFlagSet("billing budget-preview", flag.ContinueOnError)
	file := fs.String("file", "", "budget spec JSON file")
	asJSON := fs.Bool("json", false, "print the machine-readable preview")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 0 || *file == "" {
		return printErr("Invalid budget preview arguments", errors.New("usage: gregale billing budget-preview --file PATH [--json]"))
	}
	spec, err := readFinancialBudgetSpec(*file)
	if err != nil {
		return printErr("Invalid budget spec", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	preview, err := client.PreviewFinancialBudget(context.Background(), spec)
	if err != nil {
		return printErr("Could not preview budget", err)
	}
	if *asJSON {
		if err := json.NewEncoder(osStdout).Encode(preview); err != nil {
			return printErr("Could not print budget preview", err)
		}
		return 0
	}
	printFinancialBudgetPreview(osStdout, preview)
	return 0
}

func readFinancialBudgetSpec(path string) (financial.BudgetSpec, error) {
	var spec financial.BudgetSpec
	f, err := os.Open(path)
	if err != nil {
		return spec, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, api.FinancialBudgetSpecBytes+1))
	if err != nil {
		return spec, err
	}
	if len(data) > api.FinancialBudgetSpecBytes {
		return spec, fmt.Errorf("budget JSON exceeds %d bytes", api.FinancialBudgetSpecBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return spec, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return spec, errors.New("expected one JSON budget spec")
	}
	if err := financial.ValidateBudget(spec); err != nil {
		return spec, err
	}
	if len(spec.Name) > api.FinancialBudgetNameBytes || len(spec.NotifyMillicents) > api.FinancialBudgetThresholds || spec.LimitMillicents > api.FinancialBudgetMoneyMax || spec.DrainSeconds > api.FinancialBudgetDrainMax {
		return spec, errors.New("budget spec exceeds a supported limit")
	}
	if spec.Scope.Kind != "account" && uuid.Validate(spec.Scope.ID) != nil {
		return spec, errors.New("budget resource scope requires a UUID")
	}
	spec.NotifyMillicents = append([]int64{}, spec.NotifyMillicents...)
	return spec, nil
}

func printFinancialBudgetPreview(w io.Writer, p api.FinancialBudgetPreviewResponse) {
	_, _ = fmt.Fprintf(w, "Budget: %s\nScope: %s %s\nKnown %s cost: %s / %s\nCoverage complete: %t; fresh: %t\n", p.Spec.Name, p.Spec.Scope.Kind, p.Spec.Scope.ID, p.Spec.Basis, financialMoney(p.KnownMillicents), financialMoney(p.Spec.LimitMillicents), p.CoverageComplete, p.Fresh)
	if !p.EnforcementReady {
		_, _ = fmt.Fprintln(w, "Enforcement is not available on this deployment.")
	}
	switch p.Spec.Action {
	case "notify":
		_, _ = fmt.Fprintln(w, "At the limit: notify; workloads continue.")
	case "reject_traffic":
		_, _ = fmt.Fprintln(w, "At the limit: reject new traffic; running compute can continue.")
	default:
		_, _ = fmt.Fprintf(w, "At the limit: stop the selected workloads after a %d-second drain. Pending work stays queued.\n", p.Spec.DrainSeconds)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ACTION TARGET\tKIND\tID")
	for _, target := range p.Targets {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", target.Name, target.Kind, target.ID)
	}
	_ = tw.Flush()
	if len(p.ContinuingTargets) > 0 {
		_, _ = fmt.Fprintln(w, "Workloads that can continue spending:")
		for _, target := range p.ContinuingTargets {
			_, _ = fmt.Fprintf(w, "  %s (%s)\n", target.Name, target.Kind)
		}
	}
	if !p.CoverageComplete {
		_, _ = fmt.Fprintln(w, "Known costs cover only retained, priced usage; missing evidence can change the amount.")
	}
}
