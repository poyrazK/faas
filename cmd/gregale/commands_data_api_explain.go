package main

import (
	"errors"
	"flag"
)

func cmdDataAPIExplain(args []string) int {
	fs := newFlagSet("data-api explain", flag.ContinueOnError)
	directory := fs.String("directory", ".", "Data API project directory")
	config := fs.String("config", "", "project-relative development config")
	collection := fs.String("collection", "", "saved API request collection")
	fixture := fs.String("fixture", "", "representative SQL fixture (default: configured seed)")
	scenario := fs.String("scenario", "", "one named collection scenario")
	baseline := fs.String("baseline", "", "previous explain JSON report for estimated plan comparison")
	check := fs.Bool("check", false, "fail when query budgets or coverage checks fail")
	budget := fs.String("budget", "", "version 1 query budget JSON (requires --check and --baseline)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *directory == "" || *collection == "" || (*check && (*budget == "" || *baseline == "")) || (!*check && *budget != "") {
		return printErr("Invalid explain options", errors.New("provide --collection FILE; --check requires --baseline FILE and --budget FILE"))
	}
	devArgs := []string{"--directory", *directory, "--once", "--port", "0"}
	if *config != "" {
		devArgs = append(devArgs, "--config", *config)
	}
	return runDataAPIDev(devArgs, &dataAPIMigrationOptions{Explain: true, PlanCheck: *check, PlanBudget: *budget, Collection: *collection, Fixture: *fixture, PlanBaseline: *baseline, PlanScenario: *scenario})
}
