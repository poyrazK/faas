package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
