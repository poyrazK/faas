package trafficguard_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgMCPAccountEdgePolicyHistory(t *testing.T) {
	s, pool, ctx := openMCPAccessStore(t)
	accountA, appA, _ := seedMCPAccessLive(t, s, ctx, "account-policy-a")
	otherAccount, err := s.CreateAccount(ctx, "account-policy-b@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := s.CreateApp(ctx, state.App{AccountID: otherAccount.ID, Slug: "account-policy-b", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	accountB, appB := otherAccount.ID, otherApp.ID
	insert := func(account, app string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO edge_rules(account_id,app_id,kind,match_host,match_path,action) VALUES($1,$2,'jwt','*.example','/**','{"kind":"jwt","jwt":{"issuer":"https://issuer.example","jwks_url":"https://issuer.example/jwks","algorithms":["RS256"]}}') RETURNING id`, account, app).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	position := func(account string) state.EdgeRulePolicyPosition {
		t.Helper()
		p, err := s.LatestAccountEdgeRulePolicyPosition(ctx, account)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	ruleA := insert(accountA, appA)
	before := position(accountA)
	insert(accountB, appB)
	if got := position(accountA); got != before {
		t.Fatalf("unrelated account blocked A: %+v -> %+v", before, got)
	}
	// A wildcard rule on a different app in A still advances A's revision.
	sibling, err := s.CreateApp(ctx, state.App{AccountID: accountA, Slug: "same-account-wildcard", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	siblingRule := insert(accountA, sibling.ID)
	if got := position(accountA); got.DesiredRevision <= before.DesiredRevision {
		t.Fatal("same-account wildcard skipped")
	}
	// Capture both owners on a transfer, then preserve ownership on deletion.
	if _, err := pool.Exec(ctx, `UPDATE edge_rules SET account_id=$1,app_id=$2 WHERE id=$3`, accountB, appB, ruleA); err != nil {
		t.Fatal(err)
	}
	transferA, transferB := position(accountA), position(accountB)
	if transferA.DesiredRevision != transferB.DesiredRevision {
		t.Fatalf("old owner transfer lost: %+v %+v", transferA, transferB)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM edge_rules WHERE id=$1`, ruleA); err != nil {
		t.Fatal(err)
	}
	deletedB := position(accountB)
	if deletedB.DesiredRevision <= transferB.DesiredRevision {
		t.Fatal("delete not recorded")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM edge_rules WHERE app_id=$1`, appB); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM apps WHERE id=$1`, appB); err != nil {
		t.Fatal(err)
	}
	afterCascade := position(accountB)
	if afterCascade.DesiredRevision < deletedB.DesiredRevision {
		t.Fatal("app deletion erased ownership")
	}
	if _, err := pool.Exec(ctx, `UPDATE edge_rule_change_log SET created_at=now()-interval '40 days'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneEdgeRuleChangeLog(ctx, time.Now().Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if position(accountA) != transferA || position(accountB) != afterCascade {
		t.Fatal("pruning erased account cursors")
	}
	// Pre-upgrade rows remain an unknown-owner floor, never guessed from apps.
	var floor int64
	if err := pool.QueryRow(ctx, `INSERT INTO edge_rule_change_log(app_id,rule_id,operation,created_at) VALUES($1,$2,'updated',now()-interval '40 days') RETURNING id`, appA, siblingRule).Scan(&floor); err != nil {
		t.Fatal(err)
	}
	if got := position(accountA); got.DesiredRevision != floor || got.LegacyFleetRevision != floor {
		t.Fatalf("legacy history not covered: %+v", got)
	}
	if got := position(accountB); got.DesiredRevision != floor {
		t.Fatal("unknown history must affect all accounts")
	}
	insert(accountA, appA)
	if got := position(accountB); got.DesiredRevision != floor {
		t.Fatal("new known unrelated mutation raised legacy floor")
	}
	if _, err := s.PruneEdgeRuleChangeLog(ctx, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := position(accountB); got.DesiredRevision != floor || got.LegacyFleetRevision != floor {
		t.Fatal("legacy floor pruned")
	}
}
