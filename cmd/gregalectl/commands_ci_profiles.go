package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/ci"
)

func cmdCIProfile(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "usage: gregalectl ci profile <put|list|status|disable>")
		return 2
	}
	fs := flag.NewFlagSet("ci profile "+args[0], flag.ContinueOnError)
	fs.SetOutput(osStderr)
	name := fs.String("name", "", "immutable profile name (put)")
	id := fs.String("id", "", "profile UUID")
	revision := fs.String("revision", "", "immutable revision UUID (put)")
	binding := fs.String("binding", "", "account-owned GitHub binding UUID (put)")
	templates := make(ciTemplates)
	fs.Var(templates, "template", "JOB=materialized Jobs template UUID; repeat for each job")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "CI profile commands accept named flags only")
		return 2
	}
	method, path, body, err := ciProfileRequest(args[0], *name, *id, ci.ExecutionProfileRequest{RevisionID: *revision, BindingID: *binding, Templates: templates})
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

func ciProfileRequest(command, name, id string, req ci.ExecutionProfileRequest) (string, string, any, error) {
	const base = "/v1/internal/ci/profiles"
	switch command {
	case "put":
		if !ci.ValidName(name) || !ci.ValidExecutionProfileRequest(req) {
			return "", "", nil, fmt.Errorf("put requires --name NAME --revision UUID --binding UUID --template JOB=UUID")
		}
		return http.MethodPut, base + "/" + name, req, nil
	case "list":
		return http.MethodGet, base, nil, nil
	case "status", "disable":
		if !ci.ValidID(id) {
			return "", "", nil, fmt.Errorf("--id must be a profile UUID")
		}
		method := http.MethodGet
		if command == "disable" {
			method = http.MethodDelete
		}
		return method, base + "/" + id, nil, nil
	default:
		return "", "", nil, fmt.Errorf("unknown CI profile command %q", command)
	}
}
