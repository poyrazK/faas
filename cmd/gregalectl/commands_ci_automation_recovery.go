package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

func cmdCIAutomationRecovery(args []string) int {
	fs := flag.NewFlagSet("ci automation "+args[0], flag.ContinueOnError)
	fs.SetOutput(osStderr)
	binding := fs.String("binding", "", "account-owned binding UUID")
	event := fs.String("event", "", "automatic source event UUID")
	key := fs.String("key", "", "stable recovery request identity (retry)")
	generation := fs.Int64("generation", -1, "current retry_generation from status (retry)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "CI automation recovery accepts named flags only")
		return 2
	}
	method, path, body, headers, err := ciAutomationRecoveryRequest(args[0], *binding, *event, *key, *generation)
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

func ciAutomationRecoveryRequest(command, binding, event, key string, generation int64) (string, string, any, http.Header, error) {
	if !ci.ValidID(binding) || !ci.ValidID(event) {
		return "", "", nil, nil, fmt.Errorf("--binding and --event must be UUIDs")
	}
	base := "/v1/internal/ci/github/automation/" + binding + "/events/" + event
	headers := make(http.Header)
	switch command {
	case "status":
		if key != "" || generation != -1 {
			return "", "", nil, nil, fmt.Errorf("status accepts --binding and --event only")
		}
		return http.MethodGet, base, nil, headers, nil
	case "retry":
		if !ci.ValidIdempotencyKey(key) || generation < 0 || generation > api.CIAutomationRetryMaxGenerations {
			return "", "", nil, nil, fmt.Errorf("retry requires --binding UUID --event UUID --key STABLE_KEY --generation CURRENT_RETRY_GENERATION")
		}
		headers.Set("Idempotency-Key", key)
		return http.MethodPost, base + "/retry", ci.AutomationRetryRequest{ExpectedRetryGeneration: int32(generation)}, headers, nil
	default:
		return "", "", nil, nil, fmt.Errorf("unknown CI automation recovery command %q", command)
	}
}
