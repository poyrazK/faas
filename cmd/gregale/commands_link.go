package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	linkUsage    = "usage: gregale link [<project-slug>] [--interactive] [--app <slug>] [--env <environment>] [--no-gitignore]"
	contextUsage = "usage: gregale context [--check] [--verify [--timeout 10s]] | gregale context --local-config [--problems-only]"
)

func cmdLink(args []string) int {
	fs := newFlagSet("link", flag.ContinueOnError)
	interactive := fs.Bool("interactive", false, "choose project, app, and environment before saving")
	app := fs.String("app", "", "workload/app slug to use for app-scoped commands")
	environment := fs.String("environment", "", "project environment to use as the default scope")
	noGitignore := fs.Bool("no-gitignore", false, "do not add .gregale/ to the repository .gitignore")
	flags, positional := splitArgsForFlags(args, "no-gitignore", "interactive")
	if err := fs.Parse(flags); err != nil {
		// FlagSet has already emitted the parse failure (as one Problem in
		// --json mode), so adding PrintUsage here would produce a second error.
		return 1
	}
	if *interactive {
		invalid := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name != "interactive" && f.Name != "no-gitignore" {
				invalid = true
			}
		})
		if invalid || len(positional) > 1 || len(positional) == 1 && !api.ValidProjectSlug(positional[0]) {
			return printErr("Invalid interactive link flags", errors.New("use link --interactive with an optional project slug and --no-gitignore; choose app and environment in the flow"))
		}
		if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
			return printErr("Interactive terminal required", errors.New("use link PROJECT --app APP --environment ENV for scripts"))
		}
		project := ""
		if len(positional) == 1 {
			project = positional[0]
		}
		return cmdLinkInteractive(project, *noGitignore)
	}
	if len(positional) != 1 || !api.ValidProjectSlug(positional[0]) {
		PrintUsage(os.Stderr, linkUsage, "link")
		return 1
	}
	if *app != "" && !api.ValidAppSlug(*app) {
		return printErr("Invalid --app", fmt.Errorf("%q is not a valid app slug", *app))
	}
	if *environment != "" && !api.ValidProjectEnvironmentSlug(*environment) {
		return printErr("Invalid --environment", fmt.Errorf("%q is not a valid project environment slug", *environment))
	}
	cwd, err := os.Getwd()
	if err != nil {
		return printErr("Could not read current directory", err)
	}
	root, err := projectContextRoot(cwd)
	if err != nil {
		return printErr("Could not locate project context root", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	project, err := client.GetProject(context.Background(), positional[0])
	if err != nil {
		return printErr("Could not validate project", err)
	}
	selectedApp := *app
	if selectedApp == "" && len(project.Workloads) == 1 {
		selectedApp = project.Workloads[0].Slug
	}
	if selectedApp != "" {
		found := false
		for _, workload := range project.Workloads {
			if workload.Slug == selectedApp {
				found = true
				break
			}
		}
		if !found {
			return printErr("Invalid --app", fmt.Errorf("%q is not a workload in project %q", selectedApp, positional[0]))
		}
	}
	if *environment != "" {
		if _, err := client.GetProjectEnvironment(context.Background(), positional[0], *environment); err != nil {
			return printErr("Could not validate project environment", err)
		}
	}
	previous, _, previousErr := linkedProjectContext(cwd)
	if previousErr != nil && !errors.Is(previousErr, errProjectContextNotFound) {
		return printErr("Could not read existing project context", previousErr)
	}
	context := localProjectContext{
		Version:     projectContextVersion,
		Project:     positional[0],
		App:         selectedApp,
		Environment: *environment,
	}
	return saveLinkContext(cwd, root, context, previous, previousErr == nil, *noGitignore, len(project.Workloads))
}

func saveLinkContext(cwd, root string, context, previous localProjectContext, hadPrevious, noGitignore bool, workloadCount int) int {
	path, err := saveProjectContext(root, context)
	if err != nil {
		return printErr("Could not save project context", err)
	}
	if !noGitignore {
		if err := ensureProjectContextIgnored(root); err != nil {
			return printErr("Project linked, but could not update .gitignore", err)
		}
	}
	receipt := projectContextReceipt{Context: context, Path: displayProjectContextPath(cwd, path)}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	if hadPrevious && previous.Project != context.Project {
		if previous.Project == "" {
			PrintProgress(osStdout, "Replaced saved standalone app %s.", previous.App)
		} else {
			PrintProgress(osStdout, "Re-linked from project %s.", previous.Project)
		}
	}
	PrintOK(osStdout, "Linked to project %s.", context.Project)
	renderProjectContext(osStdout, receipt)
	if context.App == "" {
		PrintProgress(osStdout, "This project has %d workloads; pass --app or relink with --app for app-scoped commands.", workloadCount)
	}
	return 0
}

func cmdUnlink(args []string) int {
	if len(args) != 0 {
		PrintUsage(os.Stderr, "usage: gregale unlink", "link")
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		return printErr("Could not read current directory", err)
	}
	_, path, err := linkedProjectContext(cwd)
	if errors.Is(err, errProjectContextNotFound) {
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"linked": false}))
		}
		PrintProgress(osStdout, "No linked project context found.")
		return 0
	}
	if err != nil {
		return printErr("Could not read project context", err)
	}
	if err := os.Remove(path); err != nil {
		return printErr("Could not unlink project context", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"linked": false, "path": displayProjectContextPath(cwd, path)}))
	}
	PrintOK(osStdout, "Unlinked project context (%s).", displayProjectContextPath(cwd, path))
	return 0
}

func cmdContext(args []string) int {
	if len(args) > 0 && (args[0] == "save" || args[0] == "use" || args[0] == "presets") {
		return cmdContextPresets(args)
	}
	if len(args) > 0 && args[0] == "set" {
		return cmdContextSet(args[1:])
	}
	fs := newFlagSet("context", flag.ContinueOnError)
	check := fs.Bool("check", false, "fail when a linked project or default app is missing; local check only")
	verify := fs.Bool("verify", false, "verify authenticated account and linked targets with read-only API requests")
	localConfig := fs.Bool("local-config", false, "summarize saved project configuration files offline")
	problemsOnly := fs.Bool("problems-only", false, "show only local configuration problems; requires --local-config")
	timeout := fs.Duration("timeout", 10*time.Second, "overall verification deadline; requires --verify")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	timeoutSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "timeout" {
			timeoutSet = true
		}
	})
	if fs.NArg() != 0 || *timeout <= 0 || timeoutSet && !*verify {
		PrintUsage(os.Stderr, contextUsage, "context")
		return 1
	}
	if *problemsOnly && !*localConfig {
		return printErr("Invalid local configuration options", errors.New("--problems-only requires --local-config"))
	}
	if *localConfig {
		if *check || *verify || timeoutSet {
			return printErr("Invalid local configuration options", errors.New("use --local-config separately from --check, --verify, and --timeout"))
		}
		return cmdContextLocalConfig(*problemsOnly)
	}
	if _, err := loadCLIConfig(); err != nil {
		return printErr("Could not read connection configuration", err)
	}
	configPath, err := absoluteContextConfigPath()
	if err != nil {
		return printErr("Could not locate connection configuration", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return printErr("Could not read current directory", err)
	}
	context, path, err := linkedProjectContext(cwd)
	if err != nil && !errors.Is(err, errProjectContextNotFound) {
		return printErr("Could not read project context", err)
	}
	if *verify {
		if err := validateContextVerifyEndpoint(); err != nil {
			return printErr("Invalid verification endpoint", err)
		}
	}
	connection := effectiveConnectionContext()
	overview := projectTargeting(context, path, configPath, connection)
	var verification *contextVerification
	if *verify {
		verification = verifyProjectConnection(context, path != "", *timeout)
	}
	if errors.Is(err, errProjectContextNotFound) {
		if jsonOutput {
			receipt := map[string]any{"connection": connection, "targeting": overview}
			if verification != nil {
				receipt["verification"] = verification
			}
			if code := jsonOut(writeJSON(receipt)); code != 0 {
				return code
			}
		} else {
			renderConnectionContext()
			_, _ = fmt.Fprintln(osStdout, "No linked project. Run gregale link <project-slug> to link this checkout.")
			renderProjectTargeting(overview)
		}
	} else {
		receipt := projectContextReceipt{Context: context, Path: displayProjectContextPath(cwd, path), Connection: connection, Targeting: overview, Verification: verification}
		if jsonOutput {
			if code := jsonOut(writeJSON(receipt)); code != 0 {
				return code
			}
		} else {
			renderConnectionContext()
			if context.Project == "" {
				PrintOK(osStdout, "Saved standalone app context")
			} else {
				PrintOK(osStdout, "Linked project context")
			}
			renderProjectContext(osStdout, receipt)
			renderProjectTargeting(overview)
		}
	}
	if verification != nil {
		if !jsonOutput {
			renderContextVerification(verification)
		}
		if verification.ExitCode != 0 {
			return verification.ExitCode
		}
	}
	if *check && overview.Status != "ready for linked app defaults" {
		return 1
	}
	return 0
}
