package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

type ciTemplates map[string]string

func (v ciTemplates) String() string { return "job=template-UUID" }
func (v ciTemplates) Set(raw string) error {
	name, id, ok := strings.Cut(raw, "=")
	if !ok || !ci.ValidName(name) || !ci.ValidID(id) {
		return fmt.Errorf("--template requires JOB=UUID")
	}
	if _, exists := v[name]; exists {
		return fmt.Errorf("duplicate --template job %q", name)
	}
	v[name] = id
	return nil
}

type ciExecutionOptions struct {
	profile                                                            string
	id, revision, commit, key, binding, attempt, job, artifact, output string
	templates                                                          ciTemplates
	offset, limit                                                      int
	follow                                                             bool
}

func cmdCIExecution(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "usage: gregalectl ci execution <submit|rerun|list|status|reports|cancel|logs|artifact|archive|archives|archive-status>")
		return 2
	}
	fs := flag.NewFlagSet("ci execution "+args[0], flag.ContinueOnError)
	fs.SetOutput(osStderr)
	o := ciExecutionOptions{templates: make(ciTemplates)}
	fs.StringVar(&o.id, "id", "", "execution UUID")
	fs.StringVar(&o.profile, "profile", "", "saved execution profile UUID; replaces revision/binding/template flags")
	fs.StringVar(&o.revision, "revision", "", "immutable revision UUID")
	fs.StringVar(&o.commit, "commit", "", "full lowercase commit SHA")
	fs.StringVar(&o.key, "key", "", "stable admission identity")
	fs.StringVar(&o.binding, "binding", "", "account-owned GitHub binding UUID")
	fs.Var(o.templates, "template", "JOB=materialized Jobs template UUID; repeat for each job")
	fs.StringVar(&o.attempt, "attempt", "", "attempt UUID (logs)")
	fs.IntVar(&o.offset, "offset", 0, "retained log byte offset")
	fs.IntVar(&o.limit, "limit", api.CILogPageMaxBytes, "maximum log bytes in this page")
	fs.BoolVar(&o.follow, "follow", false, "follow attempt logs; resume with --offset")
	fs.StringVar(&o.job, "job", "", "CI job name (artifact)")
	fs.StringVar(&o.artifact, "path", "", "artifact path from execution status")
	fs.StringVar(&o.output, "output", "", "new local artifact file; never overwritten")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "CI execution commands accept named flags only")
		return 2
	}
	method, path, body, headers, err := ciExecutionRequest(args[0], o)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, err)
		return 2
	}
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, err)
		return 1
	}
	client := newOperatorHTTPClient(&sess)
	if args[0] == "artifact" {
		if err := client.downloadCIArtifact(context.Background(), o, path); err != nil {
			_, _ = fmt.Fprintln(osStderr, "internal CI:", err)
			return 1
		}
		_, _ = fmt.Fprintln(osStdout, o.output)
		return 0
	}
	if args[0] == "logs" {
		if o.follow {
			return runCILogFollow(client, o)
		}
		var page ci.LogPage
		if err := client.doJSONWithHeaders(context.Background(), method, path, body, &page, false, nil, headers); err != nil {
			_, _ = fmt.Fprintln(osStderr, "internal CI:", err)
			return 1
		}
		if jsonEnabled() {
			return emitOperatorJSON(page)
		}
		if _, err := osStdout.Write(page.Data); err != nil {
			_, _ = fmt.Fprintln(osStderr, err)
			return 1
		}
		_, _ = fmt.Fprintf(osStderr, "next_offset=%d complete=%t truncated=%t\n", page.NextOffset, page.Complete, page.Truncated)
		return 0
	}
	var out any
	if err := client.doJSONWithHeaders(context.Background(), method, path, body, &out, false, nil, headers); err != nil {
		_, _ = fmt.Fprintln(osStderr, "internal CI:", err)
		return 1
	}
	return emitOperatorJSON(out)
}

func ciExecutionRequest(command string, o ciExecutionOptions) (string, string, any, http.Header, error) {
	if o.follow && command != "logs" {
		return "", "", nil, nil, fmt.Errorf("--follow requires the logs command")
	}
	const base = "/v1/internal/ci/executions"
	headers := make(http.Header)
	if command == "archives" {
		return http.MethodGet, "/v1/internal/ci/archives", nil, headers, nil
	}
	if command == "list" {
		return http.MethodGet, base, nil, headers, nil
	}
	if command == "submit" {
		if o.profile != "" {
			if !ci.ValidID(o.profile) || o.revision != "" || o.binding != "" || len(o.templates) != 0 || !ci.ValidCommitSHA(o.commit) || !ci.ValidIdempotencyKey(o.key) {
				return "", "", nil, nil, fmt.Errorf("submit with --profile UUID requires --commit SHA --key STABLE_KEY and no revision/binding/template overrides")
			}
			headers.Set("Idempotency-Key", o.key)
			return http.MethodPost, base, ci.JobsSubmitRequest{SubmitRequest: ci.SubmitRequest{CommitSHA: o.commit}, ProfileID: o.profile}, headers, nil
		}
		if !ci.ValidID(o.revision) || !ci.ValidCommitSHA(o.commit) || !ci.ValidID(o.binding) || !ci.ValidIdempotencyKey(o.key) || len(o.templates) < 1 || len(o.templates) > api.CIMaxJobs {
			return "", "", nil, nil, fmt.Errorf("submit requires --revision UUID --commit SHA --binding UUID --key STABLE_KEY --template JOB=UUID")
		}
		headers.Set("Idempotency-Key", o.key)
		return http.MethodPost, base, ci.JobsSubmitRequest{SubmitRequest: ci.SubmitRequest{RevisionID: o.revision, CommitSHA: o.commit}, BindingID: o.binding, Templates: o.templates}, headers, nil
	}
	if !ci.ValidID(o.id) {
		return "", "", nil, nil, fmt.Errorf("--id must be an execution UUID")
	}
	path := base + "/" + o.id
	switch command {
	case "rerun":
		if !ci.ValidIdempotencyKey(o.key) || o.profile != "" || o.revision != "" || o.commit != "" || o.binding != "" || len(o.templates) != 0 ||
			o.attempt != "" || o.job != "" || o.artifact != "" || o.output != "" || o.offset != 0 {
			return "", "", nil, nil, fmt.Errorf("rerun requires --id UUID --key STABLE_KEY and no configuration, job or attempt overrides")
		}
		headers.Set("Idempotency-Key", o.key)
		return http.MethodPost, path + "/rerun", struct{}{}, headers, nil
	case "status":
		return http.MethodGet, path, nil, headers, nil
	case "reports":
		return http.MethodGet, path + "/reports", nil, headers, nil
	case "cancel":
		return http.MethodPost, path + "/cancel", nil, headers, nil
	case "archive":
		return http.MethodPost, path + "/archive", struct{}{}, headers, nil
	case "archive-status":
		return http.MethodGet, "/v1/internal/ci/archives/" + o.id, nil, headers, nil
	case "logs":
		if !ci.ValidID(o.attempt) || o.offset < 0 || o.limit < 1 || o.limit > api.CILogPageMaxBytes {
			return "", "", nil, nil, fmt.Errorf("logs requires --attempt UUID and bounded --offset/--limit")
		}
		return http.MethodGet, fmt.Sprintf("%s/attempts/%s/logs?offset=%d&limit=%d", path, o.attempt, o.offset, o.limit), nil, headers, nil
	case "artifact":
		if !ci.ValidName(o.job) || !validCIArtifactPath(o.artifact) || o.output == "" {
			return "", "", nil, nil, fmt.Errorf("artifact requires --job NAME --path ARTIFACT --output NEW_FILE")
		}
		parts := strings.Split(o.artifact, "/")
		for i := range parts {
			parts[i] = url.PathEscape(parts[i])
		}
		return http.MethodGet, path + "/jobs/" + o.job + "/artifacts/" + strings.Join(parts, "/"), nil, headers, nil
	default:
		return "", "", nil, nil, fmt.Errorf("unknown CI execution command %q", command)
	}
}

func validCIArtifactPath(value string) bool {
	if value == "" || len(value) > api.CIStorageKeyMaxBytes || strings.ContainsAny(value, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
