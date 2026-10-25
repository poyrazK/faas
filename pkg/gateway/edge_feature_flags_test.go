package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// edgeFeatureSet releases the given edge feature flags to account only.
func edgeFeatureSet(account string, released ...string) *platformflags.Set {
	defs := map[string]platformflags.Definition{}
	for _, flag := range released {
		defs[flag] = platformflags.FromAccountList(true, false, []string{account})
	}
	return platformflags.NewSet(defs)
}

// adr: 20261023113721 — for an account the edge_challenge flag leaves out,
// challenge rules serve no page, refuse nothing and leave the solve path
// to the app. The flag is decided per account; a handler without flags
// enforces.
func TestEdgeChallengeFlag(t *testing.T) {
	app := App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}
	rule := &EdgeRuleChallengeResolved{ID: "rule-1", AccountID: "acct-1", AppID: "app-1", Difficulty: 8, PassTTLSeconds: 3600}
	for name, tc := range map[string]struct {
		flags    *platformflags.Set
		enforced bool
	}{
		"no flags wired":            {nil, true},
		"unreleased":                {edgeFeatureSet("acct-1"), false},
		"only other features":       {edgeFeatureSet("acct-1", platformflags.EdgeWAFBlocking, platformflags.EdgePenaltyBox), false},
		"released to other account": {edgeFeatureSet("acct-2", platformflags.EdgeChallenge), false},
		"released":                  {edgeFeatureSet("acct-1", platformflags.EdgeChallenge), true},
	} {
		t.Run(name, func(t *testing.T) {
			h := challengeTestHandler(rule, time.Now())
			if tc.flags != nil {
				h.WithEdgeFeatureFlags(tc.flags)
			}
			if got := h.applyEdgeRuleChallenge(httptest.NewRecorder(), browserRequest(http.MethodGet, "/"), app, false); got != tc.enforced {
				t.Errorf("challenge served = %v, want %v", got, tc.enforced)
			}
			solve := browserRequestBody(http.MethodPost, ChallengeSolvePath, "token=forged&counter=1")
			solve.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if got := h.handleChallengeSolve(httptest.NewRecorder(), solve, app); got != tc.enforced {
				t.Errorf("solve path owned = %v, want %v", got, tc.enforced)
			}
		})
	}
}

// adr: 20261023113721 — for an account the edge_waf_blocking flag leaves
// out, a block rule is inspected as observe: no in-path check, no 403, the
// sample still taken.
func TestEdgeWAFBlockingFlag(t *testing.T) {
	app := App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}
	detected := WAFInlineResult{Outcome: WAFInlineDetected, RuleIDs: []int{913100}, Categories: []string{"scanner"}}
	for name, tc := range map[string]struct {
		flags    *platformflags.Set
		enforced bool
	}{
		"no flags wired": {nil, true},
		"unreleased":     {edgeFeatureSet("acct-1", platformflags.EdgeChallenge), false},
		"released":       {edgeFeatureSet("acct-1", platformflags.EdgeWAFBlocking), true},
	} {
		t.Run(name, func(t *testing.T) {
			inline := &fakeWAFInline{result: detected}
			rule := &EdgeRuleWAFResolved{ID: "rule-1", AccountID: "acct-1", ParanoiaLevel: 1, AnomalyThreshold: 5, Mode: api.EdgeWAFModeBlock}
			h := &Handler{edgeRules: wafRuleMatcher{rule: rule}, wafInspector: &recordingWAFInspector{}, wafInline: inline}
			if tc.flags != nil {
				h.WithEdgeFeatureFlags(tc.flags)
			}
			r := httptest.NewRequest(http.MethodGet, "http://app.example.test/admin/", nil)
			w := httptest.NewRecorder()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK, request: r}
			handled, submit := h.applyEdgeRuleWAF(rec, r, app, rec)
			if handled != tc.enforced || (inline.calls == 1) != tc.enforced {
				t.Errorf("blocked = %v after %d in-path checks, want enforced = %v", handled, inline.calls, tc.enforced)
			}
			if !tc.enforced && submit == nil {
				t.Error("unreleased block rule was not sampled as observe")
			}
		})
	}
}

// adr: 20261023113721 — for an account the edge_penalty_box flag leaves
// out, refusals box nobody, and withdrawing the flag releases clients
// already boxed.
func TestEdgePenaltyBoxFlag(t *testing.T) {
	app := App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}
	matcher := penaltyMatcher{penalties: map[string]api.EdgeRulePenalty{"rule-1": {Seconds: 600, After: 1, WithinSeconds: 60}}}
	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://p.example.com/", nil)
		r.RemoteAddr = "203.0.113.7:51234"
		return r
	}
	boxed := func(h *Handler) bool { return h.applyEdgePenaltyBox(httptest.NewRecorder(), req(), app, false) }

	unreleased := &Handler{edgeRules: matcher}
	unreleased.WithPenaltyBox(NewPenaltyBox()).WithEdgeFeatureFlags(edgeFeatureSet("acct-1", platformflags.EdgeChallenge))
	unreleased.penalizeEdgeRuleRejection(req(), app, "rule-1")
	if boxed(unreleased) {
		t.Fatal("unreleased penalty boxed a client")
	}

	flags := edgeFeatureSet("acct-1", platformflags.EdgePenaltyBox)
	released := &Handler{edgeRules: matcher}
	released.WithPenaltyBox(NewPenaltyBox()).WithEdgeFeatureFlags(flags)
	released.penalizeEdgeRuleRejection(req(), app, "rule-1")
	if !boxed(released) {
		t.Fatal("released penalty did not box the client")
	}
	if err := flags.Apply(platformflags.ConfigKey(platformflags.EdgePenaltyBox), []byte(`{"enabled":false,"default":false}`), 1); err != nil {
		t.Fatal(err)
	}
	if boxed(released) {
		t.Fatal("withdrawing the flag left the client boxed")
	}
}
