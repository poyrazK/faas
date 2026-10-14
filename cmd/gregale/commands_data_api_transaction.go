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
	if parseInterspersed(fs, args[1:]) != nil {
		return 1
	}
	if fs.NArg() != 0 || *output == "" || !regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`).MatchString(*name) || strings.HasPrefix(*name, "pg_") || strings.HasPrefix(*name, "gregale_") {
		return printErr("Invalid transaction options", errors.New("use a simple lowercase name up to 32 characters and a new output directory"))
	}
	if err := buildDataAPITransaction(*name, *output); err != nil {
		return printErr("Transaction init failed", err)
	}
	if jsonOutput {
		raw, _ := json.Marshal(map[string]any{"version": 1, "command": "data-api transaction init", "status": "needs_review", "function": "api." + *name, "output": *output})
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
	for _, file := range []string{"migration.sql", "client.ts", "fixture.sql", "README.md", "access-checks.json", "data-checks.json"} {
		raw, err := templates.FS.ReadFile("data-api/transactions/" + file)
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
