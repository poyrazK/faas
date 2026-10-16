package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

func cmdCIChecks(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "usage: gregalectl ci checks <list|status|retry>")
		return 2
	}
	fs := flag.NewFlagSet("ci checks "+args[0], flag.ContinueOnError)
	fs.SetOutput(osStderr)
	id := fs.String("id", "", "execution UUID")
	key := fs.String("key", "", "stable retry request identity")
	generation := fs.Int64("generation", -1, "current retry_generation from status (retry)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "CI Check commands accept named flags only")
		return 2
	}
	method, path, body, headers, err := ciChecksRequest(args[0], *id, *key, *generation)
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

func ciChecksRequest(command, id, key string, generation int64) (string, string, any, http.Header, error) {
	const base = "/v1/internal/ci/checks"
	headers := make(http.Header)
	if command == "list" && id == "" && key == "" && generation == -1 {
		return http.MethodGet, base, nil, headers, nil
	}
	if !ci.ValidID(id) {
		return "", "", nil, nil, fmt.Errorf("--id must be an execution UUID")
	}
	switch command {
	case "status":
		if key != "" || generation != -1 {
			return "", "", nil, nil, fmt.Errorf("status accepts --id only")
		}
		return http.MethodGet, base + "/" + id, nil, headers, nil
	case "retry":
		if !ci.ValidIdempotencyKey(key) || generation < 0 || generation > api.CICheckRetryMaxGenerations {
			return "", "", nil, nil, fmt.Errorf("retry requires --id UUID --key STABLE_KEY --generation CURRENT_RETRY_GENERATION")
		}
		headers.Set("Idempotency-Key", key)
		return http.MethodPost, base + "/" + id + "/retry", ci.CheckRetryRequest{ExpectedRetryGeneration: int32(generation)}, headers, nil
	default:
		return "", "", nil, nil, fmt.Errorf("unknown or invalid CI Check command %q", command)
	}
}
