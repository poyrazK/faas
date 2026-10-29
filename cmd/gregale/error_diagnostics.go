package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// loginErrorCredential also covers an explicit token that has not been saved yet.
// CLI dispatch is sequential; cmdLogin restores this value on every return path.
var loginErrorCredential string

var errorTokenPattern = regexp.MustCompile(`(?i)\bfp_(?:live|test)_[a-z0-9_-]+|\bBearer[ \t]+[a-z0-9._~+/-]+=*`)

func redactErrorText(text string) string {
	return errorTextRedactor()(text)
}

func errorTextRedactor() func(string) string {
	// Formatting an error must not emit credential-store diagnostics of its own.
	secrets := []string{loginErrorCredential, strings.TrimSpace(os.Getenv("FAAS_TOKEN")), loadTokenWithDiagnostics(io.Discard)}
	if endpoint, err := url.Parse(os.Getenv("FAAS_API")); err == nil && endpoint.User != nil {
		if password, ok := endpoint.User.Password(); ok && password != "" {
			secrets = append(secrets, password, url.QueryEscape(password), url.PathEscape(password))
		}
	}
	return func(text string) string {
		for _, secret := range secrets {
			if secret != "" {
				text = strings.ReplaceAll(text, secret, "[REDACTED]")
			}
		}
		return errorTokenPattern.ReplaceAllString(text, "[REDACTED]")
	}
}

func authenticationHint() string {
	command := "gregale"
	if profile := currentProfile(); profile != "default" {
		command += " --profile " + profile
	}
	if loginErrorCredential != "" {
		return "Provide a valid credential with " + command + " login --token-stdin."
	}
	if os.Getenv("FAAS_TOKEN") != "" {
		return "Replace or unset FAAS_TOKEN; it overrides the selected profile's stored credential."
	}
	return "Run " + command + " login, then retry."
}

// Supply fallbacks without replacing server-specific remediation or codes.
func diagnosticProblem(p api.Problem) api.Problem {
	if p.Title == "" {
		p.Title = "Request failed"
	}
	if p.Hint == "" {
		switch {
		case p.Status == 401:
			p.Hint = authenticationHint()
		case p.Status == 403 && authorizationRefusal(p.Code):
			p.Hint = "Check the selected profile, account membership, and credential permissions with gregale config profile check."
		case p.Status == 429:
			p.Hint = "Wait for Retry-After when provided, then retry."
		case p.Code == "long_poll_timeout":
			// The server stopped waiting, not working: a sync invoke past its
			// wait keeps running, so a retry would run it twice.
			p.Hint = "The work keeps running on Gregale; follow it with the command in the detail instead of retrying."
		case p.Status == 501 || p.Status >= 500 && strings.HasSuffix(p.Code, "_not_enabled"):
			// Operator-gated features answer 501 (disposable executions, app
			// tasks) or 503 with a *_not_enabled code (tenant surfaces, private
			// networks); retrying or checking service status cannot help.
			p.Hint = "This feature is not enabled on this Gregale installation; ask its operator to enable it."
		case p.Status >= 500:
			p.Hint = "Check Gregale's service status and run gregale config profile check. Inspect deployment status before retrying a deploy."
		}
	} else if p.Status == 401 && loginErrorCredential == "" && os.Getenv("FAAS_TOKEN") != "" && !strings.Contains(p.Hint, "FAAS_TOKEN") {
		p.Hint += " " + authenticationHint()
	}
	return redactProblem(p)
}

// Walk all string fields, including nested validation/binding findings. The
// round trip copies the report, leaving the SDK's original Problem untouched.
func redactProblem(p api.Problem) api.Problem {
	data, err := json.Marshal(p)
	if err != nil {
		return api.Problem{Status: p.Status, Code: p.Code, Title: "Request failed"}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return api.Problem{Status: p.Status, Code: p.Code, Title: "Request failed"}
	}
	redact := errorTextRedactor()
	var walk func(any) any
	walk = func(v any) any {
		switch node := v.(type) {
		case string:
			return redact(node)
		case map[string]any:
			for key, child := range node {
				node[key] = walk(child)
			}
		case []any:
			for i, child := range node {
				node[i] = walk(child)
			}
		}
		return v
	}
	data, err = json.Marshal(walk(value))
	if err != nil {
		return api.Problem{Status: p.Status, Code: p.Code, Title: "Request failed"}
	}
	var safe api.Problem
	if json.Unmarshal(data, &safe) != nil {
		return api.Problem{Status: p.Status, Code: p.Code, Title: "Request failed"}
	}
	return safe
}

// authorizationRefusal reports whether a 403 is about who is asking. Plan
// limits and policy refusals also answer 403 and explain themselves; the
// credential hint sent an alert rule's "Egress denied" (a private webhook
// address) to gregale profile check (H10-73).
func authorizationRefusal(code string) bool {
	switch code {
	case "", "forbidden", api.CodeForbidden, api.CodeOrgRoleForbidden:
		return true
	}
	return false
}
