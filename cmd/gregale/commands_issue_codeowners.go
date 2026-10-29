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
	"github.com/onebox-faas/faas/pkg/issues"
)

func readIssueCodeownersFile(file string) ([]byte, error) {
	f, err := openCustomerFile(file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, api.IssueCodeownersMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > api.IssueCodeownersMaxBytes {
		return nil, fmt.Errorf("input exceeds %d bytes", api.IssueCodeownersMaxBytes)
	}
	return data, nil
}

func cmdIssueCodeowners(args []string) int {
	usage := "usage: gregale debug issue codeowners <preview|apply> --file CODEOWNERS --bindings-file owners.json --source-root /app [--app SLUG --replace]"
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(os.Stderr, usage)
		return 0
	}
	fs := newFlagSet("issues codeowners", flag.ContinueOnError)
	file := fs.String("file", "", "local CODEOWNERS file")
	bindingsFile := fs.String("bindings-file", "", "JSON owner-handle to account/team target map")
	root := fs.String("source-root", "", "deployed source-frame root")
	app := fs.String("app", "", "application slug for apply")
	replace := fs.Bool("replace", false, "explicitly replace the existing ordered ownership policy")
	flags, positionals := normalizeDebugFlagArgs(args[1:], map[string]bool{"file": true, "bindings-file": true, "source-root": true, "app": true})
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if len(positionals) != 0 || *file == "" || *bindingsFile == "" || *root == "" || args[0] != "preview" && args[0] != "apply" {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	if args[0] == "apply" && (*app == "" || !*replace) {
		return printErr("Explicit replacement required", fmt.Errorf("apply requires --app and --replace; preview the resulting rules first"))
	}
	data, err := readIssueCodeownersFile(*file)
	if err != nil {
		return printErr("Could not read CODEOWNERS", err)
	}
	bindingsData, err := readIssueCodeownersFile(*bindingsFile)
	if err != nil {
		return printErr("Could not read owner bindings", err)
	}
	var bindings map[string]issues.CodeownerTarget
	if err := json.Unmarshal(bindingsData, &bindings); err != nil {
		return printErr("Invalid owner bindings", err)
	}
	compiled, err := issues.CompileCodeownersTargets(string(data), *root, bindings)
	if err != nil {
		return printErr("Could not import CODEOWNERS", err)
	}
	if args[0] == "preview" {
		return jsonOut(writeJSON(compiled))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := c.SetIssueOwnershipRules(context.Background(), *app, api.IssueOwnershipRules{Rules: compiled.Rules})
	if err != nil {
		return printErr("Could not replace ownership policy", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Replaced ownership policy with %d CODEOWNERS rules. Existing issues retain their ownership.\n", len(out.Rules))
	return 0
}
