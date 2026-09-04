package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

const openapiDefaultSource = "manual_import"

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
	fmt.Fprintf(osStdout, "OpenAPI %s: %d endpoint(s)\n", resp.OpenAPIVersion, resp.EndpointCount)
	if len(resp.Suggestions) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no uncovered routes)")
		return 0
	}
	_, _ = fmt.Fprintln(osStdout, "Uncovered routes:")
	for _, suggestion := range resp.Suggestions {
		fmt.Fprintf(osStdout, "  %-32s %-20s %s\n", suggestion.Path, joinOpenapiMethods(suggestion.Methods), suggestion.Kind)
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
