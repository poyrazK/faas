package edgerules

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261108110000 — amendment 2026-11-09: a gateway older than sealed
// values applies an op's empty value, so apid seals only while every serving
// gateway freshly reports that it opens them.
func TestGatewaysOpenHeaderSecrets(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	report := func(node string, opens bool, age time.Duration) state.ServingGatewayTrafficRuntime {
		return state.ServingGatewayTrafficRuntime{NodeName: node, Generation: 7, ReportedAt: now.Add(-age), DatabaseNow: now,
			GatewayTrafficFeatures: state.GatewayTrafficFeatures{EdgeHeaderSecrets: opens}}
	}
	for _, tc := range []struct {
		name    string
		reports []state.ServingGatewayTrafficRuntime
		want    bool
	}{
		{"every gateway opens them", []state.ServingGatewayTrafficRuntime{report("a", true, time.Second), report("b", true, 2*time.Second)}, true},
		{"one gateway predates sealing", []state.ServingGatewayTrafficRuntime{report("a", true, time.Second), report("b", false, time.Second)}, false},
		{"one gateway never reported", []state.ServingGatewayTrafficRuntime{report("a", true, time.Second)}, false},
		{"one report is stale", []state.ServingGatewayTrafficRuntime{report("a", true, time.Second), report("b", true, time.Minute)}, false},
		{"one report is retired", []state.ServingGatewayTrafficRuntime{report("a", true, time.Second), {NodeName: "b", Generation: 7, DatabaseNow: now,
			GatewayTrafficFeatures: state.GatewayTrafficFeatures{EdgeHeaderSecrets: true}}}, false},
	} {
		if got := gatewaysOpenHeaderSecrets([]string{"a", "b"}, tc.reports); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func headerBackfillFixture(t *testing.T) (context.Context, *state.MemStore, state.Account, state.App, *Service, *[]string) {
	t.Helper()
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "backfill@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "shop", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	var sealed []string
	svc := &Service{
		Host: attackModeHost{store: mem, app: app, audits: &[]string{}, notif: &recordingNotifier{}},
		SealHeaderValue: func(plaintext string) ([]byte, error) {
			sealed = append(sealed, plaintext)
			return []byte("sealed:" + plaintext), nil
		},
	}
	return ctx, mem, acct, app, svc, &sealed
}

func legacyHeadersRule(t *testing.T, ctx context.Context, mem *state.MemStore, acct state.Account, app state.App, path, token string) state.EdgeRule {
	t.Helper()
	rule, err := mem.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
		AccountID: acct.ID, AppID: app.ID, MatchHost: "@app", MatchPath: path, Priority: 50, Enabled: true,
		Kind: state.EdgeRuleKindHeaders,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
			RequestHeaders: []state.EdgeRuleHeaderOp{
				{Name: "Authorization", Action: "set", Value: token},
				{Name: "X-Trace", Action: "set", Value: "plain"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return rule
}

// adr: 20261108110000 — amendment 2026-11-09: the backfill seals credential
// values stored as written in live rules and in rule-set versions, stores one
// row per value, leaves plain headers alone, and a second pass finds nothing.
func TestSealStoredHeaderValues(t *testing.T) {
	ctx, mem, acct, app, svc, sealed := headerBackfillFixture(t)
	rule := legacyHeadersRule(t, ctx, mem, acct, app, "/a", "Bearer legacy")
	// A second change records another version holding the same value.
	disabled := false
	if _, err := mem.UpdateEdgeRule(ctx, rule.ID, state.UpdateEdgeRuleParams{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}

	rep, err := svc.SealStoredHeaderValues(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.RulesSealed != 1 || rep.VersionsSealed < 2 || rep.Pending() || rep.SecretRowsStored != 1 || len(*sealed) != 1 {
		t.Fatalf("report = %+v, sealed = %q", rep, *sealed)
	}
	live, _ := mem.GetEdgeRuleByID(ctx, rule.ID)
	ops := live.Action.Headers.RequestHeaders
	if ops[0].Value != "" || ops[0].SecretID == "" || !ops[0].Secret || ops[1].Value != "plain" || ops[1].SecretID != "" {
		t.Fatalf("live ops = %+v", ops)
	}
	versions, _ := mem.ListEdgeRuleSetVersions(ctx, app.ID, 100)
	for _, v := range versions {
		full, err := mem.GetEdgeRuleSetVersion(ctx, app.ID, v.Version)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(full.Rules)
		if strings.Contains(string(raw), "Bearer legacy") {
			t.Fatalf("version %d still stores the value: %s", v.Version, raw)
		}
	}

	again, err := svc.SealStoredHeaderValues(ctx)
	if err != nil || again.RulesSealed+again.VersionsSealed != 0 || again.Pending() {
		t.Fatalf("second pass = %+v, %v", again, err)
	}
}

// adr: 20261108110000 — amendment 2026-11-09: while a registered gateway has
// not reported that it opens sealed values, a credential is stored as
// written (and still left out of responses), and the backfill waits.
func TestHeaderValuesStayUnsealedUntilEveryGatewayOpensThem(t *testing.T) {
	ctx, mem, acct, app, svc, sealed := headerBackfillFixture(t)
	role, target := "compute-node", "http://10.0.0.9:8081"
	if _, err := mem.CreateComputeNode(ctx, state.ComputeNode{Name: "node-a", Role: &role, GatewayTargetURL: &target, Active: true}); err != nil {
		t.Fatal(err)
	}
	if svc.FleetOpensHeaderSecrets(ctx) {
		t.Fatal("a gateway with no wiring report counted as opening sealed values")
	}
	action := state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
		RequestHeaders: []state.EdgeRuleHeaderOp{{Name: "Authorization", Action: "set", Value: "Bearer early"}},
	}}
	if prob := svc.sealHeaderSecrets(ctx, app, &action, nil); prob != nil {
		t.Fatalf("seal: %v", prob)
	}
	if got := action.Headers.RequestHeaders[0]; got.Value != "Bearer early" || got.SecretID != "" || len(*sealed) != 0 {
		t.Fatalf("op = %+v, sealed = %q; want the value as written until the fleet can open sealed values", got, *sealed)
	}
	if shown := RedactHeaderSecrets(action).Headers.RequestHeaders[0]; shown.Value != "" || !shown.Redacted {
		t.Fatalf("responses show %+v; want the value left out", shown)
	}
	legacyHeadersRule(t, ctx, mem, acct, app, "/a", "Bearer legacy")
	rep, err := svc.SealStoredHeaderValues(ctx)
	if err != nil || !rep.FleetNotReady || rep.RulesSealed != 0 {
		t.Fatalf("backfill = %+v, %v; want it to wait", rep, err)
	}
}
