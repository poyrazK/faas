package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// adr: 20261021220456 — amendment 1: an attack-mode rule challenges only
// while both edge_challenge and edge_attack_mode include the account; an
// ordinary challenge rule needs edge_challenge alone.
func TestEdgeAttackModeFlag(t *testing.T) {
	app := App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanFree}
	for name, tc := range map[string]struct {
		attackMode bool
		flags      *platformflags.Set
		want       bool
	}{
		"attack mode, both released":        {true, edgeFeatureSet("acct-1", platformflags.EdgeChallenge, platformflags.EdgeAttackMode), true},
		"attack mode, only challenge":       {true, edgeFeatureSet("acct-1", platformflags.EdgeChallenge), false},
		"attack mode, only attack mode":     {true, edgeFeatureSet("acct-1", platformflags.EdgeAttackMode), false},
		"ordinary rule, only challenge":     {false, edgeFeatureSet("acct-1", platformflags.EdgeChallenge), true},
		"attack mode, no flags wired":       {true, nil, true},
		"attack mode, released to another":  {true, edgeFeatureSet("acct-2", platformflags.EdgeChallenge, platformflags.EdgeAttackMode), false},
		"ordinary rule, nothing released":   {false, edgeFeatureSet("acct-1"), false},
		"attack mode, challenge to another": {true, edgeFeatureSet("acct-1", platformflags.EdgeAttackMode), false},
	} {
		t.Run(name, func(t *testing.T) {
			rule := &EdgeRuleChallengeResolved{ID: "rule-1", AccountID: "acct-1", AppID: "app-1", Difficulty: 8, PassTTLSeconds: 3600, AttackMode: tc.attackMode}
			h := challengeTestHandler(rule, time.Now())
			if tc.flags != nil {
				h.WithEdgeFeatureFlags(tc.flags)
			}
			if got := h.applyEdgeRuleChallenge(httptest.NewRecorder(), browserRequest(http.MethodGet, "/"), app, false); got != tc.want {
				t.Errorf("challenged = %v, want %v", got, tc.want)
			}
		})
	}
}

// adr: 20261024082000 — amendment 1: an attack-mode rule's decisions count
// in gateway_edge_attack_mode_total as well as the challenge counter; an
// ordinary challenge rule's count in the challenge counter only.
func TestEdgeAttackModeMetric(t *testing.T) {
	app := App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanFree}
	script := httptest.NewRequest(http.MethodGet, "/api/items", nil)
	for _, attackMode := range []bool{true, false} {
		rule := &EdgeRuleChallengeResolved{ID: "rule-1", AccountID: "acct-1", AppID: "app-1", Difficulty: 8, PassTTLSeconds: 3600, AttackMode: attackMode}
		h := challengeTestHandler(rule, time.Now())
		h.metrics = NewMetrics()
		h.applyEdgeRuleChallenge(httptest.NewRecorder(), browserRequest(http.MethodGet, "/"), app, false)
		h.applyEdgeRuleChallenge(httptest.NewRecorder(), script, app, false)
		want := 0.0
		if attackMode {
			want = 1
		}
		for _, outcome := range []string{"issued", "rejected"} {
			if got := testutil.ToFloat64(h.metrics.edgeChallenge.WithLabelValues("app-1", outcome)); got != 1 {
				t.Errorf("attack mode %v: challenge %s = %v, want 1", attackMode, outcome, got)
			}
			if got := testutil.ToFloat64(h.metrics.edgeAttackMode.WithLabelValues("app-1", outcome)); got != want {
				t.Errorf("attack mode %v: attack mode %s = %v, want %v", attackMode, outcome, got, want)
			}
		}
	}
}
