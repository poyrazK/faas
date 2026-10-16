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
		_, _ = fmt.Fprintln(osStderr, "usage: gregalectl ci automation <put|list|disable|events>")
		return 2
	}
	fs := flag.NewFlagSet("ci automation "+args[0], flag.ContinueOnError)
	fs.SetOutput(osStderr)
	binding := fs.String("binding", "", "account-owned binding UUID")
	profile := fs.String("profile", "", "saved profile UUID (put)")
	branches := fs.String("push-branches", "", "explicit exact push branches, comma separated (put)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "CI automation commands accept named flags only")
		return 2
	}
	method, path, body, err := ciAutomationRequest(args[0], *binding, ci.AutomationRequest{ProfileID: *profile, PushBranches: ciBranches(*branches)})
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
			return "", "", nil, fmt.Errorf("put requires --profile UUID and explicit --push-branches BRANCHES")
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
