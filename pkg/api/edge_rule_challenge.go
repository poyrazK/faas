package api

import (
	"fmt"
	"net/http"
	"strings"
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
