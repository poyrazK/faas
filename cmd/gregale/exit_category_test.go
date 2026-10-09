package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSharedExitContractHumanAndJSON(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		code     int
		category string
	}{
		{"arguments", errors.New("invalid argument"), 1, "invalid_request"},
		{"missing credential", errAuth(errors.New("log in")), 2, "authentication"},
		{"unauthorized", &APIError{Problem: api.Problem{Status: 401, Code: "unauthorized", Title: "Unauthorized"}}, 2, "authentication"},
		{"permission", &APIError{Problem: api.Problem{Status: 403, Code: "forbidden", Title: "Forbidden"}}, 6, "permission_denied"},
		{"missing", &APIError{Problem: api.Problem{Status: 404, Code: "not_found", Title: "Not found"}}, 4, "not_found"},
		{"gone", &APIError{Problem: api.Problem{Status: 410, Code: "gone", Title: "Gone"}}, 4, "not_found"},
		{"conflict", &APIError{Problem: api.Problem{Status: 409, Code: "conflict", Title: "Conflict"}}, 5, "conflict"},
		{"precondition", &APIError{Problem: api.Problem{Status: 412, Code: "precondition_failed", Title: "Precondition failed"}}, 5, "conflict"},
		{"rate limit", &APIError{Problem: api.Problem{Status: 429, Code: "rate_limited", Title: "Rate limited"}}, 3, "temporary_failure"},
		{"request timeout", &APIError{Problem: api.Problem{Status: 408, Code: "timeout", Title: "Timeout"}}, 3, "temporary_failure"},
		{"server", &APIError{Problem: api.Problem{Status: 503, Code: "unavailable", Title: "Unavailable"}}, 3, "temporary_failure"},
		{"network", &url.Error{Op: "Get", URL: "https://example.invalid", Err: &net.DNSError{Err: "no such host", Name: "example.invalid"}}, 3, "temporary_failure"},
		{"timeout", fmt.Errorf("request: %w", context.DeadlineExceeded), 3, "temporary_failure"},
		{"cancelled", fmt.Errorf("request: %w", context.Canceled), 130, "cancelled"},
		{"declined", &exitErr{msg: "cleanup declined", code: 130}, 130, "cancelled"},
	}
	for _, tc := range cases {
		for _, recovery := range []bool{false, true} {
			for _, machine := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/json=%t/recovery=%t", tc.name, machine, recovery), func(t *testing.T) {
					setupCLIRegression(t)
					jsonOutput = machine
					captured, restore := captureStderr(t)
					err := tc.err
					if recovery {
						err = &deployRecoveryError{err: err, recovery: &deployRecovery{Stage: "submission", App: "demo"}}
					}
					code := printErr("Operation failed", err)
					restore()
					output := captured.String()
					if code != tc.code {
						t.Fatalf("exit=%d want=%d stderr=%s", code, tc.code, output)
					}
					if machine {
						assertOneProblem(t, output)
						var metadata struct {
							Category string `json:"category"`
							ExitCode int    `json:"exit_code"`
							Code     string `json:"code"`
						}
						if err := json.Unmarshal([]byte(output), &metadata); err != nil {
							t.Fatal(err)
						}
						if metadata.Category != tc.category || metadata.ExitCode != code {
							t.Fatalf("metadata=%+v want category=%s exit=%d", metadata, tc.category, code)
						}
						var remote *APIError
						if errors.As(tc.err, &remote) && metadata.Code != remote.Problem.Code {
							t.Fatalf("server code changed: %s", metadata.Code)
						}
					} else if output == "" {
						t.Fatal("missing human diagnostic")
					}
				})
			}
		}
	}
}
