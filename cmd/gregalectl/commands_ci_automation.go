package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/ci"
)

func cmdCIAutomation(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "usage: gregalectl ci automation <put|list|disable|events|status|retry>")
		return 2
	}
	if args[0] == "status" || args[0] == "retry" {
		return cmdCIAutomationRecovery(args)
	}
	fs := flag.NewFlagSet("ci automation "+args[0], flag.ContinueOnError)
	fs.SetOutput(osStderr)
	binding := fs.String("binding", "", "account-owned binding UUID")
	profile := fs.String("profile", "", "saved profile UUID (put)")
	branches := fs.String("push-branches", "", "explicit exact push branches, comma separated (put)")
	prBranches := fs.String("pr-branches", "", "explicit exact PR base branches, comma separated (put)")
	latest := fs.Bool("latest-only", false, "supersede older automatic work using the verified branch head (put)")
	includes := fs.String("include-paths", "", "comma-separated path globs; * and ? within a component, ** across components (put)")
	excludes := fs.String("exclude-paths", "", "comma-separated path globs excluded from triggering CI (put)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "CI automation commands accept named flags only")
		return 2
	}
	if *latest && args[0] != "put" {
		_, _ = fmt.Fprintln(osStderr, "--latest-only requires automation put")
		return 2
	}
	if (*includes != "" || *excludes != "") && args[0] != "put" {
		_, _ = fmt.Fprintln(osStderr, "path filters require automation put")
		return 2
	}
	method, path, body, err := ciAutomationRequest(args[0], *binding, ci.AutomationRequest{ProfileID: *profile, PushBranches: ciBranches(*branches), PullRequestBranches: ciBranches(*prBranches), LatestOnly: *latest, IncludePaths: ciBranches(*includes), ExcludePaths: ciBranches(*excludes)})
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, err)
		return 2
	}
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, err)
		return 1
	}
	var out any
	if err := newOperatorHTTPClient(&sess).doJSON(context.Background(), method, path, body, &out, false, nil); err != nil {
		_, _ = fmt.Fprintln(osStderr, "internal CI:", err)
		return 1
	}
	return emitOperatorJSON(out)
}

func ciAutomationRequest(command, binding string, req ci.AutomationRequest) (string, string, any, error) {
	const base = "/v1/internal/ci/github/automation"
	if command == "list" {
		return http.MethodGet, base, nil, nil
	}
	if !ci.ValidID(binding) {
		return "", "", nil, fmt.Errorf("--binding must be a binding UUID")
	}
	switch command {
	case "put":
		req, err := ci.NormalizeAutomationRequest(req)
		if err != nil {
			return "", "", nil, fmt.Errorf("put requires --profile UUID, explicit --push-branches or --pr-branches BRANCHES, and valid optional path filters")
		}
		return http.MethodPut, base + "/" + binding, req, nil
	case "disable":
		return http.MethodDelete, base + "/" + binding, nil, nil
	case "events":
		return http.MethodGet, base + "/" + binding + "/events", nil, nil
	default:
		return "", "", nil, fmt.Errorf("unknown CI automation command %q", command)
	}
}
