package sealmigrate_test

import (
	"bytes"
	"context"
	"sort"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// alertingValues is an in-memory set of alerting rows.
type alertingValues struct {
	rows   map[string]state.AlertingSealedCredential // keyed by kind|id
	rotate string
}

func (f *alertingValues) ListAlertingSealedCredentials(_ context.Context, afterKind, afterID string, limit int) ([]state.AlertingSealedCredential, error) {
	var out []state.AlertingSealedCredential
	for _, c := range f.rows {
		if c.Kind > afterKind || (c.Kind == afterKind && c.ID > afterID) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *alertingValues) ResealAlertingCredential(_ context.Context, c state.AlertingSealedCredential, sealed []byte) (bool, error) {
	key := c.Kind + "|" + c.ID
	if key == f.rotate {
		f.rotate = ""
		return false, nil
	}
	if cur, ok := f.rows[key]; !ok || !bytes.Equal(cur.Sealed, c.Sealed) {
		return false, nil
	}
	f.rows[key] = state.AlertingSealedCredential{Kind: c.Kind, ID: c.ID, Sealed: sealed}
	return true, nil
}

func TestMigrateAlerting(t *testing.T) {
	domain, fleet, stranger := mustIdentity(t), mustIdentity(t), mustIdentity(t)
	seal := func(ns, plain string, to ...*age.X25519Identity) []byte {
		var rs []*age.X25519Recipient
		for _, id := range to {
			rs = append(rs, id.Recipient())
		}
		b, err := secretbox.SealBytesTo(rs, ns, []byte(plain), 0)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	pdKey := "0123456789abcdef0123456789abcdef"
	f := &alertingValues{rows: map[string]state.AlertingSealedCredential{}}
	for _, c := range []state.AlertingSealedCredential{
		{Kind: state.AlertingSealedCredentialRule, ID: "r1", Sealed: seal(alerts.AlertSecretNamespace, "whsec", fleet)},
		{Kind: state.AlertingSealedCredentialRule, ID: "r2", Sealed: seal("alert_rule", "legacy-whsec", fleet)},
		{Kind: state.AlertingSealedCredentialRule, ID: "r3", Sealed: seal(alerts.AlertSecretNamespace, "whsec", domain)},
		{Kind: state.AlertingSealedCredentialRule, ID: "r4", Sealed: seal(alerts.AlertSecretNamespace, "whsec", stranger)},
		{Kind: state.AlertingSealedCredentialChannel, ID: "c1", Sealed: seal(alertchannels.SealNamespace, "https://hooks.slack.com/x", fleet)},
		{Kind: state.AlertingSealedCredentialChannel, ID: "c2", Sealed: seal(alerts.AlertSecretNamespace, "wrong namespace", fleet)},
		{Kind: state.AlertingSealedCredentialMCPPagerDuty, ID: "p1", Sealed: seal(api.MCPReadinessPagerDutySecretNamespace, pdKey, domain, fleet)},
		{Kind: state.AlertingSealedCredentialMCPFallbackPagerDuty, ID: "p1", Sealed: seal(api.MCPReadinessPagerDutySecretNamespace, pdKey, fleet)},
		{Kind: state.AlertingSealedCredentialMCPFallbackWebhook, ID: "p2", Sealed: seal(alerts.AlertSecretNamespace, "fallback", fleet)},
	} {
		f.rows[c.Kind+"|"+c.ID] = c
	}
	f.rotate = state.AlertingSealedCredentialMCPFallbackWebhook + "|p2"

	report, err := sealmigrate.MigrateAlerting(context.Background(), f, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	// Re-sealed: r1, r2, c1, p1 (pagerduty, which the fleet key still opens)
	// and p1 (fallback). Current: r3. Changed: p2. Failed: r4 (another key)
	// and c2 (another namespace).
	want := sealmigrate.MigrationReport{Domain: sealdomain.Alerting, Target: domain.Recipient().String(),
		Scanned: 9, Resealed: 5, Current: 1, Changed: 1, Failed: 2}
	if report != want {
		t.Fatalf("report = %+v, want %+v", report, want)
	}
	for key, wantNS := range map[string]string{
		state.AlertingSealedCredentialRule + "|r1":                 alerts.AlertSecretNamespace,
		state.AlertingSealedCredentialRule + "|r2":                 "alert_rule",
		state.AlertingSealedCredentialChannel + "|c1":              alertchannels.SealNamespace,
		state.AlertingSealedCredentialMCPPagerDuty + "|p1":         api.MCPReadinessPagerDutySecretNamespace,
		state.AlertingSealedCredentialMCPFallbackPagerDuty + "|p1": api.MCPReadinessPagerDutySecretNamespace,
	} {
		blob := f.rows[key].Sealed
		if _, _, err := secretbox.OpenBytes(fleet, blob); err == nil {
			t.Errorf("%s still opens with the fleet identity", key)
		}
		if ns, _, err := secretbox.OpenBytes(domain, blob); err != nil || ns != wantNS {
			t.Errorf("%s = namespace %q, %v; want %q under the domain", key, ns, err, wantNS)
		}
	}

	again, err := sealmigrate.MigrateAlerting(context.Background(), f, domain, []*age.X25519Identity{fleet})
	if err != nil || again.Resealed != 1 || again.Changed != 0 || again.Failed != 2 {
		t.Fatalf("second run = %+v, %v; want the rotated row re-sealed", again, err)
	}
}
