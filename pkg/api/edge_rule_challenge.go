package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/http/httpguts"
)

// ErrChallengeRequired answers a non-navigation request on a challenged
// route that carries no valid pass. It names the edge rule so the app's
// owner can find it; it says nothing a client could use to skip the work.
// ruleID is empty for a solution whose rule the gateway cannot tell.
func ErrChallengeRequired(ruleID string) *Problem {
	rule := "an edge rule of kind=challenge"
	if ruleID != "" {
		rule = "edge rule " + ruleID + ", kind=challenge"
	}
	return NewProblem(http.StatusForbidden, CodeChallengeRequired, "Challenge required",
		"this route asks browsers to load a page first ("+rule+"); API clients need a verified credential")
}

// EdgeRuleChallengeAction is the kind=challenge payload (ADR-831 amendment
// 7). A matched request without a valid pass is answered by the gateway: a
// browser navigation gets a page that solves a proof of work and earns a
// signed pass cookie; any other request gets 403 challenge_required.
// Requests with a verified API key, consumer key or JWT subject are never
// challenged. Rule-level log mode (ADR-960) is the dry run.
type EdgeRuleChallengeAction struct {
	// Difficulty is how many leading zero bits the browser must find in a
	// SHA-256 hash. Zero applies EdgeChallengeDefaultDifficulty.
	Difficulty int `json:"difficulty,omitempty"`
	// PassTTLSeconds is how long a solved challenge lets the client
	// through. Zero applies EdgeChallengeDefaultPassTTLSeconds.
	PassTTLSeconds int `json:"pass_ttl_seconds,omitempty"`
	// MaxDifficulty, when above Difficulty, lets the gateway raise the
	// difficulty up to it while the rule serves far more challenge pages
	// than usual (ADR-831 amendment 8). Zero or equal to Difficulty keeps
	// the difficulty fixed.
	MaxDifficulty int `json:"max_difficulty,omitempty"`
}

// Validate applies the defaults and bounds, mutating the receiver so the
// stored row carries effective values.
func (a *EdgeRuleChallengeAction) Validate() *Problem {
	if a == nil {
		return ErrValidation("challenge action is required")
	}
	if a.Difficulty == 0 {
		a.Difficulty = EdgeChallengeDefaultDifficulty
	}
	if a.Difficulty < MinEdgeChallengeDifficulty || a.Difficulty > MaxEdgeChallengeDifficulty {
		return ErrValidation(fmt.Sprintf(
			"challenge action: difficulty must be in %d..%d leading zero bits (got %d)",
			MinEdgeChallengeDifficulty, MaxEdgeChallengeDifficulty, a.Difficulty))
	}
	if a.MaxDifficulty != 0 && (a.MaxDifficulty < a.Difficulty || a.MaxDifficulty > MaxEdgeChallengeDifficulty) {
		return ErrValidation(fmt.Sprintf(
			"challenge action: max_difficulty must be in %d..%d, from the difficulty up (got %d)",
			a.Difficulty, MaxEdgeChallengeDifficulty, a.MaxDifficulty))
	}
	if a.PassTTLSeconds == 0 {
		a.PassTTLSeconds = EdgeChallengeDefaultPassTTLSeconds
	}
	if a.PassTTLSeconds < MinEdgeChallengePassTTLSeconds || a.PassTTLSeconds > MaxEdgeChallengePassTTLSeconds {
		return ErrValidation(fmt.Sprintf(
			"challenge action: pass_ttl_seconds must be in %d..%d (got %d)",
			MinEdgeChallengePassTTLSeconds, MaxEdgeChallengePassTTLSeconds, a.PassTTLSeconds))
	}
	return nil
}

// EdgeChallengeNavigation reports whether a request is a browser navigation,
// which gets the challenge page; any other request on a challenged route
// gets 403 challenge_required (ADR-831 amendment 7). Sec-Fetch-Mode decides
// when present; otherwise an Accept naming text/html does.
func EdgeChallengeNavigation(method string, header http.Header) bool {
	if method != http.MethodGet && method != http.MethodHead {
		return false
	}
	if mode := header.Get("Sec-Fetch-Mode"); mode != "" {
		return strings.EqualFold(mode, "navigate")
	}
	return strings.Contains(strings.ToLower(header.Get("Accept")), "text/html")
}

// Bounds on what a challenged preflight answer echoes.
const (
	edgeChallengePreflightMaxOrigin  = 512
	edgeChallengePreflightMaxHeaders = 32
)

// EdgeChallengePreflightHeaders answers a CORS preflight on a challenged
// route (ADR-831 amendment 18). A preflight carries no cookies and no
// Authorization, so it can never hold a pass or a verified credential, and
// refusing it fails every cross-origin call that needs one, even from a
// browser that holds a pass or a JWT. The answer allows the requested method
// and headers from the requesting origin, without credentials: the browser
// then sends the request itself without cookies, the challenge still applies
// to it, and the app's own response still decides whether the page may read
// it. It reports false for a request that is not a well-formed preflight.
func EdgeChallengePreflightHeaders(method string, header http.Header) (http.Header, bool) {
	if method != http.MethodOptions {
		return nil, false
	}
	origin := header.Get("Origin")
	requested := strings.TrimSpace(header.Get("Access-Control-Request-Method"))
	if origin == "" || len(origin) > edgeChallengePreflightMaxOrigin || !httpguts.ValidHeaderFieldName(requested) {
		return nil, false
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, false
	}
	var names []string
	for _, name := range strings.Split(header.Get("Access-Control-Request-Headers"), ",") {
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		if !httpguts.ValidHeaderFieldName(name) || len(names) == edgeChallengePreflightMaxHeaders {
			return nil, false
		}
		names = append(names, name)
	}
	out := http.Header{}
	out.Set("Access-Control-Allow-Origin", origin)
	out.Set("Access-Control-Allow-Methods", requested)
	if len(names) > 0 {
		out.Set("Access-Control-Allow-Headers", strings.Join(names, ", "))
	}
	out.Set("Vary", "Origin, Access-Control-Request-Method, Access-Control-Request-Headers")
	out.Set("Cache-Control", "no-store")
	return out, true
}
