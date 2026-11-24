package mcphosting

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// httpResponseError retains the actual MCP HTTP status across CLI/SDK error
// handling. Remote bodies may contain credentials or HTML and are excluded,
// except for a JSON "error" field that is a plain code. On 401/403 the
// Bearer challenge's error and error_description (RFC 6750 §3) are kept:
// "authentication required" for every 401 hid whether the token was
// missing, wrong, or could not be checked by the server. Retry metadata does
// not enable automatic retries.
func httpResponseError(response *http.Response) error {
	problem := api.Problem{
		Type:   "about:blank",
		Title:  http.StatusText(response.StatusCode),
		Status: response.StatusCode,
		Code:   "http_error",
		Detail: fmt.Sprintf("MCP endpoint returned HTTP %d", response.StatusCode),
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		describeAuthRefusal(&problem, response)
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

const (
	mcpTokenHint = "Pass --token-env NAME, where NAME is an environment variable holding a token the MCP server accepts; gregale login does not sign MCP clients in."
	// signingKeysUnavailable is the error_description Gregale's MCP starters
	// send when they could not fetch their authorization server's JWKS.
	signingKeysUnavailable = "signing keys unavailable"
	// signingKeysRefused is the error_description they send when the JWKS URL
	// answered 401 or 403: it needs credentials the server does not have.
	signingKeysRefused = "signing keys refused"
)

func describeAuthRefusal(problem *api.Problem, response *http.Response) {
	params := bearerChallengeParams(response.Header.Values("WWW-Authenticate"))
	code := params["error"]
	if !authErrorCodePattern.MatchString(code) {
		code = authBodyErrorCode(response)
	}
	description := params["error_description"]
	if !authDescriptionPattern.MatchString(description) {
		description = ""
	}
	tokenSent := response.Request != nil && response.Request.Header.Get("Authorization") != ""
	if response.StatusCode == http.StatusUnauthorized && code == "" {
		code = "authentication_required"
	}
	if code == "" {
		return
	}
	problem.Code = code
	reason := code
	if description != "" {
		reason += ": " + description
	}
	switch {
	case strings.EqualFold(params["realm"], "gregale") || strings.EqualFold(params["realm"], "apps"):
		// Gregale's gateway refused the request before it reached the MCP
		// server: the app's access protection, not the MCP token's audience.
		problem.Detail = fmt.Sprintf("Gregale's gateway refused the request before it reached the MCP server (HTTP %d %s)", response.StatusCode, reason)
		problem.Hint = "The app is protected at Gregale's edge: send a credential its protection accepts, or configure MCP client ingress for the app so MCP clients reach the server."
	case response.StatusCode == http.StatusForbidden:
		problem.Detail = fmt.Sprintf("MCP server refused the request (HTTP 403 %s)", reason)
		if code == "insufficient_scope" {
			if scope := params["scope"]; authDescriptionPattern.MatchString(scope) {
				problem.Detail += "; required scope: " + scope
			}
			problem.Hint = "Use a token granted the required scopes."
		}
	case code == "authentication_required" && !tokenSent:
		problem.Detail = "MCP authentication required (HTTP 401 authentication_required); supply a client token with --token-env"
		// The CLI's generic 401 hint said to run `gregale login`, which
		// does not authenticate an MCP client (Hunt 32 H32-28).
		problem.Hint = mcpTokenHint
	case code == "authentication_required":
		// A token went out, yet the server saw no usable Bearer credential
		// (malformed, oversized, or stripped on the way).
		problem.Detail = fmt.Sprintf("MCP server did not accept the Authorization header as a Bearer token (HTTP 401 %s)", reason)
		problem.Hint = "A token was sent: check the variable named by --token-env holds a bare token (no \"Bearer \" prefix, no line breaks) of a size the server accepts."
	case strings.Contains(strings.ToLower(description), signingKeysRefused):
		problem.Detail = fmt.Sprintf("MCP server could not check the token: its JWKS URL refused the request (HTTP 401 %s)", reason)
		problem.Hint = "The token itself may be fine. The authorization server's JWKS URL answered 401 or 403: access protection or a firewall refused the app's request. If the authorization server runs on Gregale, open it to anonymous callers with gregale app update <authorization-server-app> --no-require-authn --public-auth open, and set access: public in its gregale.yaml so the next deploy keeps it open. Otherwise check that the provider lets the app's requests through. Then retry."
	case strings.Contains(strings.ToLower(description), signingKeysUnavailable):
		problem.Detail = fmt.Sprintf("MCP server could not check the token (HTTP 401 %s)", reason)
		problem.Hint = "The server could not fetch its authorization server's signing keys (JWKS); the token itself may be fine. Check that the JWKS URL is reachable from the app, then retry."
	case code == "invalid_token":
		problem.Detail = fmt.Sprintf("MCP server rejected the token (HTTP 401 %s)", reason)
		problem.Hint = "The token was sent but not accepted: check that it is unexpired, issued by the server's authorization server for this MCP endpoint (audience), and carries the required scopes."
	default:
		problem.Detail = fmt.Sprintf("MCP server refused the request (HTTP 401 %s)", reason)
		problem.Hint = mcpTokenHint
	}
}

var (
	// RFC 6750 §3: error is a short code; error_description and scope use
	// printable ASCII without '"' or '\'. Anything else (control bytes,
	// terminal escapes, oversized text) is dropped.
	authErrorCodePattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	authDescriptionPattern = regexp.MustCompile(`^[\x20\x21\x23-\x5b\x5d-\x7e]{1,200}$`)
)

// bearerChallengeParams returns the auth-params of the first Bearer
// challenge, unquoted. Malformed input yields what parsed before it.
func bearerChallengeParams(values []string) map[string]string {
	for _, value := range values {
		s := value
		for {
			// Each challenge is a scheme then its auth-params; the params of
			// another scheme are parsed (quoted strings included) to find the
			// next challenge, so "bearer" inside a quoted realm is no match.
			s = strings.TrimLeft(s, " ,")
			if s == "" {
				break
			}
			end := strings.IndexAny(s, " ,")
			if end < 0 {
				end = len(s)
			}
			scheme := s[:end]
			params := map[string]string{}
			s = parseAuthParams(s[end:], params)
			if strings.EqualFold(scheme, "bearer") {
				return params
			}
		}
	}
	return map[string]string{}
}

// parseAuthParams reads name=value pairs into params and returns the rest of
// s from the next challenge's scheme, or "" at the end or on malformed input.
func parseAuthParams(s string, params map[string]string) string {
	for {
		s = strings.TrimLeft(s, " ,")
		eq := strings.IndexByte(s, '=')
		if eq <= 0 {
			return ""
		}
		name := strings.ToLower(strings.TrimSpace(s[:eq]))
		if strings.ContainsAny(name, " \t\",") {
			// The next challenge ("Bearer error=..."): hand it back.
			return s
		}
		s = strings.TrimLeft(s[eq+1:], " ")
		var value string
		if strings.HasPrefix(s, `"`) {
			var b strings.Builder
			i := 1
			closed := false
			for ; i < len(s); i++ {
				switch s[i] {
				case '\\':
					if i+1 < len(s) {
						i++
						b.WriteByte(s[i])
					}
					continue
				case '"':
					closed = true
				}
				if closed {
					break
				}
				b.WriteByte(s[i])
			}
			if !closed {
				return ""
			}
			value, s = b.String(), s[i+1:]
		} else {
			end := strings.IndexAny(s, ", ")
			if end < 0 {
				end = len(s)
			}
			value, s = s[:end], s[end:]
		}
		if _, seen := params[name]; !seen {
			params[name] = value
		}
	}
}

// authBodyErrorCode reads a JSON body's "error" member when it is a plain
// code, for servers that omit error from the challenge (RFC 6750 does for a
// request without a token). Nothing else from the body is kept.
func authBodyErrorCode(response *http.Response) string {
	// Only a complete, small body: a streaming client has no timeout, and an
	// error body that never ends must not hang the error path.
	if response.Body == nil || response.ContentLength <= 0 || response.ContentLength > 4<<10 {
		return ""
	}
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<10))
	if err != nil {
		return ""
	}
	var body struct {
		Error json.RawMessage `json:"error"`
	}
	var code string
	if json.Unmarshal(data, &body) != nil || json.Unmarshal(body.Error, &code) != nil || !authErrorCodePattern.MatchString(code) {
		return ""
	}
	return code
}

// signingKeysUnavailableError reports a 401 the server marked as its own
// failure to fetch signing keys (with Retry-After or the starters'
// error_description), where sending the same token once more can succeed.
func signingKeysUnavailableError(err error) bool {
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Problem.Status != http.StatusUnauthorized || apiErr.Problem.Code != "invalid_token" {
		return false
	}
	return apiErr.Problem.RetryAfterSeconds != nil || strings.Contains(strings.ToLower(apiErr.Problem.Detail), signingKeysUnavailable)
}
