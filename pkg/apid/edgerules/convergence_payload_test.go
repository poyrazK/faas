package edgerules

import (
	"fmt"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
)

// adr: 091 — amendment 2026-10-30 (round 5): a convergence payload carries the
// mutated rules' hosts, and NOTIFY refuses a payload over 7999 bytes. A
// rollback or list edit over a few hundred distinct hosts was refused for good
// ("edge-rule fleet convergence is unavailable"). Such a host list now
// collapses to "*", which fences and invalidates every host of the account.
func TestConvergencePayloadFitsNotifyWithManyHosts(t *testing.T) {
	var many []string
	for i := range 400 {
		many = append(many, fmt.Sprintf("tenant-%03d.customer-domain-example.com", i))
	}
	for _, tc := range []struct {
		name  string
		hosts []string
		want  []string
	}{
		{name: "few hosts kept", hosts: []string{"B.example.com", "a.example.com", "@app"}, want: []string{"@app", "a.example.com", "b.example.com"}},
		{name: "too many collapse", hosts: many, want: []string{"*"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv := &EdgeRuleConvergence{AppID: "app", AccountID: "acct", Operation: "rolled_back", Generation: 42,
				Hosts: boundedEdgeRuleHosts(canonicalEdgeRuleHosts(tc.hosts))}
			if !slices.Equal(conv.Hosts, tc.want) {
				t.Fatalf("hosts = %v, want %v", conv.Hosts, tc.want)
			}
			payload, err := conv.payload(ConvergencePhasePrepare)
			if err != nil {
				t.Fatal(err)
			}
			if len(payload) > db.NotifyPayloadMaxBytes {
				t.Fatalf("payload is %d bytes, over the NOTIFY limit %d", len(payload), db.NotifyPayloadMaxBytes)
			}
		})
	}
}
