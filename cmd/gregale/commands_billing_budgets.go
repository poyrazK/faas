package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdBillingBudgets(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		if _, err := fmt.Fprintln(osStdout, "usage: gregale billing budgets <list|get|create|update|delete|history> [flags] [ID]\nBudget drafts do not enforce spending limits. Preview consequences with billing budget-preview."); err != nil {
			return printErr("Could not print budget help", err)
		}
		return 0
	}
	verb := args[0]
	if verb != "list" && verb != "get" && verb != "create" && verb != "update" && verb != "delete" && verb != "history" {
		return printErr("Invalid budget command", errors.New("expected list, get, create, update, delete or history"))
	}
	fs := newFlagSet("billing budgets "+verb, flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print machine-readable data")
	var file, key string
	var expected, after int64
	limit := api.FinancialBudgetHistoryMax
	if verb == "create" || verb == "update" {
		fs.StringVar(&file, "file", "", "budget spec JSON file")
	}
	if verb == "update" || verb == "delete" {
		fs.Int64Var(&expected, "expected-revision", 0, "current policy revision (required)")
	}
	if verb == "create" || verb == "update" || verb == "delete" {
		fs.StringVar(&key, "key", "", "stable operation key for retries")
	}
	if verb == "history" {
		fs.Int64Var(&after, "after-revision", 0, "continue after this revision")
		fs.IntVar(&limit, "limit", api.FinancialBudgetHistoryMax, "maximum revisions in this page")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	id := ""
	needsID := verb == "get" || verb == "update" || verb == "delete" || verb == "history"
	if needsID {
		if fs.NArg() != 1 || uuid.Validate(fs.Arg(0)) != nil {
			return printErr("Invalid budget ID", errors.New("expected one UUID after flags"))
		}
		id = fs.Arg(0)
	} else if fs.NArg() != 0 {
		return printErr("Invalid budget arguments", errors.New("unexpected positional argument"))
	}
	if (verb == "update" || verb == "delete") && (expected < 1 || expected >= api.FinancialBudgetRevisionMax) {
		return printErr("Invalid budget revision", errors.New("--expected-revision must be the current policy revision"))
	}
	if after < 0 || after > api.FinancialBudgetRevisionMax || limit < 1 || limit > api.FinancialBudgetHistoryMax || len(key) > api.FinancialBudgetOperationKeyBytes {
		return printErr("Invalid budget arguments", errors.New("history bounds or operation key exceeded"))
	}
	return runBillingBudget(verb, id, file, key, expected, after, limit, *asJSON)
}

func runBillingBudget(verb, id, file, key string, expected, after int64, limit int, asJSON bool) int {
	var request api.CreateFinancialBudgetRequest
	if verb == "create" || verb == "update" {
		if file == "" {
			return printErr("Budget file required", errors.New("use --file PATH"))
		}
		spec, err := readFinancialBudgetSpec(file)
		if err != nil {
			return printErr("Invalid budget spec", err)
		}
		request.Spec = spec
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if key != "" {
		ctx = api.ContextWithIdempotencyKey(ctx, key)
	}
	var out any
	switch verb {
	case "list":
		out, err = client.ListFinancialBudgets(ctx)
	case "get":
		out, err = client.GetFinancialBudget(ctx, id)
	case "create":
		out, err = client.CreateFinancialBudget(ctx, request)
	case "update":
		out, err = client.UpdateFinancialBudget(ctx, id, api.UpdateFinancialBudgetRequest{ExpectedRevision: expected, Spec: request.Spec})
	case "delete":
		out, err = client.DeleteFinancialBudget(ctx, id, expected)
	case "history":
		out, err = client.ListFinancialBudgetRevisions(ctx, id, after, limit)
	}
	if err != nil {
		return printErr("Budget operation failed", err)
	}
	if asJSON {
		if err := json.NewEncoder(osStdout).Encode(out); err != nil {
			return printErr("Could not print budget", err)
		}
		return 0
	}
	if err := printBillingBudgetResult(out); err != nil {
		return printErr("Could not print budget", err)
	}
	return 0
}

func printBillingBudgetResult(out any) error {
	switch value := out.(type) {
	case api.FinancialBudgetResponse:
		if _, err := fmt.Fprintf(osStdout, "%s\nID: %s\nRevision: %d\nStatus: %s\nLimit: %s (%s)\n", value.Spec.Name, value.ID, value.Revision, value.Status, financialMoney(value.Spec.LimitMillicents), value.Spec.Basis); err != nil {
			return err
		}
		if !value.EnforcementReady {
			_, err := fmt.Fprintln(osStdout, "Enforcement is unavailable; this policy does not protect spending.")
			return err
		}
	case api.FinancialBudgetListResponse:
		tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
		if _, err := fmt.Fprintln(tw, "ID\tNAME\tREVISION\tSTATUS\tLIMIT"); err != nil {
			return err
		}
		for _, p := range value.Budgets {
			if _, err := fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", p.ID, p.Spec.Name, p.Revision, p.Status, financialMoney(p.Spec.LimitMillicents)); err != nil {
				return err
			}
		}
		return tw.Flush()
	case api.FinancialBudgetHistoryResponse:
		tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
		if _, err := fmt.Fprintln(tw, "REVISION\tMUTATION\tACTOR\tRECORDED"); err != nil {
			return err
		}
		for _, p := range value.Revisions {
			if _, err := fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", p.Revision, p.Mutation, p.Actor, p.RecordedAt.Format("2006-01-02T15:04:05Z07:00")); err != nil {
				return err
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		if value.NextRevision != 0 {
			_, err := fmt.Fprintf(osStdout, "Continue with --after-revision %d\n", value.NextRevision)
			return err
		}
	}
	return nil
}
