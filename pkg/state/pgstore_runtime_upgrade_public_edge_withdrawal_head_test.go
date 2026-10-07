package state_test

// adr: 615

import (
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgPublicWithdrawalDirectHeadRoundTripCannotBorrowOldZeroFacts(t *testing.T) {
	s, pool, gateway, original := publicEdgeFixture(t)
	for _, m := range original.Members {
		recordPublicActivity(t, s, m, state.RuntimeUpgradePublicEdgeActivity{Version: 1, Known: true})
		if err := s.RecordRuntimeUpgradePublicEdgeGuard(t.Context(), m); err != nil {
			t.Fatal(err)
		}
	}
	if got := observePublicCoverage(t, s, original); got.Status != "coverage_observed" {
		t.Fatal(got)
	}
	next := uuid.NewString()
	if _, err := pool.Exec(t.Context(), `INSERT INTO runtime_upgrade_public_edge_rosters(revision,gateway_roster_revision,topology_sha256,slot_ids,public_sessions,config_sha256s) SELECT $1,gateway_roster_revision,repeat('d',64),slot_ids,public_sessions,config_sha256s FROM runtime_upgrade_public_edge_rosters WHERE revision=$2`, next, original.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_public_edge_roster_head SET revision=$1`, next); err != nil {
		t.Fatal(err)
	}
	tk := ingress.NewActivityTracker()
	a, _ := tk.Begin()
	b, err := s.AuthorizeRuntimeUpgradePublicEdgeIngress(t.Context(), original.Members[0], gateway.Members[0].SlotID, gateway.Members[0].SessionID)
	if err != nil || b.PublicRevision != next {
		t.Fatal(b, err)
	}
	if err := a.Bind(ingress.Generation{PublicRevision: b.PublicRevision, GatewayRevision: b.GatewayRevision}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_public_edge_roster_head SET revision=$1`, original.Revision); err != nil {
		t.Fatal(err)
	}
	var facts int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM runtime_upgrade_public_edge_guards)+(SELECT count(*) FROM runtime_upgrade_public_edge_activity)`).Scan(&facts); err != nil || facts != 0 {
		t.Fatal("head round trip retained stale facts", facts, err)
	}
	if got := observePublicCoverage(t, s, original); got.Status != "pending" || got.ConfirmedEdges != 0 || len(got.PendingWithdrawals) != 0 {
		t.Fatal("older zero hid intermediate generation", got)
	}
	if err := s.RecordRuntimeUpgradePublicEdgeActivity(t.Context(), original.Members[0], trackerPublicSnapshot(tk)); err != nil {
		t.Fatal(err)
	}
	recordPublicActivity(t, s, original.Members[1], state.RuntimeUpgradePublicEdgeActivity{Version: 1, Known: true})
	if got := observePublicCoverage(t, s, original); got.Status != "pending" || got.Previous != 1 {
		t.Fatal("fresh snapshot lost intermediate work", got)
	}
	a.Finish()
	if err := s.RecordRuntimeUpgradePublicEdgeActivity(t.Context(), original.Members[0], trackerPublicSnapshot(tk)); err != nil {
		t.Fatal(err)
	}
	if got := observePublicCoverage(t, s, original); got.Status != "coverage_observed" {
		t.Fatal("completed intermediate work did not recover", got)
	}
}
