package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// appProjectVerbs are the app verbs that also take --project PROJECT and run
// once per workload of that project, in workload order. Compose projects of
// ten services otherwise needed a shell loop over every slug for a restart,
// a health check or a setting change.
var appProjectVerbs = []string{subRestart, subHealth, "update"}

// projectFlagValue returns the --project value in args and args without it.
func projectFlagValue(args []string) (string, []string, bool) {
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		name, value, hasValue := strings.Cut(args[i], "=")
		if name != "--project" && name != "-project" {
			continue
		}
		rest := append([]string{}, args[:i]...)
		if hasValue {
			return value, append(rest, args[i+1:]...), true
		}
		if i+1 < len(args) {
			return args[i+1], append(rest, args[i+2:]...), true
		}
		return "", rest, true
	}
	return "", args, false
}

// isAppProjectCommand reports whether args (after `app`) are `<verb> ...
// --project P` for a verb that fans out over a project.
func isAppProjectCommand(args []string) bool {
	if len(args) == 0 || !slices.Contains(appProjectVerbs, args[0]) {
		return false
	}
	_, _, ok := projectFlagValue(args[1:])
	return ok
}

// cmdAppProject runs `app <verb> --project P [flags]` for each of P's
// workloads and returns the worst exit code.
func cmdAppProject(args []string) int {
	verb := args[0]
	project, rest, _ := projectFlagValue(args[1:])
	if project == "" || !api.ValidAppSlug(project) {
		PrintUsage(osStderr, "usage: gregale app "+verb+" --project <project> [flags]", "apps")
		return 1
	}
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		return printErr("Conflicting targets", fmt.Errorf("name one app or --project %s, not both", project))
	}
	if jsonOutput {
		return printErr("Unsupported output", errors.New("--json with --project is not supported; run the command per app, listing them with `gregale project info "+project+" --json`"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	info, err := client.GetProject(context.Background(), project)
	if err != nil {
		return printErr("Could not read project", err)
	}
	slugs := make([]string, 0, len(info.Workloads))
	for _, workload := range info.Workloads {
		slugs = append(slugs, workload.Slug)
	}
	if len(slugs) == 0 {
		return printErr("Nothing to "+verb, fmt.Errorf("project %s has no apps", project))
	}
	return runAppProjectVerb(verb, slugs, rest, appProjectRunner(verb))
}

func appProjectRunner(verb string) func(slug string, args []string) int {
	switch verb {
	case subRestart:
		return cmdAppRestart
	case subHealth:
		return cmdAppHealth
	default:
		return func(slug string, args []string) int { return cmdApp(append([]string{slug}, args...)) }
	}
}

// runAppProjectVerb runs one verb per app, continuing past failures so one
// broken service does not hide the others, and summarizes the outcome.
func runAppProjectVerb(verb string, slugs, args []string, run func(slug string, args []string) int) int {
	worst := 0
	var failed []string
	for i, slug := range slugs {
		if i > 0 {
			_, _ = fmt.Fprintln(osStdout)
		}
		_, _ = fmt.Fprintf(osStdout, "== %s (%d/%d)\n", slug, i+1, len(slugs))
		if code := run(slug, append([]string{}, args...)); code != 0 {
			failed = append(failed, slug)
			worst = max(worst, code)
		}
	}
	_, _ = fmt.Fprintln(osStdout)
	if len(failed) == 0 {
		_, _ = fmt.Fprintf(osStdout, "%s: all %d apps succeeded\n", verb, len(slugs))
	} else {
		_, _ = fmt.Fprintf(osStdout, "%s: %d of %d apps failed: %s\n", verb, len(failed), len(slugs), strings.Join(failed, ", "))
	}
	return worst
}
