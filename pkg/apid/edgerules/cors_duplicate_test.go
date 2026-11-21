package edgerules

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// prod 2026-11-15: a second CORS rule on the same host and path was accepted
// and then never applied, because the edge uses only the first matching CORS
// rule. It is refused now, naming the rule that already covers the path.
func TestSecondCORSRuleOnTheSamePathIsRefused(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "cors-dup@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "cors-dup", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{Host: attackModeHost{store: mem, app: app, audits: &[]string{}, notif: &recordingNotifier{}}}
	first, prob := svc.CreateRule(ctx, acct, app, api.CreateEdgeRuleRequest{MatchHost: "*", MatchPath: "/*", Kind: "cors", Action: json.RawMessage(`{"allow_origins":["https://good.example"],"allow_methods":["GET"]}`)})
	if prob != nil {
		t.Fatal(prob)
	}
	_, prob = svc.CreateRule(ctx, acct, app, api.CreateEdgeRuleRequest{MatchHost: "*", MatchPath: "/*", Kind: "cors", Action: json.RawMessage(`{"allow_origins":["https://*.example"],"allow_methods":["GET"]}`)})
	if prob == nil || prob.Status != http.StatusBadRequest {
		t.Fatalf("duplicate CORS rule was not refused: %v", prob)
	}
	if want := first.ID; !strings.Contains(prob.Detail, want) {
		t.Fatalf("detail %q does not name the covering rule %s", prob.Detail, want)
	}
	if _, prob := svc.CreateRule(ctx, acct, app, api.CreateEdgeRuleRequest{MatchHost: "*", MatchPath: "/api/*", Kind: "cors", Action: json.RawMessage(`{"allow_origins":["https://*.example"],"allow_methods":["GET"]}`)}); prob != nil {
		t.Fatalf("a CORS rule on another path was refused: %v", prob)
	}
}
