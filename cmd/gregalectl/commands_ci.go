package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

// cmdCIDispatch keeps internal CI out of the customer CLI. Planning is local;
// metadata operations use the ordinary authenticated operator session.
func cmdCIDispatch(args []string) int {
	if len(args) > 0 && args[0] == "checks" {
		return cmdCIChecks(args[1:])
	}
	if len(args) > 0 && args[0] == "automation" {
		return cmdCIAutomation(args[1:])
	}
	if len(args) > 0 && args[0] == "profile" {
		return cmdCIProfile(args[1:])
	}
	if len(args) > 0 && args[0] == "execution" {
		return cmdCIExecution(args[1:])
	}
	if len(args) > 0 && args[0] == "github" {
		return cmdCIGitHub(args[1:])
	}
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "usage: gregalectl ci <validate|plan|health|capabilities|put|revisions|revision|run|runs|status|events|cancel|github|execution|profile|automation|checks>")
		return 2
	}
	fs := flag.NewFlagSet("ci "+args[0], flag.ContinueOnError)
	fs.SetOutput(osStderr)
	file := fs.String("file", "gregale-ci.yaml", "local CI definition")
	name := fs.String("name", "", "pipeline name (put)")
	id := fs.String("id", "", "run or revision UUID")
	revision := fs.String("revision", "", "immutable revision UUID (run)")
	commit := fs.String("commit", "", "full lowercase commit SHA (run)")
	key := fs.String("key", "", "stable request identity (run)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "CI commands accept named flags only")
		return 2
	}
	if args[0] == "validate" || args[0] == "plan" {
		_, plan, err := readCIDefinition(*file)
		if err != nil {
			_, _ = fmt.Fprintln(osStderr, err)
			return 1
		}
		if args[0] == "plan" || jsonEnabled() {
			return emitOperatorJSON(plan)
		}
		_, _ = fmt.Fprintf(osStdout, "Valid internal CI definition: %s (%d jobs); compute unavailable\n", plan.DefinitionDigest, len(plan.Jobs))
		return 0
	}
	method, path, body, headers, err := ciMetadataRequest(args[0], *file, *name, *id, *revision, *commit, *key)
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
	if err := newOperatorHTTPClient(&sess).doJSONWithHeaders(context.Background(), method, path, body, &out, false, nil, headers); err != nil {
		_, _ = fmt.Fprintln(osStderr, "internal CI:", err)
		return 1
	}
	return emitOperatorJSON(out)
}

func readCIDefinition(filename string) (string, ci.Plan, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return "", ci.Plan{}, fmt.Errorf("inspect CI definition: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", ci.Plan{}, fmt.Errorf("CI definition must be a regular file")
	}
	// This operator-selected metadata file is checked before opening and before
	// reading; it is never uploaded as an executable source bundle.
	f, err := os.Open(filename) //nolint:forbidigo // Local CI definition; regular-file checks bracket opening and bounded reads follow.
	if err != nil {
		return "", ci.Plan{}, fmt.Errorf("open CI definition: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err = f.Stat()
	if err != nil {
		return "", ci.Plan{}, fmt.Errorf("inspect CI definition: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", ci.Plan{}, fmt.Errorf("CI definition must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, api.CIDefinitionMaxBytes+1))
	if err != nil {
		return "", ci.Plan{}, fmt.Errorf("read CI definition: %w", err)
	}
	def, err := ci.Parse(data)
	if err != nil {
		return "", ci.Plan{}, err
	}
	plan, _, err := ci.Compile(def)
	return string(data), plan, err
}

func ciMetadataRequest(command, file, name, id, revision, commit, key string) (string, string, any, http.Header, error) {
	const base = "/v1/internal/ci/"
	headers := make(http.Header)
	switch command {
	case "health", "capabilities", "revisions", "runs":
		path := base + command
		if command == "revisions" {
			path = base + "pipelines"
		}
		return http.MethodGet, path, nil, headers, nil
	case "revision", "status", "events", "cancel":
		if !ci.ValidID(id) {
			return "", "", nil, nil, fmt.Errorf("--id must be a UUID")
		}
		path, method := base+"runs/"+id, http.MethodGet
		if command == "revision" {
			path = base + "revisions/" + id
		}
		if command == "events" {
			path += "/events"
		}
		if command == "cancel" {
			path += "/cancel"
			method = http.MethodPost
		}
		return method, path, nil, headers, nil
	case "put":
		if !ci.ValidName(name) {
			return "", "", nil, nil, fmt.Errorf("--name must be a CI pipeline name")
		}
		data, _, err := readCIDefinition(file)
		return http.MethodPut, base + "pipelines/" + name, map[string]string{"definition": data}, headers, err
	case "run":
		if !ci.ValidID(revision) || !ci.ValidCommitSHA(commit) || !ci.ValidIdempotencyKey(key) {
			return "", "", nil, nil, fmt.Errorf("run requires --revision UUID --commit SHA --key STABLE_KEY")
		}
		headers.Set("Idempotency-Key", key)
		return http.MethodPost, base + "runs", ci.SubmitRequest{RevisionID: revision, CommitSHA: commit}, headers, nil
	default:
		return "", "", nil, nil, fmt.Errorf("unknown CI command %q", command)
	}
}
