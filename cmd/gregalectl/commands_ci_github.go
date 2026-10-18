package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/ci"
)

func cmdCIGitHub(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "usage: gregalectl ci github <bind|bindings|unbind>")
		return 2
	}
	fs := flag.NewFlagSet("ci github "+args[0], flag.ContinueOnError)
	fs.SetOutput(osStderr)
	name := fs.String("name", "", "binding name")
	revision := fs.String("revision", "", "immutable pipeline revision UUID")
	installation := fs.Int64("installation", 0, "account-owned GitHub installation ID")
	repository := fs.Int64("repository-id", 0, "immutable GitHub repository ID")
	push := fs.String("push-branches", "", "comma-separated exact push branches")
	prs := fs.String("pr-branches", "", "comma-separated exact PR base branches")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "CI GitHub commands accept named flags only")
		return 2
	}
	req := ci.GitHubBindingRequest{RevisionID: *revision, InstallationID: *installation, RepositoryID: *repository, PushBranches: ciBranches(*push), PullRequestBranches: ciBranches(*prs)}
	method, path, body, err := ciGitHubRequest(args[0], *name, req)
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
	if err := newOperatorHTTPClient(&sess).doJSONWithHeaders(context.Background(), method, path, body, &out, false, nil, nil); err != nil {
		_, _ = fmt.Fprintln(osStderr, "internal CI GitHub:", err)
		return 1
	}
	if method == http.MethodDelete {
		return 0
	}
	return emitOperatorJSON(out)
}

func ciBranches(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func ciGitHubRequest(command, name string, req ci.GitHubBindingRequest) (string, string, any, error) {
	const base = "/v1/internal/ci/github/bindings"
	if command == "bindings" {
		return http.MethodGet, base, nil, nil
	}
	if !ci.ValidName(name) {
		return "", "", nil, fmt.Errorf("--name must be a CI binding name")
	}
	if command == "unbind" {
		return http.MethodDelete, base + "/" + name, nil, nil
	}
	if command != "bind" {
		return "", "", nil, fmt.Errorf("unknown CI GitHub command %q", command)
	}
	normalized, err := ci.NormalizeGitHubBinding(req)
	if err != nil {
		return "", "", nil, err
	}
	return http.MethodPut, base + "/" + name, normalized, nil
}
