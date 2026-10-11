package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/cmd/gregale/automationtemplates"
)

func cmdAutomationsInit(args []string) int {
	fs := newFlagSet("automations init", flag.ContinueOnError)
	template := fs.String("template", "", "starter name")
	destination := fs.String("path", "", "new directory (defaults to the template name)")
	list := fs.Bool("list", false, "list automation starters without creating files")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *list {
		if *template != "" || *destination != "" {
			return printErr("Invalid automation init options", fmt.Errorf("--list cannot be combined with --template or --path"))
		}
		if jsonOutput {
			return jsonOut(writeJSON(struct {
				Templates []string `json:"templates"`
			}{automationtemplates.Names}))
		}
		_, err := fmt.Fprintln(osStdout, strings.Join(automationtemplates.Names, "\n"))
		if err != nil {
			return printErr("Could not list automation templates", err)
		}
		return 0
	}
	if strings.TrimSpace(*template) == "" {
		PrintUsage(os.Stderr, "usage: gregale automations init --template <name> [--path <new-directory>] | --list", "automations")
		return 1
	}
	if *destination == "" {
		*destination = *template
	}
	absolute, err := filepath.Abs(*destination)
	if err != nil {
		return printErr("Invalid destination", err)
	}
	files, err := automationtemplates.Materialize(*template, absolute)
	if err != nil {
		return printErr("Could not create automation starter (available: "+strings.Join(automationtemplates.Names, ", ")+")", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			Template string   `json:"template"`
			Path     string   `json:"path"`
			Files    []string `json:"files"`
		}{*template, absolute, files}))
	}
	PrintProgress(osStdout, "Created %s in %s", *template, absolute)
	PrintProgress(osStdout, "Read README.md for handler and integration setup, then simulate with the supplied sample files before publishing.")
	return 0
}
