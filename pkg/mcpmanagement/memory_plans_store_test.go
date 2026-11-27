package mcpmanagement

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
)

// Flag, budget and reviewed plans lived only in memory, so every hosted
// bridge restart (each release) dropped them. They are journaled with the
// connection's state: a rebuilt server applies the same plans, replays
// their receipts and keeps the flag attempt marker.
func TestMemoryPlansSurviveARestart(t *testing.T) {
	store, _, _ := testStateStore(t)
	opts := Options{APIBase: "http://127.0.0.1:1", Token: testToken, planStore: store, planNamespace: "grant-1", accountID: "acct-1"}
	_, first, err := newManagementServer(api.NewClient(opts.APIBase, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(10 * time.Minute)
	if _, err := first.saveFlagPlan(&flagPlan{AccountID: "acct-1", Project: "shop", Environment: "production", ExpectedVersion: 4, ExpiresAt: expires, config: flags.Config{}}); err != nil {
		t.Fatal(err)
	}
	var flagID string
	for id := range first.flagPlans {
		flagID = id
	}
	first.mu.Lock()
	first.flagPlans[flagID].attempted = true
	first.reviewedPlans["r"+flagID[1:]] = &reviewedPlan{ID: "r" + flagID[1:], Kind: "schema_registration", AccountID: "acct-1", Request: json.RawMessage(`{"name":"order.paid"}`), ExpiresAt: expires, receipt: map[string]any{"plan_id": "done"}}
	first.budgetPlans["b"+flagID[1:]] = &budgetPlan{ID: "b" + flagID[1:], AccountID: "other-account", ExpiresAt: expires}
	if err := first.persistMemoryPlansLocked(); err != nil {
		t.Fatal(err)
	}
	first.mu.Unlock()

	_, second, err := newManagementServer(api.NewClient(opts.APIBase, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	p := second.flagPlans[flagID]
	if p == nil || !p.attempted || p.ExpectedVersion != 4 || p.Project != "shop" {
		t.Fatalf("flag plan after restart = %+v", p)
	}
	r := second.reviewedPlans["r"+flagID[1:]]
	if r == nil || r.receipt == nil {
		t.Fatalf("reviewed plan after restart = %+v", r)
	}
	if out, err := second.applyReviewedChange(t.Context(), applyArgs{PlanID: r.ID}); err != nil || out == nil {
		t.Fatalf("receipt replay = %v %v", out, err)
	}
	if second.budgetPlans["b"+flagID[1:]] != nil {
		t.Fatal("restored a plan bound to another account")
	}
}

func TestLiveMemoryPlansKeepHeadlessState(t *testing.T) {
	now := time.Now()
	live, _ := json.Marshal(storedMemoryPlans{Version: 1, Flags: map[string]storedFlagPlan{"p": {Plan: flagPlan{ExpiresAt: now.Add(time.Minute)}}}})
	stale, _ := json.Marshal(storedMemoryPlans{Version: 1, Budgets: map[string]storedBudgetPlan{"p": {Plan: budgetPlan{ExpiresAt: now.Add(-time.Minute)}}}})
	if !liveMemoryPlans(live, now) || liveMemoryPlans(stale, now) || liveMemoryPlans(nil, now) {
		t.Fatal("liveMemoryPlans misjudged plan expiry")
	}
}
