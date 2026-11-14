package secretd_test

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc/codes"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/secretd"
)

// counter returns the value of name{labels}, 0 when absent.
func counter(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, fam := range families {
		if fam.GetName() != name {
			continue
		}
	metric:
		for _, m := range fam.GetMetric() {
			for _, lp := range m.GetLabel() {
				if want, ok := labels[lp.GetName()]; ok && want != lp.GetValue() {
					continue metric
				}
			}
			return m.GetCounter().GetValue()
		}
	}
	return 0
}

// ADR-20261110210000: every refusal is counted by reason, so a node asking
// for tenants it does not run, or for another tenant's ciphertext, alerts.
func TestMetricsCountOpensAndRefusalsByReason(t *testing.T) {
	f := newFixture(t, 1)
	reg := prometheus.NewRegistry()
	m, err := secretd.NewMetrics(reg)
	if err != nil {
		t.Fatal(err)
	}
	f.server.WithMetrics(m)
	f.store.apps["app-c"] = secretd.AppTarget{OwnerNodeID: "n-owner", AccountID: "acct-a"}
	own := &secretdpb.SealedEntry{Key: "K", Ciphertext: f.seal(t, "acct-a", secretbox.Envelope{"K": "v"})}

	steps := []struct {
		name   string
		caller string
		app    string
		entry  *secretdpb.SealedEntry
		code   codes.Code
	}{
		{"owner opens", "owner-node", "app-a", own, codes.OK},
		{"another node's app", "owner-node", "app-b", own, codes.PermissionDenied},
		{"unknown node", "not-a-node", "app-a", own, codes.PermissionDenied},
		{"no identity", "", "app-a", own, codes.Unauthenticated},
		{"another tenant's envelope", "owner-node", "app-a",
			&secretdpb.SealedEntry{Key: "K", Ciphertext: f.seal(t, "acct-b", secretbox.Envelope{"K": "stolen"})}, codes.FailedPrecondition},
		{"over the budget", "owner-node", "app-c", own, codes.ResourceExhausted},
	}
	for _, s := range steps {
		f.caller = s.caller
		if _, code := f.open(t, s.app, s.entry); code != s.code {
			t.Fatalf("%s = %v, want %v", s.name, code, s.code)
		}
	}
	f.caller = "owner-node"
	f.server.RequireReferences(true)
	if _, code := f.open(t, "app-a", own); code != codes.FailedPrecondition {
		t.Fatalf("unreferenced = %v", code)
	}

	for _, tc := range []struct {
		name   string
		labels map[string]string
		want   float64
	}{
		{"faas_secretd_opens_total", map[string]string{"rpc": "OpenAppSecrets", "via": "owner"}, 1},
		{"faas_secretd_refusals_total", map[string]string{"rpc": "OpenAppSecrets", "reason": secretd.ReasonNotAuthorized}, 1},
		{"faas_secretd_refusals_total", map[string]string{"rpc": "OpenAppSecrets", "reason": secretd.ReasonUnknownNode}, 1},
		{"faas_secretd_refusals_total", map[string]string{"rpc": "OpenAppSecrets", "reason": secretd.ReasonUnauthenticated}, 1},
		{"faas_secretd_refusals_total", map[string]string{"rpc": "OpenAppSecrets", "reason": secretd.ReasonWrongAccount}, 1},
		{"faas_secretd_refusals_total", map[string]string{"rpc": "OpenAppSecrets", "reason": secretd.ReasonBudgetExhausted}, 1},
		{"faas_secretd_refusals_total", map[string]string{"rpc": "OpenAppSecrets", "reason": secretd.ReasonUnreferenced}, 1},
		{"faas_secretd_refusals_total", map[string]string{"rpc": "OpenAppSecrets", "reason": secretd.ReasonEnvelope}, 0},
	} {
		if got := counter(t, reg, tc.name, tc.labels); got != tc.want {
			t.Errorf("%s%v = %v, want %v", tc.name, tc.labels, got, tc.want)
		}
	}
	// Alerts take increase() over pre-created series: 8 RPCs x 8 reasons.
	if n, err := testutil.GatherAndCount(reg, "faas_secretd_refusals_total"); err != nil || n != 64 {
		t.Fatalf("refusal series = %d, %v", n, err)
	}
}
