package main

import (
	"context"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261108110000 — gatewayd-internal opens a sealed request header value
// of the rule's own app and applies it; a value of another app, under another
// namespace, or missing disables the rule instead of sending the request
// without its credential.
func TestResolveEdgeRuleHeaderSecrets(t *testing.T) {
	ctx := context.Background()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	seal := func(namespace, value string) []byte {
		b, err := secretbox.SealBytesTo([]*age.X25519Recipient{identity.Recipient()}, namespace, []byte(value), 0)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	mem := state.NewMemStore()
	const appID, otherApp = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	own, _ := mem.CreateEdgeRuleHeaderSecret(ctx, "acct", appID, seal(api.EdgeRuleHeaderSecretSealNamespace, "Bearer origin"))
	foreign, _ := mem.CreateEdgeRuleHeaderSecret(ctx, "acct", otherApp, seal(api.EdgeRuleHeaderSecretSealNamespace, "Bearer other"))
	wrongNS, _ := mem.CreateEdgeRuleHeaderSecret(ctx, "acct", appID, seal(api.AppPublicAuthBasicSealNamespace, "u\np"))

	rule := func(id, secretID string) state.EdgeRule {
		return state.EdgeRule{ID: id, AppID: appID, Enabled: true, Kind: state.EdgeRuleKindHeaders, Action: state.EdgeRuleAction{
			Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{RequestHeaders: []state.EdgeRuleHeaderOp{
				{Name: "X-Trace", Action: "set", Value: "1"},
				{Name: "Authorization", Action: "set", Secret: true, SecretID: secretID},
			}},
		}}
	}
	in := []state.EdgeRule{rule("own", own.ID), rule("foreign", foreign.ID), rule("namespace", wrongNS.ID), rule("missing", "33333333-3333-4333-8333-333333333333")}
	g := &gatewaydEdgeRules{store: mem, headerSecretOpener: newEdgeRuleHeaderSecretOpener([]*age.X25519Identity{identity})}
	out, err := g.resolveEdgeRuleHeaderSecrets(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !out[0].Enabled || out[0].Action.Headers.RequestHeaders[1].Value != "Bearer origin" {
		t.Fatalf("own secret not applied: %+v", out[0].Action.Headers.RequestHeaders)
	}
	for _, r := range out[1:] {
		if r.Enabled {
			t.Errorf("rule %s with an unusable secret is still enabled", r.ID)
		}
	}
	if in[0].Action.Headers.RequestHeaders[1].Value != "" {
		t.Fatal("the stored rule was modified; resolution must work on a copy")
	}

	compiled, errs := compileHeadersRules(out)
	if len(errs) != 0 || len(compiled) != 1 || compiled[0].RequestHeaders[1].Value != "Bearer origin" {
		t.Fatalf("compiled = %+v, errs = %v", compiled, errs)
	}

	// A cached value of one app is not served to another app's rule.
	stolen := rule("stolen", own.ID)
	stolen.AppID = otherApp
	out, err = g.resolveEdgeRuleHeaderSecrets(ctx, []state.EdgeRule{stolen})
	if err != nil || out[0].Enabled {
		t.Fatalf("another app's cached secret applied: enabled=%v err=%v", out[0].Enabled, err)
	}

	// Without an opener (no edge identity) the rule is not applied.
	none := &gatewaydEdgeRules{store: mem}
	out, err = none.resolveEdgeRuleHeaderSecrets(ctx, in[:1])
	if err != nil || out[0].Enabled {
		t.Fatalf("no opener: enabled=%v err=%v", out[0].Enabled, err)
	}
}

// adr: 20261110210000 — slice 3i: in inject mode the gateway opens nothing;
// the compiled op names the value and the rule's app for the instance's vmmd.
func TestInjectModeKeepsEdgeRuleHeaderSecretsSealed(t *testing.T) {
	for _, tc := range []struct {
		env    string
		inject bool
		bad    bool
	}{{"", false, false}, {"local", false, false}, {"inject", true, false}, {"vmmd", false, true}} {
		inject, err := edgeHeaderSecretsInjectFromEnv(func(string) string { return tc.env })
		if inject != tc.inject || (err != nil) != tc.bad {
			t.Errorf("FAAS_GATEWAY_EDGE_HEADER_SECRETS=%q = %v, %v", tc.env, inject, err)
		}
	}
	const appID = "11111111-1111-4111-8111-111111111111"
	in := []state.EdgeRule{{ID: "r", AppID: appID, Enabled: true, Kind: state.EdgeRuleKindHeaders, Action: state.EdgeRuleAction{
		Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{RequestHeaders: []state.EdgeRuleHeaderOp{
			{Name: "X-Trace", Action: "set", Value: "1"},
			{Name: "Authorization", Action: "set", Secret: true, SecretID: "s-1"},
		}},
	}}}
	g := &gatewaydEdgeRules{store: state.NewMemStore(), headerSecretsInject: true}
	out, err := g.resolveEdgeRuleHeaderSecrets(context.Background(), in)
	if err != nil || !out[0].Enabled {
		t.Fatalf("inject mode: enabled=%v err=%v", out[0].Enabled, err)
	}
	compiled, errs := compileHeadersRules(out)
	if len(errs) != 0 || len(compiled) != 1 {
		t.Fatalf("compiled = %+v, errs = %v", compiled, errs)
	}
	ops := compiled[0].RequestHeaders
	if ops[0].SecretID != "" || ops[0].Value != "1" {
		t.Fatalf("literal op = %+v", ops[0])
	}
	if ops[1].Value != "" || ops[1].SecretID != "s-1" || ops[1].SecretAppID != appID {
		t.Fatalf("sealed op = %+v, want a reference to the rule's app", ops[1])
	}
}
