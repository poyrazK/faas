// Package redact strips PII / secrets from strings before they are
// persisted to the customer-facing automatic error grouping store
// (ADR-096). The closed regex set here is the single source of truth:
// every value persisted via pkg/state.IncrementAppError (sample
// message, headers_sample) MUST pass through Redactor.Apply first.
//
// Background: error grouping ships error message excerpts + a
// sampled header map to the customer dashboard. Any PII leaking into
// those excerpts is a Sentry-grade incident. The regex set is kept
// deliberately narrow and explicit — each entry names the attack
// vector it closes. New entries are a CODE CHANGE, not a config
// flip.
//
// Threading: Redactor is safe for concurrent use. The compiled
// patterns are immutable after construction and shared across
// goroutines; Apply takes the input by value and returns new
// strings.
//
// Cap behaviour: Apply truncates the input to Cap bytes before
// matching, then appends the truncation marker ("...") at the end.
// This is load-bearing: a 64 KiB stack-trace blob must not blow
// memory or let a regex run over arbitrarily long input. The cap
// is per-call, configured at construction; sample_message uses 512
// (limits.AppErrorsSampleMessageCapBytes), header values use 256.
package redact

import (
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/safetext"
)

// Pattern is a closed-vocabulary redaction rule. The Name is what
// shows up in the substituted marker (e.g. "[REDACTED:email]") and
// is also returned in the applied list so the caller can render
// "we redacted email / card / cookie".
type Pattern struct {
	Name  string
	Regex *regexp.Regexp
	// Replacer is optional. If set, it receives the full match
	// string + submatches and returns the replacement.
	// Replacer runs INSIDE ReplaceAllStringFunc, so it must be
	// fast and side-effect free. If nil, the whole match is
	// replaced with "[REDACTED:<name>]".
	Replacer func(groups []string) string
}

// Redactor is a thread-safe bundle of compiled PII patterns + a
// per-call input cap. Construct one per process (constructor is
// cheap relative to regex compilation) and share across goroutines.
type Redactor struct {
	patterns []Pattern
	cap      int
}

// New builds a Redactor with the standard ADR-096 regex set. Cap
// is the per-call byte limit; inputs longer than cap are truncated
// to cap bytes + "..." before matching. Cap <= 0 falls back to 4096
// so a misconfigured caller cannot DoS the regex engine.
func New(cap int) *Redactor {
	if cap <= 0 {
		cap = 4096
	}
	return &Redactor{
		patterns: defaultPatterns(),
		cap:      cap,
	}
}

// Default returns the canonical ADR-096 pattern set. Exposed so
// the wire-side grep tripwire (handlers_app_errors_security_test.go)
// can re-check the output against the same regexes the writer used.
func Default() []Pattern { return defaultPatterns() }

// Apply redacts every PII / secret match in s, truncating s to
// the configured cap first. Returns the redacted string and the
// sorted unique list of pattern names that matched (so the caller
// can render "we redacted X / Y / Z").
//
// Empty input returns ("", nil). Order of returned pattern names
// is alphabetical so callers get a deterministic surface.
//
// When no pattern matched AND no truncation occurred, the returned
// names slice is nil (not an empty slice). This matches the
// "nothing happened" contract callers depend on.
func (r *Redactor) Apply(s string) (string, []string) {
	return r.apply(s, "")
}

// apply is Apply with an optional line prefix ("Name: ") that is matched
// together with s but is not counted against the cap and is stripped from
// the result. ApplyHeaders uses it so the header-style rules see the
// header name.
func (r *Redactor) apply(s, prefix string) (string, []string) {
	if s == "" {
		return "", nil
	}
	orig := s
	// Truncate first. Doing this BEFORE regex matching is
	// load-bearing: long inputs (64 KiB stack traces) would
	// otherwise blow memory and let the regex engine see
	// arbitrary-length input. The cut never splits a rune: the
	// result is persisted to a Postgres text column, which rejects
	// invalid UTF-8.
	truncated := false
	if r.cap > 0 && len(s) > r.cap {
		s = safetext.Truncate(s, r.cap) + "..."
		truncated = true
	}
	s = prefix + s

	applied := map[string]struct{}{}
	for _, p := range r.patterns {
		s = p.Regex.ReplaceAllStringFunc(s, func(m string) string {
			applied[p.Name] = struct{}{}
			if p.Replacer == nil {
				return "[REDACTED:" + p.Name + "]"
			}
			// Pull submatches so Replacer can preserve the
			// header name (case) etc. FindStringSubmatchIndex
			// is fine; we only need the string values.
			return p.Replacer(p.Regex.FindStringSubmatch(m))
		})
	}
	if prefix != "" {
		if !strings.HasPrefix(s, prefix) {
			// A rule rewrote the header name itself; match the
			// value alone rather than guess where it starts.
			return r.apply(orig, "")
		}
		s = s[len(prefix):]
	}

	names := make([]string, 0, len(applied))
	for n := range applied {
		names = append(names, n)
	}
	// Stable, alphabetical order so JSON output is deterministic.
	sortStrings(names)

	if truncated {
		// Surface truncation so callers can render a hint.
		// Dedupe against any pre-existing entry.
		for _, n := range names {
			if n == "truncated" {
				return s, names
			}
		}
		names = append(names, "truncated")
	}
	if len(names) == 0 {
		return s, nil
	}
	return s, names
}

// ApplyHeaders redacts every VALUE in a flat header map. The
// header-style rules (authorization, cookie, x-api-key) match
// "Name: value" lines, so each value is matched with its name in
// front: a credential header is redacted by name whatever its value
// looks like, and the other rules still catch bearer tokens that
// landed in custom headers. Only the value is returned.
//
// Returns a NEW map; the input is not mutated.
func (r *Redactor) ApplyHeaders(h map[string]string) (map[string]string, []string) {
	if len(h) == 0 {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(h))
	seen := map[string]struct{}{}
	for k, v := range h {
		redacted, names := r.apply(v, k+": ")
		out[k] = redacted
		for _, n := range names {
			seen[n] = struct{}{}
		}
	}
	all := make([]string, 0, len(seen))
	for n := range seen {
		all = append(all, n)
	}
	sortStrings(all)
	if len(all) == 0 {
		return out, nil
	}
	return out, all
}

// sortStrings is an inlined, allocation-light ascending sort.
// Strings are pattern names (≤16 chars, ≤16 entries), so this
// is the right complexity. Kept private so callers can never
// depend on the implementation.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// defaultPatterns returns the canonical ADR-096 regex set. Order
// matters only for human readability of the test fixtures — every
// pattern is applied to the full input on every pass.
//
// Each header-style rule captures TWO groups: (1) the original
// header NAME (with whatever case the caller used) and the colon
// + whitespace, (2) the value. The Replacer rebuilds the line
// preserving the caller's case.
func defaultPatterns() []Pattern {
	return []Pattern{
		// Email — RFC 5322-lite. Anchored on word boundaries so
		// we don't redact "user@vm1" inside a host:port string.
		// Two-char TLD minimum keeps us from eating "x.y" pairs
		// inside JSON dotted paths (the dotted-path negative
		// case is load-bearing — false positives here would
		// shred every JSON-shaped error message). The
		// `([A-Za-z0-9\-]+\.)+` form permits arbitrary-depth
		// subdomains (e.g. `alice@sub.example.co.uk`).
		{
			Name:  "email",
			Regex: regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@(?:[A-Za-z0-9\-]+\.)+[A-Za-z]{2,}\b`),
		},

		// Card-number-ish — exactly 13..19 digits with optional
		// single-space-or-hyphen separators BETWEEN digits but
		// NOT after the last digit (a trailing optional
		// separator was eating the trailing space and producing
		// "[REDACTED:card]ok" instead of "[REDACTED:card] ok").
		// We deliberately do NOT run Luhn here: the cost
		// (table-driven Luhn over the full input) is
		// disproportionate to the benefit (a 16-digit string
		// already screams "card"). False positives on UUIDs
		// are impossible because UUIDs include hex letters
		// that break the digit run.
		{
			Name:  "card",
			Regex: regexp.MustCompile(`\b\d(?:[ \-]?\d){12,18}\b`),
		},

		// Authorization header — capture the header NAME
		// (whatever case the caller used) plus the value so
		// the Replacer can rebuild the line preserving the
		// original case. Anchored on line endings (CR/LF/NUL)
		// so we don't bleed into the next header.
		{
			Name:  "authorization",
			Regex: regexp.MustCompile(`(?i)(Authorization)(\s*:\s*)([^\r\n\000]+)`),
			Replacer: func(g []string) string {
				if len(g) < 4 {
					return "[REDACTED:authorization]"
				}
				return g[1] + g[2] + "[REDACTED:authorization]"
			},
		},

		// Cookie header — same shape as Authorization.
		{
			Name:  "cookie",
			Regex: regexp.MustCompile(`(?i)(Cookie)(\s*:\s*)([^\r\n\000]+)`),
			Replacer: func(g []string) string {
				if len(g) < 4 {
					return "[REDACTED:cookie]"
				}
				return g[1] + g[2] + "[REDACTED:cookie]"
			},
		},

		// X-API-Key / X-Api-Token — common custom auth headers
		// in customer app stacks.
		{
			Name:  "x-api-key",
			Regex: regexp.MustCompile(`(?i)(X-(?:API-Key|Api-Token))(\s*:\s*)([^\r\n\000]+)`),
			Replacer: func(g []string) string {
				if len(g) < 4 {
					return "[REDACTED:x-api-key]"
				}
				return g[1] + g[2] + "[REDACTED:x-api-key]"
			},
		},

		// Query-string secrets — match the key=value pair as a
		// unit (key + "=" + value) so the Replacer can rebuild
		// "<key>=[REDACTED:query_secret]" without any
		// backref gymnastics (which ReplaceAllStringFunc
		// doesn't honour anyway). Common in URL-only error
		// messages ("GET /v1?api_key=sk_live_...").
		{
			Name:  "query_secret",
			Regex: regexp.MustCompile(`(?i)([?&](?:api_key|token|secret|password|access_token)=)([^&\s]+)`),
			Replacer: func(g []string) string {
				if len(g) < 3 {
					return "[REDACTED:query_secret]"
				}
				return g[1] + "[REDACTED:query_secret]"
			},
		},

		// Stripe-shaped keys — sk_live_, pk_live_, sk_test_,
		// pk_test_, rk_live_. 16+ alnum after the prefix. These
		// leak via stack-trace logs far more often than people
		// expect.
		{
			Name:  "stripe_key",
			Regex: regexp.MustCompile(`\b(?:sk|pk|rk)_(?:live|test)_[A-Za-z0-9]{16,}\b`),
		},

		// JWT-shaped — three base64url chunks separated by dots,
		// each starting with `eyJ` (the base64url of `{"`).
		// Anchored on word boundaries so we don't redact the
		// substring inside a longer identifier.
		{
			Name:  "jwt",
			Regex: regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+\b`),
		},

		// IPv4 in dotted-quad form. We keep it in the redaction
		// set so customer IPs leaked via 5xx error messages
		// ("connect 192.168.1.42:5432: refused") are scrubbed.
		// 0..255 octets enforced.
		{
			Name:  "ipv4",
			Regex: regexp.MustCompile(`\b(?:25[0-5]|2[0-4]\d|[01]?\d?\d)(?:\.(?:25[0-5]|2[0-4]\d|[01]?\d?\d)){3}\b`),
		},
	}
}

// internal helpers — keep allocation churn low.
var _ = strings.HasPrefix // strings is reserved for future header-name normalisation
