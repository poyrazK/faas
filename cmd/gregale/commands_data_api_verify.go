package main

import (
	"errors"
	"flag"
)

func cmdDataAPIVerify(args []string) int {
	if len(args) > 0 && args[0] == "release" {
		return cmdDataAPIVerifyRelease(args[1:])
	}
	if len(args) > 0 && args[0] == "coverage" {
		return cmdDataAPIVerifyCoverage(args[1:])
	}
	if len(args) > 0 && args[0] == "init" {
		return cmdDataAPIVerifyInit(args[1:])
	}
	fs := newFlagSet("data-api verify", flag.ContinueOnError)
	directory := fs.String("directory", ".", "Data API project directory")
	config := fs.String("config", "", "project-relative development config")
	checks := fs.String("checks", "", "version 1 named SQL assertion manifest")
	access := fs.String("access-checks", "", "request collection or version 3/4/5 guided RLS check manifest")
	fixture := fs.String("fixture", "", "SQL fixture for the current schema (default: configured seed)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *directory == "" || (*checks == "" && *access == "") {
		return printErr("Invalid verification options", errors.New("provide --checks FILE or --access-checks FILE and a project directory"))
	}
	devArgs := []string{"--directory", *directory, "--once", "--port", "0"}
	if *config != "" {
		devArgs = append(devArgs, "--config", *config)
	}
	return runDataAPIDev(devArgs, &dataAPIMigrationOptions{Verify: true, AccessChecks: *access, DataCheck: *checks, Fixture: *fixture})
}
