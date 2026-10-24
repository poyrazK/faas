package edgerules

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// adr: 20261023113721 — recipe suggestions offer a challenge alternative only
// once challenge rules are released to the account.
func TestRecipeSuggestionsHideUnreleasedChallenge(t *testing.T) {
	app := state.App{ID: "0b8f6a52-3f4e-4f39-9a51-5d1c0f6f8a11", AccountID: "5a0c2b8e-1d7f-4c3a-8e6b-2f9d4a7c1e33", Slug: "shop"}
	fake := &telemetryStore{
		MemStore: state.NewMemStore(),
		routes: []sqlc.RequestTelemetryAnalyticsByRouteRow{
			{Route: "POST /login", Method: "POST", Requests: 900, ErrorRequests: 400},
		},
	}
	acct := state.Account{ID: app.AccountID, Plan: api.PlanPro}
	alternatives := func(released map[string]bool) int {
		t.Helper()
		svc := &Service{Host: releaseHost{suggestionHost: suggestionHost{store: statetest.NewMem(fake), app: app}, released: released}}
		out, err := svc.RecipeSuggestions(t.Context(), acct, app, 7)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, sg := range out.Suggestions {
			if sg.Alternative != nil && sg.Alternative.Recipe == api.EdgeRuleRecipeChallengePath {
				n++
			}
		}
		return n
	}
	if n := alternatives(nil); n != 0 {
		t.Fatalf("unreleased: %d challenge alternatives, want none", n)
	}
	if n := alternatives(map[string]bool{platformflags.EdgeChallenge: true}); n == 0 {
		t.Fatal("released: no challenge alternative for the busy sign-in route")
	}
}
