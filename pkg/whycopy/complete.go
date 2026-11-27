// adr: 20261123230000

package whycopy

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// A fix that says `gregale logs <slug>` when the app is known sends an agent
// back to look up a value it already gave (2026-11-23 agent eval: the
// voting-app worker's startup crash). Complete fills the values a failure
// knows, lists the commands that are now runnable, and says whether the
// same request can simply be retried.

// Values are a failure's known values for the placeholders in fix text.
// Empty fields leave their placeholders as they are.
type Values struct {
	// App is the app slug: <slug>, <app-slug> and <app>.
	App string
	// Deployment is the deployment ID: <deployment-id>, and <id>, which
	// every catalog command uses for a deployment (deploys status, logs
	// --deployment, release logs).
	Deployment string
}

var (
	appPlaceholders        = []string{"<slug>", "<app-slug>", "<app>"}
	deploymentPlaceholders = []string{"<deployment-id>", "<id>"}
	quotedCommand          = regexp.MustCompile("`(gregale [^`]+)`")
	// A placeholder left in a command: <name>, an upper-case word (TEXT,
	// ENV_VAR_NAME, MB), an ellipsis or an unformatted verb.
	placeholder = regexp.MustCompile(`<[A-Za-z][A-Za-z0-9_-]*>|\b[A-Z][A-Z0-9_]{1,}\b|\.\.\.|%[a-z]`)
)

// Fill replaces the placeholders whose value v knows.
func Fill(text string, v Values) string {
	if text == "" {
		return text
	}
	replace := func(text string, keys []string, value string) string {
		if value == "" {
			return text
		}
		for _, key := range keys {
			text = strings.ReplaceAll(text, key, value)
		}
		return text
	}
	text = replace(text, appPlaceholders, v.App)
	return replace(text, deploymentPlaceholders, v.Deployment)
}

// Commands returns the gregale commands quoted in fix text, in order and
// without repeats, leaving out any that still holds a placeholder: a
// command an agent cannot run as written is not a next step.
func Commands(fix string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range quotedCommand.FindAllStringSubmatch(fix, -1) {
		command := strings.TrimSpace(m[1])
		if placeholder.MatchString(command) || seen[command] {
			continue
		}
		seen[command] = true
		out = append(out, command)
	}
	return out
}

// Retryable reports whether repeating the same request can succeed without
// a change: a rate limit, a Retry-After, or a transient platform status.
// nil when the status says nothing either way.
func Retryable(p *api.Problem) *bool {
	if p == nil {
		return nil
	}
	yes, no := true, false
	if p.RetryAfterSeconds != nil {
		return &yes
	}
	switch {
	case p.Status == http.StatusRequestTimeout, p.Status == http.StatusTooEarly,
		p.Status == http.StatusTooManyRequests, p.Status == http.StatusBadGateway,
		p.Status == http.StatusServiceUnavailable, p.Status == http.StatusGatewayTimeout:
		return &yes
	case p.Status == http.StatusConflict, p.Status == http.StatusLocked:
		// A conflict or lock may clear on its own (another deploy finishing)
		// or need a change; without Retry-After the caller cannot tell.
		return nil
	case p.Status >= 400 && p.Status < 500:
		return &no
	}
	return nil
}

// Complete fills p's fix with v, sets FixCommands from it and, when unset,
// Retryable. Safe to call again with more values: a filled placeholder is
// gone, and the commands are recomputed. Commands a server already sent
// stay unless filling changed the fix: the CLI completes every problem it
// prints, and must not drop what an API sent.
func Complete(p *api.Problem, v Values) *api.Problem {
	if p == nil {
		return nil
	}
	fix := Fill(p.Fix, v)
	if len(p.FixCommands) == 0 || fix != p.Fix {
		p.FixCommands = Commands(fix)
	}
	p.Fix = fix
	p.Hint = Fill(p.Hint, v)
	if p.Retryable == nil {
		p.Retryable = Retryable(p)
	}
	return p
}
