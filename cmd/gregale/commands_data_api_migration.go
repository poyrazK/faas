package main

import (
	"errors"
	"flag"
	"os"
)

type dataAPIMigrationOptions struct {
	AccessChecks string
	Verify       bool
	PlanCheck    bool
	PlanBudget   string
	Explain      bool
	Collection   string
	PlanBaseline string
	PlanScenario string
	Dependencies string
	FromVersion  int
	Fixture      string
	DataCheck    string
}

func cmdDataAPIMigration(args []string) int {
	if len(args) > 0 && args[0] == "guide" {
		return cmdDataAPIMigrationGuide(args[1:])
	}
	if len(args) == 0 || args[0] != "preview" {
		PrintUsage(os.Stderr, "usage: gregale data-api migration preview --baseline FILE [--directory DIR] [--config FILE] [--output FILE] [--dependencies FILE] [--approval FILE] [--replay FILE] [--scenario NAME] [--from-version N] [--fixture FILE] [--data-check FILE]", "data-api")
		return 1
	}
	fs := newFlagSet("data-api migration preview", flag.ContinueOnError)
	directory := fs.String("directory", ".", "Data API project directory")
	baseline := fs.String("baseline", "", "read-only schema contract baseline")
	config := fs.String("config", "", "project-relative development config")
	output := fs.String("output", "", "committed types path (default: project config or client/src/database.types.ts)")
	dependencies := fs.String("dependencies", "", "customer-declared consumer dependency manifest")
	approval := fs.String("approval", "", "fingerprint-bound breaking change approvals")
	replay := fs.String("replay", "", "saved request collection")
	scenario := fs.String("scenario", "", "one named replay scenario")
	fromVersion := fs.Int("from-version", 0, "upgrade from an existing numbered migration version")
	fixture := fs.String("fixture", "", "SQL data fixture for the older schema (default: configured seed)")
	dataCheck := fs.String("data-check", "", "JSON manifest of post-migration SQL data assertions")
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	fromSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "from-version" {
			fromSet = true
		}
	})
	if (fromSet && (*fromVersion < 1 || *fromVersion > 9999)) || (*fixture != "" && !fromSet) {
		return printErr("Invalid upgrade rehearsal", errors.New("use --from-version 1..9999 and select a version when supplying --fixture"))
	}
	if fs.NArg() != 0 || *baseline == "" || *directory == "" || (*scenario != "" && *replay == "") {
		return printErr("Invalid migration preview", errors.New("provide --baseline FILE and use --scenario only with --replay"))
	}
	devArgs := []string{"--directory", *directory, "--baseline", *baseline, "--once", "--check", "--check-types", "--check-breaking", "--port", "0"}
	for _, option := range [][2]string{{"--config", *config}, {"--output", *output}, {"--approval", *approval}, {"--replay", *replay}, {"--scenario", *scenario}} {
		if option[1] != "" {
			devArgs = append(devArgs, option[0], option[1])
		}
	}
	return runDataAPIDev(devArgs, &dataAPIMigrationOptions{Dependencies: *dependencies, FromVersion: *fromVersion, Fixture: *fixture, DataCheck: *dataCheck})
}
