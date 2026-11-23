package mcphosting

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// httpResponseError retains the actual MCP HTTP status across CLI/SDK error
// handling. Remote bodies/challenges may contain credentials or HTML and are
// deliberately excluded. Retry metadata does not enable automatic retries.
func httpResponseError(response *http.Response) error {
	problem := api.Problem{
		Type:   "about:blank",
		Title:  http.StatusText(response.StatusCode),
		Status: response.StatusCode,
		Code:   "http_error",
		Detail: fmt.Sprintf("MCP endpoint returned HTTP %d", response.StatusCode),
	}
	if response.StatusCode == http.StatusUnauthorized {
		problem.Detail = "MCP authentication required (HTTP 401); supply a client token with --token-env"
		// The CLI's generic 401 hint said to run `gregale login`, which
		// does not authenticate an MCP client (Hunt 32 H32-28).
		problem.Hint = "Pass --token-env NAME, where NAME is an environment variable holding a token the MCP server accepts; gregale login does not sign MCP clients in."
		if tokenSent(response) {
			// Telling "no token" apart from "token rejected" matters: the
			// old text sent operators hunting for a missing --token-env when
			// the server had in fact failed to fetch its signing keys.
			problem.Code = "mcp_token_rejected"
			reason := bearerChallengeError(response.Header.Values("WWW-Authenticate"))
			problem.Detail = "the MCP server rejected the client token (HTTP 401"
			if reason != "" {
				problem.Detail += ", " + reason
			}
			problem.Detail += ")"
			problem.Hint = "The token reached the server and failed verification: check its issuer, audience, scopes and expiry, and whether the server can fetch its signing keys (JWKS). The server's logs say which."
		}
	}
	retry := strings.TrimSpace(response.Header.Get("Retry-After"))
	// Only bounded, valid delta-seconds or HTTP dates are useful retry metadata.
	if len(retry) <= 128 {
		if seconds, err := strconv.ParseInt(retry, 10, 64); err == nil && seconds >= 0 {
			problem.RetryAfterSeconds = &seconds
			problem = *problem.WithHeader("Retry-After", retry)
		} else if _, err := http.ParseTime(retry); err == nil {
			problem = *problem.WithHeader("Retry-After", retry)
		}
	}
	return &api.APIError{Problem: problem}
}

func tokenSent(response *http.Response) bool {
	return response.Request != nil && response.Request.Header.Get("Authorization") != ""
}

// bearerChallengeError returns the RFC 6750 error code from a Bearer
// challenge. Only the three registered codes are returned; any other value is
// server-controlled text and stays excluded.
func bearerChallengeError(challenges []string) string {
	for _, challenge := range challenges {
		scheme, params, _ := strings.Cut(strings.TrimSpace(challenge), " ")
		if !strings.EqualFold(scheme, "Bearer") {
			continue
		}
		for _, param := range strings.Split(params, ",") {
			name, value, ok := strings.Cut(strings.TrimSpace(param), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(name), "error") {
				continue
			}
			switch code := strings.Trim(strings.TrimSpace(value), `"`); code {
			case "invalid_token", "invalid_request", "insufficient_scope":
				return code
			}
		}
	}
	return ""
}

// tokenRejectionRetryDelay outlasts the starters' 1 s JWKS failure backoff.
var tokenRejectionRetryDelay = 1500 * time.Millisecond

// retryableTokenRejection reports a 401 invalid_token answered to a request
// that carried a token. Starters back off only briefly after a failed JWKS
// fetch, so one short retry separates a transient key fetch from a token the
// server will never accept.
func retryableTokenRejection(response *http.Response) bool {
	return response.StatusCode == http.StatusUnauthorized && tokenSent(response) &&
		bearerChallengeError(response.Header.Values("WWW-Authenticate")) == "invalid_token"
}
