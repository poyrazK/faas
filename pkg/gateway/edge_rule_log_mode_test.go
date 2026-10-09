// adr: 904
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type recordedHit struct {
	rule, app string
	logged    bool
}

type fakeHitRecorder struct {
	mu   sync.Mutex
	hits []recordedHit
}

func (f *fakeHitRecorder) RecordEdgeRuleHit(ruleID, appID string, logged bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits = append(f.hits, recordedHit{ruleID, appID, logged})
}

// adr: 904 — a log-mode rule never acts or shadows: the kind's effective
// rule is chosen from enforced rules only, while the first matching log-mode
// rule is counted. Each rule is counted at most once per request even when
// the kind is looked up again.
func TestLogModeRulesAreCountedButNeverApplied(t *testing.T) {
	rules := []EdgeRuleRedirectResolved{
		{EdgeRuleCondition: EdgeRuleCondition{RuleID: "shadow", AppID: "app", LogOnly: true}, ID: "shadow", AccountID: "acct", Priority: 1, To: "/new"},
		{EdgeRuleCondition: EdgeRuleCondition{RuleID: "live", AppID: "app"}, ID: "live", AccountID: "acct", Priority: 5, To: "/live"},
	}
	account := func(r *EdgeRuleRedirectResolved) string { return r.AccountID }
	rec := &fakeHitRecorder{}
	req := httptest.NewRequest(http.MethodGet, "http://a.example.com/x", nil)
	ctx := WithEdgeRuleMatchContext(WithEdgeRuleOwner(context.Background(), "acct"), NewEdgeRuleMatchContext(req, nil, nil, rec))

	lookup := func() *EdgeRuleRedirectResolved {
		enforced := PickFirstRedirectMatch(ApplicableEdgeRules(ctx, rules, account, "/x", http.MethodGet), "/x", http.MethodGet)
		logged := PickFirstRedirectMatch(LoggedEdgeRules(ctx, rules, account, "/x", http.MethodGet), "/x", http.MethodGet)
		return ObserveEdgeRuleMatch(ctx, enforced, logged)
	}
	got := lookup()
	if got == nil || got.ID != "live" {
		t.Fatalf("effective rule = %v, want live (log-mode shadow must not apply)", got)
	}
	lookup()
	if len(rec.hits) != 2 {
		t.Fatalf("hits = %+v, want one matched and one logged", rec.hits)
	}
	want := map[string]bool{"live": false, "shadow": true}
	for _, h := range rec.hits {
		if logged, ok := want[h.rule]; !ok || logged != h.logged || h.app != "app" {
			t.Fatalf("unexpected hit %+v", h)
		}
	}
}

func TestLoggedEdgeRulesIsNilWithoutLogModeRules(t *testing.T) {
	rules := []EdgeRuleRedirectResolved{{ID: "live", AccountID: "acct"}}
	if got := LoggedEdgeRules(context.Background(), rules, func(r *EdgeRuleRedirectResolved) string { return r.AccountID }, "/", http.MethodGet); got != nil {
		t.Fatalf("LoggedEdgeRules = %v, want nil", got)
	}
}
