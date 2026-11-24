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
	"github.com/onebox-faas/faas/pkg/wire"
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
	secrets := []string{loginErrorCredential, strings.TrimSpace(os.Getenv(tokenEnvVar)), strings.TrimSpace(os.Getenv(legacyTokenEnvVar)), loadTokenWithDiagnostics(io.Discard)}
	for _, name := range []string{apiEnvVar, legacyAPIEnvVar} {
		if endpoint, err := url.Parse(os.Getenv(name)); err == nil && endpoint.User != nil {
			if password, ok := endpoint.User.Password(); ok && password != "" {
				secrets = append(secrets, password, url.QueryEscape(password), url.PathEscape(password))
			}
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
	if name := envTokenName(); name != "" {
		return "Replace or unset " + name + "; it overrides the selected profile's stored credential."
	}
	return "Run " + command + " login, then retry."
}

// missingRouteHint explains apid's answer for a route it does not offer
// (pkg/apid/httpkit.NoSuchRoute). Without it the error read exactly like a
// missing app or a typo, though the usual cause is a CLI release newer or
// older than the server, or a feature not released to the account
// (ADR-20261023115741).
func missingRouteHint() string {
	return "This gregale (" + wire.Version + ") may be newer or older than the server, or the feature is not enabled for your account. Check the CLI release with gregale version; gregale update installs the current one."
}

// Supply fallbacks without replacing server-specific remediation or codes.
func diagnosticProblem(p api.Problem) api.Problem {
	if p.Title == "" {
		p.Title = "Request failed"
	}
	if p.Hint == "" {
		switch {
		case p.Status == 404 && p.Detail == noSuchRouteDetail:
			p.Hint = missingRouteHint()
		case p.Status == 401:
			p.Hint = authenticationHint()
		case p.Status == 403 && authorizationRefusal(p.Code):
			p.Hint = "Check the selected profile, account membership, and credential permissions with gregale config profile check."
		case p.Status == 429:
			p.Hint = "Wait for Retry-After when provided, then retry."
		case api.EdgeRuleChangeConverging(p):
			// The 503 came after the write: resending would repeat a change
			// that already happened (#2683).
			p.Hint = "The change is saved; some serving gateways have not applied it yet and will within seconds. Don't resend it: check the current rules with gregale edge rule list."
		case p.Code == "long_poll_timeout":
			// The server stopped waiting, not working: a sync invoke past its
			// wait keeps running, so a retry would run it twice.
			p.Hint = "The work keeps running on Gregale; follow it with the command in the detail instead of retrying."
		case p.Status == 501 && p.Title == api.InteractiveSessionsUnavailableTitle:
			// app-tasks is listed as available while interactive sessions are
			// off, so "feature list shows what is available" sent agents in a
			// circle (agent eval 2026-11-23); the detail names the way round.
			p.Hint = "Interactive sessions are off on this installation even where app-tasks is listed; use the alternative in the detail, or contact support."
		case p.Status == 501 || p.Status >= 500 && strings.HasSuffix(p.Code, "_not_enabled"):
			// Operator-gated features answer 501 (disposable executions, app
			// tasks) or 503 with a *_not_enabled code (tenant surfaces, private
			// networks); retrying or checking service status cannot help.
			// A hosted user has no operator to ask: on prod `app exec` told
			// them to (hunt 2026-11-21) while `gregale capabilities` said to
			// contact support.
			p.Hint = "This feature is not enabled on this Gregale installation: contact support (or, on your own installation, its operator). gregale feature list shows what is available."
		case p.Status == 503 && p.RetryAfterSeconds != nil && *p.RetryAfterSeconds > 0:
			// The server said when to come back: the resource is busy, not
			// Gregale down. A bucket finishing an inventory read "check service
			// status … retrying a deploy" (prod 2026-11-19).
			p.Hint = "The resource is busy for a moment; wait for Retry-After, then run the same command again."
		case p.Status >= 500:
			p.Hint = "Check Gregale's service status and run gregale config profile check. Inspect deployment status before retrying a deploy."
		}
	} else if p.Status == 401 && loginErrorCredential == "" && envTokenName() != "" && !strings.Contains(p.Hint, envTokenName()) {
		p.Hint += " " + authenticationHint()
	}
	return redactProblem(p)
}

// Walk all string fields, including nested validation/binding findings. The
// round trip copies the report, leaving the SDK's original Problem untouched.
func redactProblem(p api.Problem) (safe api.Problem) {
	redact := errorTextRedactor()
	id := problemRequestID(p)
	if id != "" && redact(id) != id {
		id = ""
	}
	// The JSON copy intentionally drops private client metadata. Preserve
	// only the validated, non-secret correlation ID, including on fallback.
	defer func() {
		if id != "" {
			safe = *safe.WithRequestID(id)
		}
	}()
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
	if json.Unmarshal(data, &safe) != nil {
		return api.Problem{Status: p.Status, Code: p.Code, Title: "Request failed"}
	}
	return safe
}

func problemRequestID(p api.Problem) string {
	return p.RequestID()
}

// JSON writers can also receive locally assembled problems without passing
// through diagnosticProblem. Validate and apply credential redaction here too.
func safeProblemRequestID(p api.Problem) string {
	id := problemRequestID(p)
	if id != "" && redactErrorText(id) != id {
		return ""
	}
	return id
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
