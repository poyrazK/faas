package api

import (
	"fmt"
	"net/http"
	"strconv"
)

// EdgeRulePenalty is a rule's penalty box (ADR-20261022105846): once the
// rule has rejected the same client After times within WithinSeconds, every
// request from that client to the app is refused for Seconds, whichever
// rule or path it targets. The client is its IP address (an IPv6 client is
// its /64). Boxes are kept in memory by each gateway node.
type EdgeRulePenalty struct {
	// Seconds is how long a boxed client is refused.
	Seconds int `json:"seconds"`
	// After is how many rejections by this rule box the client. Zero
	// applies EdgePenaltyDefaultAfter (the first rejection).
	After int `json:"after,omitempty"`
	// WithinSeconds is the window After counts in. Zero applies
	// EdgePenaltyDefaultWithinSeconds.
	WithinSeconds int `json:"within_seconds,omitempty"`
}

// Validate applies the defaults and bounds, mutating the receiver.
func (p *EdgeRulePenalty) Validate() *Problem {
	if p.After == 0 {
		p.After = EdgePenaltyDefaultAfter
	}
	if p.WithinSeconds == 0 {
		p.WithinSeconds = EdgePenaltyDefaultWithinSeconds
	}
	switch {
	case p.Seconds < MinEdgePenaltySeconds || p.Seconds > MaxEdgePenaltySeconds:
		return ErrValidation(fmt.Sprintf("penalty.seconds must be in %d..%d (got %d)", MinEdgePenaltySeconds, MaxEdgePenaltySeconds, p.Seconds))
	case p.After < 1 || p.After > MaxEdgePenaltyAfter:
		return ErrValidation(fmt.Sprintf("penalty.after must be in 1..%d (got %d)", MaxEdgePenaltyAfter, p.After))
	case p.WithinSeconds < 1 || p.WithinSeconds > MaxEdgePenaltyWithinSeconds:
		return ErrValidation(fmt.Sprintf("penalty.within_seconds must be in 1..%d (got %d)", MaxEdgePenaltyWithinSeconds, p.WithinSeconds))
	}
	return nil
}

// ValidateEdgeRulePenaltyKind checks that a rule of kind can carry a
// penalty: only rules that refuse a client by policy can box it (ip, geo,
// throttle, and waf in block mode). A challenge would box browsers that
// have not solved yet, and the other kinds do not refuse clients.
// wafMode is the waf rule's mode ("" for other kinds).
func ValidateEdgeRulePenaltyKind(kind, wafMode string) *Problem {
	switch kind {
	case "ip", "geo", "throttle":
		return nil
	case "waf":
		if wafMode == EdgeWAFModeBlock {
			return nil
		}
		return ErrValidation("penalty: a waf rule boxes clients only in block mode (got mode " + strconv.Quote(wafMode) + ")")
	}
	return ErrValidation("penalty: kind=" + kind + " cannot box clients; use it on ip, geo, throttle or waf (block) rules")
}

// ErrPenaltyBox answers a request from a client a rule's penalty box holds.
// It names the rule so the app's owner can find it, and says when the box
// ends; Retry-After carries the same.
func ErrPenaltyBox(ruleID string, retryAfterSeconds int) *Problem {
	return NewProblem(http.StatusForbidden, CodePenaltyBox, "Temporarily blocked",
		fmt.Sprintf("this client was refused repeatedly by edge rule %s and is blocked from this app for %d more seconds", ruleID, retryAfterSeconds)).
		WithHeader("Retry-After", strconv.Itoa(retryAfterSeconds))
}
