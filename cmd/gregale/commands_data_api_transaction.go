package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

func cmdDataAPITransaction(args []string) int {
	if len(args) == 0 || args[0] != "init" {
		return printErr("Invalid transaction command", errors.New("use transaction init --name NAME --output NEW_DIR"))
	}
	fs := newFlagSet("data-api transaction init", flag.ContinueOnError)
	name := fs.String("name", "", "function/table prefix")
	output := fs.String("output", "", "new package directory")
	contract := fs.String("contract", "v1", "receipt contract: v1 or v2")
	if parseInterspersed(fs, args[1:]) != nil {
		return 1
	}
	if fs.NArg() != 0 || *output == "" || !regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`).MatchString(*name) || strings.HasPrefix(*name, "pg_") || strings.HasPrefix(*name, "gregale_") {
		return printErr("Invalid transaction options", errors.New("use a simple lowercase name up to 32 characters and a new output directory"))
	}
	if err := buildDataAPITransactionContract(*name, *output, *contract); err != nil {
		return printErr("Transaction init failed", err)
	}
	if jsonOutput {
		report := map[string]any{"version": 1, "command": "data-api transaction init", "status": "needs_review", "function": "api." + *name, "output": *output}
		if *contract == "v2" {
			report["contract"] = "v2"
			report["function"] = "api." + *name + "_v2"
			report["receipt_function"] = "api.read_" + *name + "_receipt_v2"
		}
		raw, _ := json.Marshal(report)
		if _, err := fmt.Fprintln(osStdout, string(raw)); err != nil {
			return printErr("Output failed", err)
		}
	} else {
		if _, err := fmt.Fprintf(osStdout, "Transaction package created in %s; review README.md and the migration before applying.\n", *output); err != nil {
			return printErr("Output failed", err)
		}
	}
	return 0
}
func buildDataAPITransaction(name, output string) error {
	return buildDataAPITransactionContract(name, output, "v1")
}

func buildDataAPITransactionContract(name, output, contract string) error {
	if contract != "v1" && contract != "v2" {
		return errors.New("receipt contract must be v1 or v2")
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`).MatchString(name) || strings.HasPrefix(name, "pg_") || strings.HasPrefix(name, "gregale_") {
		return errors.New("invalid transaction name")
	}
	target, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if err = os.Mkdir(target, 0700); err != nil {
		return errors.New("choose a new directory; existing files are preserved")
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(target)
		}
	}()
	source := "data-api/transactions/"
	files := []string{"migration.sql", "client.ts", "fixture.sql", "README.md", "access-checks.json", "data-checks.json"}
	if contract == "v2" {
		source += "v2/"
		files = append(files, "cleanup-receipts.mjs", "receipt-diagnostics.mjs", "receipt-diagnostics-core.mjs", "package.json")
	}
	for _, file := range files {
		path := source + file
		if file == "receipt-diagnostics-core.mjs" {
			path = "data-api-starter/migrations/" + file
		}
		raw, err := templates.FS.ReadFile(path)
		if err != nil {
			return err
		}
		raw = []byte(strings.ReplaceAll(string(raw), "__OP__", name))
		if err = os.WriteFile(filepath.Join(target, file), raw, 0600); err != nil {
			return err
		}
	}
	complete = true
	return nil
}
