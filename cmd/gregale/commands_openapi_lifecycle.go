package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

const openapiDefaultSource = "manual_import"

type openapiPreviewOutput struct {
	Policy              api.AppOpenAPIPolicyPreviewResponse `json:"policy"`
	Contract            *api.OpenAPIContractDiffResponse    `json:"contract,omitempty"`
	ContractDiffEnabled bool                                `json:"contract_diff_enabled"`
	ContractError       *api.Problem                        `json:"contract_error,omitempty"`
}

// cmdOpenapiPreview shows the read-only declared-vs-observed contract and
// route-policy coverage for an app. The API remains the source of truth for
// route matching; this command only renders the response for humans or JSON
// consumers.
func cmdOpenapiPreview(args []string) int {
	flags, pos := splitArgsForFlags(args)
	fs := newOpenapiFlagSet("openapi preview")
	scope := fs.String("scope", "prod", "deployment scope to compare")
	failOnUnavailable := fs.Bool("fail-on-unavailable", false, "fail when the contract-diff backend is unavailable")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		PrintUsage(osStderr, "usage: gregale openapi preview <slug> [--scope <scope>] [--fail-on-unavailable]", "openapi")
		return 1
	}
	if !validCLISlug(pos[0]) {
		return printErr("Invalid app slug", fmt.Errorf("invalid slug %q", pos[0]))
	}
	if problem := api.ValidateScope(*scope); problem != nil {
		return printErr("Invalid --scope", &api.APIError{Problem: *problem})
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	resp, err := client.PreviewAppOpenAPIPolicy(ctx, pos[0])
	if err != nil {
		return printErr("Could not preview OpenAPI policy", err)
	}
	contract, contractErr := client.DiffAppOpenAPIContract(ctx, pos[0], *scope)
	if contractErr != nil && !contractDiffDisabled(contractErr) {
		return printErr("Could not preview OpenAPI contract", contractErr)
	}
	if jsonOutput {
		var contractPtr *api.OpenAPIContractDiffResponse
		if contractErr == nil {
			contractPtr = &contract
		}
		var contractProblem *api.Problem
		if contractErr != nil {
			contractProblem = contractDiffProblem(contractErr)
		}
		if code := jsonOut(writeJSON(openapiPreviewOutput{
			Policy: resp, Contract: contractPtr, ContractDiffEnabled: contractErr == nil,
			ContractError: contractProblem,
		})); code != 0 {
			return code
		}
		if *failOnUnavailable && contractProblem != nil {
			return exitCodeForStatus(contractProblem.Status)
		}
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "OpenAPI policy preview for %s (source=%s, observed=%t)\n", pos[0], resp.Source, resp.ObservedAvailable)
	if contractErr == nil {
		printOpenapiContractPreview(contract)
	} else {
		problem := contractDiffProblem(contractErr)
		if problem != nil {
			_, _ = fmt.Fprintf(osStdout, "Contract diff: UNAVAILABLE (%s): %s\n", problem.Code, problem.Detail)
		} else {
			_, _ = fmt.Fprintf(osStdout, "Contract diff: UNAVAILABLE: %s\n", contractErr)
		}
	}
	if len(resp.Routes) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no declared or observed routes)")
		if *failOnUnavailable && contractErr != nil {
			if problem := contractDiffProblem(contractErr); problem != nil {
				return exitCodeForStatus(problem.Status)
			}
			return 3
		}
		return 0
	}
	for _, route := range resp.Routes {
		coverage := "uncovered"
		if route.Covered {
			coverage = "covered"
		}
		_, _ = fmt.Fprintf(osStdout, "  %-8s %-32s %-13s %s\n", route.Method, route.Path, route.Status, coverage)
		for _, rule := range route.Rules {
			enabled := "disabled"
			if rule.Enabled {
				enabled = "enabled"
			}
			_, _ = fmt.Fprintf(osStdout, "    rule %-36s %-10s %s\n", rule.ID, rule.Kind, enabled)
		}
	}
	if len(resp.Suggestions) > 0 {
		_, _ = fmt.Fprintf(osStdout, "Suggestions: %d uncovered declared route(s)\n", len(resp.Suggestions))
	}
	if *failOnUnavailable && contractErr != nil {
		if problem := contractDiffProblem(contractErr); problem != nil {
			return exitCodeForStatus(problem.Status)
		}
		return 3
	}
	return 0
}

func contractDiffDisabled(err error) bool {
	return contractDiffProblem(err) != nil
}

func contractDiffProblem(err error) *api.Problem {
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Problem.Code != api.CodeAPIContractDiffDisabled {
		return nil
	}
	problem := apiErr.Problem
	return &problem
}

func printOpenapiContractPreview(resp api.OpenAPIContractDiffResponse) {
	_, _ = fmt.Fprintf(osStdout, "Contract: source=%s scope=%s blocking=%t breaks=%d additions=%d\n",
		resp.Source, resp.Scope, resp.Blocking, len(resp.Breaks), len(resp.Additions))
	for _, br := range resp.Breaks {
		anchor := br.Method + " " + br.Path
		if br.Status != "" {
			anchor += " " + br.Status
		}
		_, _ = fmt.Fprintf(osStdout, "  BREAKING %-36s %s\n", anchor, br.Kind)
	}
}

// cmdOpenapiApply plans or applies validation edge rules generated from the
// app's persisted OpenAPI document. The command intentionally separates the
// two phases: run without --confirm to inspect the deterministic plan, then
// pass its --preview-sha256 back with --confirm to authorize the write.
func cmdOpenapiApply(args []string) int {
	flags, pos := splitArgsForFlags(args)
	fs := newOpenapiFlagSet("openapi apply")
	confirm := fs.Bool("confirm", false, "apply the plan (requires --preview-sha256)")
	previewSHA256 := fs.String("preview-sha256", "", "approval hash returned by the plan")
	matchHost := fs.String("match-host", "", "hostname for generated rules (defaults to the app hostname)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		PrintUsage(osStderr, "usage: gregale openapi apply <slug> [--confirm --preview-sha256 <sha256>] [--match-host <host>]", "openapi")
		return 1
	}
	if !validCLISlug(pos[0]) {
		return printErr("Invalid app slug", fmt.Errorf("invalid slug %q", pos[0]))
	}
	if *confirm && *previewSHA256 == "" {
		return printErr("Missing preview hash", fmt.Errorf("--confirm requires --preview-sha256"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ApplyAppOpenAPIPolicy(context.Background(), pos[0], api.ApplyAppOpenAPIPolicyRequest{
		Confirm: *confirm, PreviewSHA256: *previewSHA256, MatchHost: *matchHost,
	})
	if err != nil {
		return printErr("Could not apply OpenAPI policy", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if resp.Planned {
		_, _ = fmt.Fprintf(osStdout, "OpenAPI policy plan for %s (host=%s)\n", pos[0], resp.MatchHost)
		_, _ = fmt.Fprintf(osStdout, "  preview_sha256: %s\n", resp.PreviewSHA256)
		if len(resp.Suggestions) == 0 {
			_, _ = fmt.Fprintln(osStdout, "  (no changes)")
			return 0
		}
		_, _ = fmt.Fprintf(osStdout, "  rules to create: %d\n", len(resp.Suggestions))
		for _, suggestion := range resp.Suggestions {
			_, _ = fmt.Fprintf(osStdout, "    %-32s %-20s %s\n", suggestion.Path, joinOpenapiMethods(suggestion.Methods), suggestion.Kind)
		}
		_, _ = fmt.Fprintln(osStdout, "Run again with --confirm --preview-sha256 <hash> to apply.")
		return 0
	}
	PrintOK(osStdout, "OpenAPI policy applied for %s (%d rule(s)).", pos[0], resp.AppliedCount)
	return 0
}

// cmdOpenapiGet fetches the app-level OpenAPI document. The response is
// deliberately written as raw JSON so it can be saved directly or piped to
// jq; --source=auto returns the platform-merged document.
func cmdOpenapiGet(args []string) int {
	flags, pos := splitArgsForFlags(args)
	fs := newOpenapiFlagSet("openapi get")
	source := fs.String("source", openapiDefaultSource, "document source (manual_import|auto)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		PrintUsage(osStderr, "usage: gregale openapi get <slug> [--source manual_import|auto]", "openapi")
		return 1
	}
	if !validOpenapiSource(*source) {
		return printErr("Invalid --source", fmt.Errorf("must be manual_import or auto; got %q", *source))
	}
	if !validCLISlug(pos[0]) {
		return printErr("Invalid app slug", fmt.Errorf("invalid slug %q", pos[0]))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	doc, err := client.GetAppOpenAPI(context.Background(), pos[0], *source)
	if err != nil {
		return printErr("Could not fetch OpenAPI document", err)
	}
	if _, err := osStdout.Write(doc); err != nil {
		return printErr("Could not write OpenAPI document", err)
	}
	if len(doc) == 0 || doc[len(doc)-1] != '\n' {
		_, _ = fmt.Fprintln(osStdout)
	}
	return 0
}

// cmdOpenapiImport stores an app-level OpenAPI document. The input is a JSON
// file, or '-' to read JSON from stdin. The server remains the canonical
// validator for the OpenAPI version and endpoint limits.
func cmdOpenapiImport(args []string) int {
	doc, slug, ok := parseOpenapiDocumentArgs("openapi import", args)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ImportAppOpenAPI(context.Background(), slug, doc)
	if err != nil {
		return printErr("Could not import OpenAPI document", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "OpenAPI document imported for %s.", slug)
	printOpenapiImportSummary(resp)
	return 0
}

// cmdOpenapiDryRun validates a candidate document and reports uncovered
// routes without persisting it. --json preserves the complete suggestion
// action payload for a follow-up edge-rules command.
func cmdOpenapiDryRun(args []string) int {
	doc, slug, ok := parseOpenapiDocumentArgs("openapi dry-run", args)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.DryRunAppOpenAPI(context.Background(), slug, doc)
	if err != nil {
		return printErr("Could not dry-run OpenAPI document", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	_, _ = fmt.Fprintf(osStdout, "OpenAPI %s: %d endpoint(s)\n", resp.OpenAPIVersion, resp.EndpointCount)
	if len(resp.Suggestions) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no uncovered routes)")
		return 0
	}
	_, _ = fmt.Fprintln(osStdout, "Uncovered routes:")
	for _, suggestion := range resp.Suggestions {
		_, _ = fmt.Fprintf(osStdout, "  %-32s %-20s %s\n", suggestion.Path, joinOpenapiMethods(suggestion.Methods), suggestion.Kind)
	}
	return 0
}

// cmdOpenapiRemove deletes the app-level imported document. The API makes
// this operation idempotent, so no extra confirmation state is needed here.
func cmdOpenapiRemove(args []string) int {
	flags, pos := splitArgsForFlags(args)
	fs := newOpenapiFlagSet("openapi rm")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		PrintUsage(osStderr, "usage: gregale openapi rm <slug>", "openapi")
		return 1
	}
	if !validCLISlug(pos[0]) {
		return printErr("Invalid app slug", fmt.Errorf("invalid slug %q", pos[0]))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteAppOpenAPI(context.Background(), pos[0]); err != nil {
		return printErr("Could not remove OpenAPI document", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"app": pos[0], "deleted": true}))
	}
	PrintOK(osStdout, "OpenAPI document removed for %s.", pos[0])
	return 0
}

func newOpenapiFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	return fs
}

func validOpenapiSource(source string) bool {
	return source == openapiDefaultSource || source == "auto"
}

func parseOpenapiDocumentArgs(command string, args []string) (map[string]any, string, bool) {
	flags, pos := splitArgsForFlags(args)
	fs := newOpenapiFlagSet(command)
	if err := fs.Parse(flags); err != nil {
		return nil, "", false
	}
	if len(pos) != 2 {
		PrintUsage(osStderr, "usage: gregale "+command+" <slug> <file|->", "openapi")
		return nil, "", false
	}
	if !validCLISlug(pos[0]) {
		printErr("Invalid app slug", fmt.Errorf("invalid slug %q", pos[0]))
		return nil, "", false
	}
	doc, err := readOpenapiDocument(pos[1])
	if err != nil {
		printErr("Could not read OpenAPI document", err)
		return nil, "", false
	}
	return doc, pos[0], true
}

func readOpenapiDocument(path string) (map[string]any, error) {
	var body []byte
	var err error
	if path == "-" {
		body, err = io.ReadAll(osStdin)
	} else {
		body, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("document is empty")
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("document is not valid JSON: %w", err)
	}
	if doc == nil {
		return nil, fmt.Errorf("document must be a JSON object")
	}
	return doc, nil
}

func printOpenapiImportSummary(resp api.AppOpenAPIImportResponse) {
	_, _ = fmt.Fprintf(osStdout, "  version:     %s\n", resp.OpenAPIVersion)
	_, _ = fmt.Fprintf(osStdout, "  endpoints:   %d\n", resp.EndpointCount)
	_, _ = fmt.Fprintf(osStdout, "  bytes:       %d\n", resp.ByteSize)
	_, _ = fmt.Fprintf(osStdout, "  updated_at:  %s\n", resp.UpdatedAt)
}

func joinOpenapiMethods(methods []string) string {
	if len(methods) == 0 {
		return "-"
	}
	result := methods[0]
	for _, method := range methods[1:] {
		result += "," + method
	}
	return result
}
