package gateway

import (
	"crypto/rand"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// challengeSolveMaxBody bounds the solve form: a token, a counter and a
// return path.
const challengeSolveMaxBody = 8 << 10

// WithEdgeChallengeKey arms kind=challenge (ADR-831 amendment 7) with the
// key from DeriveEdgeChallengeKey. Without it challenge rules are not
// enforced; neither are they for an account the edge_challenge platform
// flag leaves out (ADR-20261023113721).
func (h *Handler) WithEdgeChallengeKey(key []byte) *Handler {
	h.challengeKey = append([]byte(nil), key...)
	return h
}

func (h *Handler) challengeClock() time.Time {
	if h.challengeNow != nil {
		return h.challengeNow()
	}
	return time.Now()
}

func (h *Handler) challengeMatcher() ChallengeEdgeRuleMatcher {
	if h.challengeKey == nil || h.edgeRules == nil {
		return nil
	}
	m, _ := h.edgeRules.(ChallengeEdgeRuleMatcher)
	return m
}

// applyEdgeRuleChallenge is the kind=challenge gate. It runs after the auth
// gates, so a verified credential is known, and reports true when it
// answered the request: the challenge page for a browser navigation, 403
// challenge_required for anything else.
func (h *Handler) applyEdgeRuleChallenge(w http.ResponseWriter, r *http.Request, app App, deploymentSmoke bool) bool {
	m := h.challengeMatcher()
	if m == nil || deploymentSmoke || !h.edgeFeatureReleased(platformflags.EdgeChallenge, app) {
		return false
	}
	rule := m.MatchChallenge(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
	if rule == nil {
		h.metrics.ObserveEdgeRuleMatch("challenge", "miss")
		return false
	}
	if rule.AccountID != app.AccountID {
		h.metrics.ObserveEdgeRuleMatch("challenge", "blocked")
		return false
	}
	// ADR-20261021220456 amendment 1: an attack-mode rule is inert for an
	// account the edge_attack_mode flag leaves out. The solve path stays
	// open: no attack-mode page is served, and the app's own challenge rules
	// still need it.
	if rule.AttackMode && !h.edgeFeatureReleased(platformflags.EdgeAttackMode, app) {
		return false
	}
	h.metrics.ObserveEdgeRuleMatch("challenge", "match")
	if id := authenticatedFrom(r.Context()); id.APIKeyID != "" || id.ConsumerID != "" || id.JWTSubject != "" {
		h.metrics.ObserveEdgeChallenge(app.ID, "bypassed")
		return false
	}
	now := h.challengeClock()
	if validPass(h.challengeKey, r, app.ID, now) {
		h.metrics.ObserveEdgeChallenge(app.ID, "passed")
		return false
	}
	if !isBrowserNavigation(r) {
		api.WriteProblem(w, api.ErrChallengeRequired(rule.ID))
		h.metrics.ObserveEdgeChallenge(app.ID, "rejected")
		h.recordChallengeEvent(r, rule.ID, app.ID, EdgeRuleEventRejected)
		return true
	}
	difficulty := h.challengeDifficulty(rule, app.ID, now)
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	token := sealChallenge(h.challengeKey, "token", challengeTokenPrefix, challengeToken{
		AppID: app.ID, RuleID: rule.ID, Nonce: nonce, Difficulty: difficulty, PassTTL: rule.PassTTLSeconds,
		Expires: now.Add(api.EdgeChallengeTokenTTLSeconds * time.Second).Unix(),
	}, bindingFor(r))
	writeChallengePage(w, r, token, difficulty)
	h.metrics.ObserveEdgeChallenge(app.ID, "issued")
	if difficulty > rule.Difficulty {
		h.metrics.ObserveEdgeChallengeRaised(app.ID)
	}
	h.recordChallengeEvent(r, rule.ID, app.ID, EdgeRuleEventChallenged)
	return true
}

// challengeDifficulty is the difficulty for the challenge page served now:
// the rule's own, raised while this gateway serves the rule far more pages
// than usual (ADR-831 amendment 8). A step up or down is logged.
func (h *Handler) challengeDifficulty(rule *EdgeRuleChallengeResolved, appID string, now time.Time) int {
	difficulty, moved := h.challengeLoad.issue(rule, now)
	if moved != 0 && h.log != nil {
		msg := "edge challenge difficulty raised under load"
		if moved < 0 {
			msg = "edge challenge difficulty lowered as load eased"
		}
		h.log.Info(msg, "app_id", appID, "rule_id", rule.ID, "difficulty", difficulty,
			"base_difficulty", rule.Difficulty, "max_difficulty", rule.MaxDifficulty)
	}
	return difficulty
}

// handleChallengeSolve checks a solution posted to ChallengeSolvePath and,
// when it holds, sets the pass and redirects back with 303. It reports false
// for any request it does not own, so on hosts without a challenge rule the
// path belongs to the app. An expired token sends the browser back for a
// fresh challenge; any other failure is 403 challenge_required.
func (h *Handler) handleChallengeSolve(w http.ResponseWriter, r *http.Request, app App) bool {
	if r.URL.Path != ChallengeSolvePath || r.Method != http.MethodPost {
		return false
	}
	m := h.challengeMatcher()
	if m == nil || !h.edgeFeatureReleased(platformflags.EdgeChallenge, app) {
		return false
	}
	rule := m.HostChallengeRule(r.Context(), hostname(r.Host))
	if rule == nil || rule.AccountID != app.AccountID {
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, challengeSolveMaxBody)
	if r.ParseForm() != nil {
		return h.rejectChallengeSolve(w, r, app, rule.ID)
	}
	token, counter := r.PostForm.Get("token"), r.PostForm.Get("counter")
	back := safeReturnPath(r.PostForm.Get("return"))
	binding := bindingFor(r)
	var t challengeToken
	if !openChallenge(h.challengeKey, "token", challengeTokenPrefix, token, binding, &t) || t.AppID != app.ID {
		return h.rejectChallengeSolve(w, r, app, rule.ID)
	}
	now := h.challengeClock()
	w.Header().Set("Cache-Control", "no-store")
	if now.Unix() >= t.Expires {
		h.metrics.ObserveEdgeChallenge(app.ID, "expired")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return true
	}
	if !challengeWorkValid(token, counter, t.Difficulty) {
		return h.rejectChallengeSolve(w, r, app, t.RuleID)
	}
	pass := sealChallenge(h.challengeKey, "pass", challengePassPrefix, challengePass{
		AppID: app.ID, Expires: now.Add(time.Duration(t.PassTTL) * time.Second).Unix(),
	}, binding)
	http.SetCookie(w, challengeCookie(pass, t.PassTTL))
	h.metrics.ObserveEdgeChallenge(app.ID, "solved")
	h.recordChallengeEvent(r, t.RuleID, app.ID, EdgeRuleEventSolved)
	http.Redirect(w, r, back, http.StatusSeeOther)
	return true
}

// rejectChallengeSolve answers a solution that does not hold with 403.
// ruleID is the token's rule once the token opened, else the host's.
func (h *Handler) rejectChallengeSolve(w http.ResponseWriter, r *http.Request, app App, ruleID string) bool {
	api.WriteProblem(w, api.ErrChallengeRequired(ruleID))
	h.metrics.ObserveEdgeChallenge(app.ID, "invalid")
	h.recordChallengeEvent(r, ruleID, app.ID, EdgeRuleEventFailed)
	return true
}
