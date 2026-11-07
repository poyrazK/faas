package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// scopedChallengeMatcher answers with the challenge rule of the app the
// request's edge-rule scope names.
type scopedChallengeMatcher struct {
	noOpEdgeRuleMatcher
	byApp map[string]*EdgeRuleChallengeResolved
}

func (m scopedChallengeMatcher) MatchChallenge(ctx context.Context, _, _, _ string) *EdgeRuleChallengeResolved {
	return m.byApp[EdgeRuleApp(ctx)]
}

func (m scopedChallengeMatcher) HostChallengeRule(ctx context.Context, _ string) *EdgeRuleChallengeResolved {
	return m.byApp[EdgeRuleApp(ctx)]
}

// adr: 091 — amendment 2026-11-04 (review round 12): a request a route rule
// serves on another app still takes the attack mode of the app its host
// belongs to; the source app's other rules stay its own.
func TestRoutedRequestsTakeTheSourceAttackMode(t *testing.T) {
	source := App{ID: "source", AccountID: "acct", Plan: api.PlanPro}
	target := App{ID: "target", AccountID: "acct", Plan: api.PlanPro}
	attack := &EdgeRuleChallengeResolved{ID: "attack", AccountID: "acct", AppID: "source", AttackMode: true, Difficulty: 4, PassTTLSeconds: 600}
	plain := &EdgeRuleChallengeResolved{ID: "plain", AccountID: "acct", AppID: "source", Difficulty: 4, PassTTLSeconds: 600}
	for _, tc := range []struct {
		name   string
		rule   *EdgeRuleChallengeResolved
		routed bool
		want   bool
	}{
		{name: "routed request, source in attack mode", rule: attack, routed: true, want: true},
		{name: "routed request, source has an ordinary challenge rule", rule: plain, routed: true, want: false},
		{name: "direct request to the target", rule: attack, routed: false, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{edgeRules: scopedChallengeMatcher{byApp: map[string]*EdgeRuleChallengeResolved{"source": tc.rule}}}
			h.WithEdgeChallengeKey(challengeTestKey)
			h.challengeNow = func() time.Time { return time.Unix(1_800_000_000, 0) }
			r := browserRequest(http.MethodGet, "http://shop.example.test/legacy/page")
			ctx := WithEdgeRuleScope(r.Context(), target)
			if tc.routed {
				ctx = withEdgeRouteSource(ctx, source)
			}
			r = r.WithContext(ctx)
			w := httptest.NewRecorder()
			if got := h.applyEdgeRuleChallenge(w, r, target, false); got != tc.want {
				t.Fatalf("challenged = %v, want %v (status %d)", got, tc.want, w.Code)
			}
		})
	}
}
