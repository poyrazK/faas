package edgerules

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func environmentHeaderRule(ops ...state.EdgeRuleHeaderOp) state.ProjectEnvironmentEdgeRule {
	return state.ProjectEnvironmentEdgeRule{Kind: state.EdgeRuleKindHeaders, MatchPath: "/", Priority: 100, Enabled: true,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{RequestHeaders: ops}}}
}

// adr: 20261108110000 — amendment 2026-11-09: environment policy header
// credentials are sealed as an application rule's, as values of the app; a
// redacted op takes the stored value of its own rule first; responses leave
// values out.
func TestEnvironmentPolicyHeadersAreSealed(t *testing.T) {
	ctx, _, _, app, svc, sealed := headerBackfillFixture(t)
	rules := []state.ProjectEnvironmentEdgeRule{
		environmentHeaderRule(state.EdgeRuleHeaderOp{Name: "Authorization", Action: "set", Value: "Bearer env"},
			state.EdgeRuleHeaderOp{Name: "X-Trace", Action: "set", Value: "plain"}),
		{Kind: state.EdgeRuleKindCORSA, MatchPath: "/", Enabled: true, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindCORSA, CORS: &state.EdgeRuleCORSAction{}}},
	}
	if prob := svc.SealEnvironmentPolicyHeaders(ctx, app, rules, nil); prob != nil {
		t.Fatal(prob)
	}
	ops := rules[0].Action.Headers.RequestHeaders
	if ops[0].Value != "" || ops[0].SecretID == "" || ops[1].Value != "plain" || len(*sealed) != 1 {
		t.Fatalf("ops = %+v, sealed = %q", ops, *sealed)
	}
	rows, _ := svc.Host.Store().(state.EdgeRuleHeaderSecretStore).EdgeRuleHeaderSecretsByID(ctx, app.ID, []string{ops[0].SecretID})
	if len(rows) != 1 {
		t.Fatalf("the value is not a sealed value of the app: %+v", rows)
	}

	// Two stored rules set the same header; each redacted op keeps its own.
	previous := []state.ProjectEnvironmentEdgeRule{
		environmentHeaderRule(state.EdgeRuleHeaderOp{Name: "X-Api-Key", Action: "set", Value: "first"}),
		environmentHeaderRule(state.EdgeRuleHeaderOp{Name: "X-Api-Key", Action: "set", Value: "second"}),
	}
	sent := []state.ProjectEnvironmentEdgeRule{
		environmentHeaderRule(state.EdgeRuleHeaderOp{Name: "X-Api-Key", Action: "set", Redacted: true}),
		environmentHeaderRule(state.EdgeRuleHeaderOp{Name: "X-Api-Key", Action: "set", Redacted: true}),
	}
	*sealed = nil
	if prob := svc.SealEnvironmentPolicyHeaders(ctx, app, sent, previous); prob != nil {
		t.Fatal(prob)
	}
	if len(*sealed) != 2 || (*sealed)[0] != "first" || (*sealed)[1] != "second" {
		t.Fatalf("sealed = %q; want each rule's own stored value", *sealed)
	}

	shown := RedactEnvironmentPolicyAction(previous[0]).Headers.RequestHeaders[0]
	if shown.Value != "" || !shown.Redacted {
		t.Fatalf("responses show %+v", shown)
	}
}

// adr: 20261108110000 — amendment 2026-11-09: the backfill seals an
// environment policy stored as written through the convergence path.
func TestSealStoredEnvironmentPolicyHeaders(t *testing.T) {
	ctx, mem, acct, _, svc, sealed := headerBackfillFixture(t)
	project, err := mem.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-api", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mem.PutProjectEnvironmentEdgePolicy(ctx, state.ProjectEnvironmentEdgePolicy{AccountID: acct.ID, ProjectID: project.ID,
		AppID: app.ID, EnvironmentSlug: "production", Rules: []state.ProjectEnvironmentEdgeRule{
			environmentHeaderRule(state.EdgeRuleHeaderOp{Name: "Authorization", Action: "set", Value: "Bearer stored"}),
		}}); err != nil {
		t.Fatal(err)
	}
	var hosts []string
	svc.EnvironmentPolicyHost = func(_ context.Context, policy state.ProjectEnvironmentEdgePolicy) (string, error) {
		hosts = append(hosts, policy.AppID+"."+policy.EnvironmentSlug)
		return "env.example.test", nil
	}
	rep, err := svc.SealStoredHeaderValues(ctx)
	if err != nil || rep.PoliciesSealed != 1 || rep.Pending() || len(hosts) != 1 {
		t.Fatalf("report = %+v, err = %v, hosts = %v", rep, err, hosts)
	}
	stored, err := mem.GetProjectEnvironmentEdgePolicy(ctx, acct.ID, app.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	op := stored.Rules[0].Action.Headers.RequestHeaders[0]
	if op.Value != "" || op.SecretID == "" || len(*sealed) != 1 || !strings.Contains((*sealed)[0], "Bearer stored") {
		t.Fatalf("stored op = %+v, sealed = %q", op, *sealed)
	}
	if again, err := svc.SealStoredHeaderValues(ctx); err != nil || again.PoliciesSealed != 0 || again.Pending() {
		t.Fatalf("second pass = %+v, %v", again, err)
	}
}
