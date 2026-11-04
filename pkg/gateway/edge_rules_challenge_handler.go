package gateway

import (
	"crypto/rand"
	"net/http"
	"strings"
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
		h.observeChallenge(rule, app.ID, "bypassed")
		return false
	}
	now := h.challengeClock()
	if validPass(h.challengeKey, r, app, now) {
		h.observeChallenge(rule, app.ID, "passed")
		return false
	}
	if !isBrowserNavigation(r) {
		api.WriteProblem(w, api.ErrChallengeRequired(rule.ID))
		h.observeChallenge(rule, app.ID, "rejected")
		h.recordChallengeEvent(r, rule.ID, app.ID, EdgeRuleEventRejected)
		return true
	}
	difficulty := h.challengeDifficulty(rule, app.ID, now)
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	token := sealChallenge(h.challengeKey, "token", challengeTokenPrefix, challengeToken{
		AppID: app.ID, AccountID: app.AccountID, Host: challengeHost(r), RuleID: rule.ID,
		Nonce: nonce, Difficulty: difficulty, PassTTL: rule.PassTTLSeconds,
		Expires: now.Add(api.EdgeChallengeTokenTTLSeconds * time.Second).Unix(),
	}, bindingFor(r))
	writeChallengePage(w, r, token, difficulty)
	h.observeChallenge(rule, app.ID, "issued")
	if difficulty > rule.Difficulty {
		h.metrics.ObserveEdgeChallengeRaised(app.ID)
	}
	h.recordChallengeEvent(r, rule.ID, app.ID, EdgeRuleEventChallenged)
	return true
}

// observeChallenge counts a challenge decision, and an attack-mode rule's
// decision again as attack mode's (ADR-20261024082000 amendment 1).
func (h *Handler) observeChallenge(rule *EdgeRuleChallengeResolved, appID, outcome string) {
	h.metrics.ObserveEdgeChallenge(appID, outcome)
	if rule.AttackMode {
		h.metrics.ObserveEdgeAttackMode(appID, outcome)
	}
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

// challengeSolveRequest reports whether r is a solution for this gateway to
// check: a POST to ChallengeSolvePath carrying a challenge token in its query,
// as the page posts it, or on a host with an enforced challenge rule of the
// app's account. Otherwise the path belongs to the app. ServeHTTP asks before
// the redirect, rewrite and declared-route gates, which would otherwise move
// or refuse the post and leave the visitor unable to pass (ADR-831 amendment
// 17); the query token also finds the solve on a host whose challenged paths
// route to another of the account's apps, whose rules the host's app does
// not load.
func (h *Handler) challengeSolveRequest(r *http.Request, app App) bool {
	if r.URL.Path != ChallengeSolvePath || r.Method != http.MethodPost {
		return false
	}
	m := h.challengeMatcher()
	if m == nil || !h.edgeFeatureReleased(platformflags.EdgeChallenge, app) {
		return false
	}
	if strings.HasPrefix(r.URL.Query().Get("token"), challengeTokenPrefix) {
		return true
	}
	rule := m.HostChallengeRule(r.Context(), hostname(r.Host))
	return rule != nil && rule.AccountID == app.AccountID
}

// handleChallengeSolve checks the solution when challengeSolveRequest owns
// r, and reports whether it answered.
func (h *Handler) handleChallengeSolve(w http.ResponseWriter, r *http.Request, app App) bool {
	if !h.challengeSolveRequest(r, app) {
		return false
	}
	h.serveChallengeSolve(w, r, app)
	return true
}

// serveChallengeSolve checks a solution posted to ChallengeSolvePath and,
// when it holds, sets the pass and redirects back with 303. The token must
// have been issued on this host while the app's account owned it. An expired
// token sends the browser back for a fresh challenge; any other failure is
// 403 challenge_required.
func (h *Handler) serveChallengeSolve(w http.ResponseWriter, r *http.Request, app App) {
	r.Body = http.MaxBytesReader(w, r.Body, challengeSolveMaxBody)
	if r.ParseForm() != nil {
		h.rejectChallengeSolve(w, r, app.ID, h.hostChallengeRuleID(r, app))
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		token = r.PostForm.Get("token")
	}
	counter, back := r.PostForm.Get("counter"), safeReturnPath(r.PostForm.Get("return"))
	binding := bindingFor(r)
	var t challengeToken
	if !openChallenge(h.challengeKey, "token", challengeTokenPrefix, token, binding, &t) ||
		t.AccountID != app.AccountID || t.Host != challengeHost(r) {
		h.rejectChallengeSolve(w, r, app.ID, h.hostChallengeRuleID(r, app))
		return
	}
	now := h.challengeClock()
	w.Header().Set("Cache-Control", "no-store")
	if now.Unix() >= t.Expires {
		h.metrics.ObserveEdgeChallenge(t.AppID, "expired")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if !challengeWorkValid(token, counter, t.Difficulty) {
		h.rejectChallengeSolve(w, r, t.AppID, t.RuleID)
		return
	}
	pass := sealChallenge(h.challengeKey, "pass", challengePassPrefix, challengePass{
		AccountID: t.AccountID, Host: t.Host, Expires: now.Add(time.Duration(t.PassTTL) * time.Second).Unix(),
	}, binding)
	http.SetCookie(w, challengeCookie(pass, t.PassTTL))
	h.metrics.ObserveEdgeChallenge(t.AppID, "solved")
	h.recordChallengeEvent(r, t.RuleID, t.AppID, EdgeRuleEventSolved)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// hostChallengeRuleID is the rule a failed solution is reported against when
// its token did not open: the host's challenge rule, or "" when the host's
// app loads none.
func (h *Handler) hostChallengeRuleID(r *http.Request, app App) string {
	if m := h.challengeMatcher(); m != nil {
		if rule := m.HostChallengeRule(r.Context(), hostname(r.Host)); rule != nil && rule.AccountID == app.AccountID {
			return rule.ID
		}
	}
	return ""
}

// rejectChallengeSolve answers a solution that does not hold with 403.
// appID and ruleID are the token's once it opened.
func (h *Handler) rejectChallengeSolve(w http.ResponseWriter, r *http.Request, appID, ruleID string) {
	api.WriteProblem(w, api.ErrChallengeRequired(ruleID))
	h.metrics.ObserveEdgeChallenge(appID, "invalid")
	h.recordChallengeEvent(r, ruleID, appID, EdgeRuleEventFailed)
}
