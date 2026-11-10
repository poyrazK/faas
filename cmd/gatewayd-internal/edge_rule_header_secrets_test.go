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
