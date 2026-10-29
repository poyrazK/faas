package sealmigrate_test

import (
	"bytes"
	"context"
	"sort"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionpayload"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// accountValues is an in-memory set of sealed account values, keyed by kind
// then id. rotate names one id a customer changes between the re-seal's
// read and its write.
type accountValues struct {
	rows   map[string]map[string]state.AccountSealedValue
	rotate string
}

func (f *accountValues) put(v state.AccountSealedValue) {
	if f.rows[v.Kind] == nil {
		f.rows[v.Kind] = map[string]state.AccountSealedValue{}
	}
	f.rows[v.Kind][v.ID] = v
}

func (f *accountValues) ListAccountSealedValues(_ context.Context, kind, afterID string, limit int) ([]state.AccountSealedValue, error) {
	var out []state.AccountSealedValue
	for id, v := range f.rows[kind] {
		if id > afterID {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *accountValues) ResealAccountValue(_ context.Context, v state.AccountSealedValue, sealed []byte, kid string) (bool, error) {
	if v.ID == f.rotate {
		f.rotate = ""
		return false, nil
	}
	row, ok := f.rows[v.Kind][v.ID]
	if !ok || !bytes.Equal(row.Sealed, v.Sealed) {
		return false, nil
	}
	row.Sealed = sealed
	if v.Kind == state.AccountSealedWorkflowPlan {
		row.KID = kid
	}
	f.rows[v.Kind][v.ID] = row
	return true, nil
}

func TestMigrateAccount(t *testing.T) {
	domain, fleet := mustIdentity(t), mustIdentity(t)
	to := func(ids ...*age.X25519Identity) []*age.X25519Recipient {
		var rs []*age.X25519Recipient
		for _, id := range ids {
			rs = append(rs, id.Recipient())
		}
		return rs
	}
	envelope := func(env secretbox.Envelope, ids ...*age.X25519Identity) []byte {
		b, err := secretbox.SealTo(to(ids...), env)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	blob := func(namespace, value string, ids ...*age.X25519Identity) []byte {
		b, err := secretbox.SealBytesTo(to(ids...), namespace, []byte(value), 4096)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	plan := []byte(`{"steps":[]}`)
	sealedPlan, err := executionpayload.SealWorkflowPlanTo(to(fleet), plan)
	if err != nil {
		t.Fatal(err)
	}

	f := &accountValues{rows: map[string]map[string]state.AccountSealedValue{}}
	mfa := secretbox.Envelope{"MFA_SECRET": "JBSWY3DPEHPK3PXP"}
	f.put(state.AccountSealedValue{Kind: state.AccountSealedMFASecret, ID: "a1", Sealed: envelope(mfa, fleet)})
	f.put(state.AccountSealedValue{Kind: state.AccountSealedMFASecret, ID: "a2", Sealed: envelope(mfa, domain, fleet)})
	f.put(state.AccountSealedValue{Kind: state.AccountSealedMFASecret, ID: "a3", Sealed: envelope(mfa, domain)})
	// Another envelope kind in the column is not re-sealed as a TOTP secret.
	f.put(state.AccountSealedValue{Kind: state.AccountSealedMFASecret, ID: "a4", Sealed: envelope(secretbox.Envelope{"OTHER": "x"}, fleet)})
	f.put(state.AccountSealedValue{Kind: state.AccountSealedInboundWebhook, ID: "w1", Sealed: blob(api.InboundWebhookSecretNamespace, "whsec_inbound", fleet)})
	f.put(state.AccountSealedValue{Kind: state.AccountSealedBillingKey, ID: "acct-1/stripe", Sealed: blob(api.BillingProviderKeyNamespace("stripe", "acct-1"), "sk_live_x", fleet)})
	// A key copied from another account does not open as this one's.
	f.put(state.AccountSealedValue{Kind: state.AccountSealedBillingKey, ID: "acct-2/stripe", Sealed: blob(api.BillingProviderKeyNamespace("stripe", "acct-1"), "sk_live_x", fleet)})
	f.put(state.AccountSealedValue{Kind: state.AccountSealedWorkflowPlan, ID: "p1", Sealed: sealedPlan, KID: fleet.Recipient().String()})
	f.put(state.AccountSealedValue{Kind: state.AccountSealedPagerDutyAck, ID: "rule-1/primary", Sealed: blob(api.MCPReadinessPagerDutyWebhookSecretNamespace, "pd-secret", fleet)})
	f.put(state.AccountSealedValue{Kind: state.AccountSealedPagerDutyAck, ID: "rule-1/fallback", Sealed: blob(api.MCPReadinessPagerDutyWebhookSecretNamespace, "pd-secret-2", fleet)})
	f.rotate = "rule-1/fallback"

	report, err := sealmigrate.MigrateAccount(context.Background(), f, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	want := sealmigrate.MigrationReport{Domain: sealdomain.Account, Target: domain.Recipient().String(),
		Scanned: 10, Resealed: 6, Current: 1, Changed: 1, Failed: 2}
	if report != want {
		t.Fatalf("report = %+v, want %+v", report, want)
	}

	for kind, id := range map[string]string{state.AccountSealedMFASecret: "a1", state.AccountSealedInboundWebhook: "w1",
		state.AccountSealedBillingKey: "acct-1/stripe", state.AccountSealedWorkflowPlan: "p1", state.AccountSealedPagerDutyAck: "rule-1/primary"} {
		v := f.rows[kind][id]
		if _, _, err := secretbox.OpenBytes(fleet, v.Sealed); err == nil {
			t.Errorf("%s %s still opens with the fleet identity", kind, id)
		}
		if _, err := secretbox.Open(fleet, v.Sealed); err == nil {
			t.Errorf("%s %s still opens with the fleet identity", kind, id)
		}
	}
	if env, err := secretbox.OpenMulti([]*age.X25519Identity{domain}, f.rows[state.AccountSealedMFASecret]["a1"].Sealed); err != nil || env["MFA_SECRET"] != mfa["MFA_SECRET"] {
		t.Fatalf("TOTP secret domain open = %v", err)
	}
	if ns, _, err := secretbox.OpenBytes(domain, f.rows[state.AccountSealedBillingKey]["acct-1/stripe"].Sealed); err != nil || ns != api.BillingProviderKeyNamespace("stripe", "acct-1") {
		t.Fatalf("billing key domain open = %q, %v", ns, err)
	}
	p := f.rows[state.AccountSealedWorkflowPlan]["p1"]
	if p.KID != domain.Recipient().String() {
		t.Fatalf("plan key id = %q, want the domain recipient", p.KID)
	}
	if got, err := executionpayload.DecodeWorkflowPlan(context.Background(), []*age.X25519Identity{domain}, p.Sealed, p.KID); err != nil || !bytes.Equal(got, plan) {
		t.Fatalf("worker decode of the re-sealed plan = %q, %v", got, err)
	}

	again, err := sealmigrate.MigrateAccount(context.Background(), f, domain, []*age.X25519Identity{fleet})
	if err != nil || again.Resealed != 1 || again.Changed != 0 || again.Current != 7 {
		t.Fatalf("second run = %+v, %v; want only the rotated acknowledgement re-sealed", again, err)
	}
}
