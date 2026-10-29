package gateway

// ADR-831 amendment 7 follow-up: kind=challenge outcomes are ADR-964
// security events, so a customer can see who was challenged, who was turned
// away for not being a browser, and who solved or forged a solution. Passes
// and verified-credential bypasses stay metric-only: every returning visitor
// would produce one, and they would bury the outcomes worth reading.

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Event outcomes for kind=challenge, alongside "matched" and "logged".
const (
	// EdgeRuleEventChallenged is a browser navigation answered with the
	// challenge page.
	EdgeRuleEventChallenged = "challenged"
	// EdgeRuleEventRejected is a request that is not a browser navigation,
	// answered with 403 challenge_required.
	EdgeRuleEventRejected = "rejected"
	// EdgeRuleEventSolved is a solution that earned a pass.
	EdgeRuleEventSolved = "solved"
	// EdgeRuleEventFailed is a solution answered with 403: a token that is
	// forged, foreign or unreadable, or work short of the difficulty. An
	// expired token is not a failure; the browser is sent back for a fresh
	// challenge, which records its own event.
	EdgeRuleEventFailed = "failed"
)

// recordChallengeEvent samples a challenge outcome as a security event. It
// draws on the per-rule budget kind=waf detections use; a challenge rule's
// ID is its own, so the two kinds never compete for it.
func (h *Handler) recordChallengeEvent(r *http.Request, ruleID, appID, outcome string) {
	rec, ok := h.edgeRuleHits.(WAFEventRecorder)
	if !ok || ruleID == "" || !rec.SampleEdgeRuleDetection(ruleID) {
		return
	}
	rec.RecordEdgeRuleEvent(outcomeEvent(r, ruleID, appID, outcome))
}

// outcomeEvent snapshots the request like a sampled match, from the
// request's match context when the handler built one, for an outcome
// recorded instead of a match: a challenge outcome or a penalty box.
func outcomeEvent(r *http.Request, ruleID, appID, outcome string) EdgeRuleEvent {
	if m := edgeRuleMatchContextFrom(r.Context()); m != nil {
		ev := m.event(ruleID, appID, false)
		ev.Outcome = outcome
		return ev
	}
	ev := EdgeRuleEvent{
		RuleID: ruleID, AppID: appID, Outcome: outcome, At: time.Now().UTC(),
		RequestID: truncateBytes(r.Header.Get(api.RequestIDHeader), 128),
		Method:    truncateBytes(r.Method, 16),
		Host:      truncateBytes(hostname(r.Host), 253),
		Path:      truncateBytes(r.URL.Path, EdgeRuleEventMaxPathBytes),
		UserAgent: truncateBytes(r.Header.Get("User-Agent"), EdgeRuleEventMaxUserAgentBytes),
	}
	if ip := challengeClientIP(r); ip != nil {
		ev.ClientIP = ip.String()
	}
	return ev
}

// suppressEnforcedEdgeRuleMatchEvents marks kind=challenge: an enforced
// rule's matches are counted (ADR-960), and its sampled events are the
// outcomes above. A log-mode rule never acts, so its matches stay "logged"
// events and the dry run shows who would have been challenged.
func (EdgeRuleChallengeResolved) suppressEnforcedEdgeRuleMatchEvents() {}

// edgeRuleEnforcedMatchEventsSuppressed is implemented by rule kinds whose
// enforced matches record outcome events instead of "matched" ones.
type edgeRuleEnforcedMatchEventsSuppressed interface{ suppressEnforcedEdgeRuleMatchEvents() }
