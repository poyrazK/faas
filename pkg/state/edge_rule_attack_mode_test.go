package state_test

// ADR-20261021220456: the app's attack-mode rule (manifest_key
// "attack-mode") is outside the plan's rule quotas, and a rule-set rollback
// neither removes it nor brings an old one back. Both stores run the same
// steps.

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// attackModeTestStore is the slice of the store these steps use.
type attackModeTestStore interface {
	CreateEdgeRule(ctx context.Context, in state.CreateEdgeRuleParams) (state.EdgeRule, error)
	CreateEdgeRuleIfUnderQuota(ctx context.Context, in state.CreateEdgeRuleParams, limits api.Limits) (state.EdgeRule, error)
	DeleteEdgeRule(ctx context.Context, id string) error
	ListEdgeRuleSetVersions(ctx context.Context, appID string, limit int) ([]state.EdgeRuleSetVersion, error)
	RestoreEdgeRuleSetVersion(ctx context.Context, appID string, version int, limits api.Limits) (state.EdgeRuleSetRestore, error)
}

func attackModeRuleParams(accountID, appID string) state.CreateEdgeRuleParams {
	return state.CreateEdgeRuleParams{
		AccountID:   accountID,
		AppID:       appID,
		ManifestKey: state.EdgeRuleAttackModeKey,
		MatchHost:   "@app",
		MatchPath:   "/**",
		Priority:    0,
		Enabled:     true,
		Kind:        state.EdgeRuleKindChallenge,
		Action: state.EdgeRuleAction{
			Kind:      state.EdgeRuleKindChallenge,
			Challenge: &state.EdgeRuleChallengeAction{Difficulty: 16, PassTTLSeconds: 3600},
		},
		Name: "Attack mode",
	}
}

func challengeRuleParams(accountID, appID, host string) state.CreateEdgeRuleParams {
	p := attackModeRuleParams(accountID, appID)
	p.ManifestKey, p.MatchHost, p.Priority, p.Name = "", host, 100, ""
	return p
}

func latestEdgeRuleSetVersion(t *testing.T, s attackModeTestStore, ctx context.Context, appID string) int {
	t.Helper()
	versions, err := s.ListEdgeRuleSetVersions(ctx, appID, 1)
	if err != nil || len(versions) == 0 {
		t.Fatalf("versions = %v, %v", versions, err)
	}
	return versions[0].Version
}

func ruleIDs(rules []state.EdgeRule) []string {
	ids := make([]string, len(rules))
	for i, r := range rules {
		ids[i] = r.ID
	}
	slices.Sort(ids)
	return ids
}

func runAttackModeRuleStoreSteps(t *testing.T, s attackModeTestStore, ctx context.Context, acct, app string) {
	t.Helper()
	limits := api.MustLimitsFor(api.PlanPro)
	limits.EdgeRulesPerApp = 2
	limits.EdgeRulesChallengePerApp = 1

	route, err := s.CreateEdgeRuleIfUnderQuota(ctx, pgSampleEdgeRuleParams(acct, app, "am.example.com"), limits)
	if err != nil {
		t.Fatalf("create route rule: %v", err)
	}
	beforeAttack := latestEdgeRuleSetVersion(t, s, ctx, app)
	attack, err := s.CreateEdgeRule(ctx, attackModeRuleParams(acct, app))
	if err != nil {
		t.Fatalf("create attack-mode rule: %v", err)
	}
	if !attack.AttackMode() {
		t.Fatalf("attack-mode rule reads back with manifest key %q", attack.ManifestKey)
	}
	withAttack := latestEdgeRuleSetVersion(t, s, ctx, app)

	// Neither the app cap (2) nor the challenge cap (1) counts the
	// attack-mode rule, so one more challenge rule still fits.
	if _, err := s.CreateEdgeRuleIfUnderQuota(ctx, challengeRuleParams(acct, app, "am.example.com"), limits); err != nil {
		t.Fatalf("challenge rule beside attack mode: %v", err)
	}
	var qe *state.EdgeRuleQuotaError
	if _, err := s.CreateEdgeRuleIfUnderQuota(ctx, pgSampleEdgeRuleParams(acct, app, "am2.example.com"), limits); !errors.As(err, &qe) || qe.Observed != 2 {
		t.Fatalf("third counted rule err = %v, want quota error observing 2", err)
	}

	// A rollback to before attack mode keeps the current attack-mode rule.
	restored, err := s.RestoreEdgeRuleSetVersion(ctx, app, beforeAttack, limits)
	if err != nil {
		t.Fatalf("restore before attack mode: %v", err)
	}
	if got, want := ruleIDs(restored.Rules), ruleIDs([]state.EdgeRule{route, attack}); !slices.Equal(got, want) {
		t.Fatalf("restored rules = %v, want the route rule and the kept attack-mode rule %v", got, want)
	}

	// Once attack mode is off, a rollback to a version that had it does
	// not switch it back on.
	if err := s.DeleteEdgeRule(ctx, attack.ID); err != nil {
		t.Fatalf("delete attack-mode rule: %v", err)
	}
	restored, err = s.RestoreEdgeRuleSetVersion(ctx, app, withAttack, limits)
	if err != nil {
		t.Fatalf("restore version with attack mode: %v", err)
	}
	if got, want := ruleIDs(restored.Rules), []string{route.ID}; !slices.Equal(got, want) {
		t.Fatalf("restored rules = %v, want only the route rule %v", got, want)
	}
}

func TestMemStore_EdgeRuleAttackMode_OutsideQuotaAndRollback(t *testing.T) {
	m, ctx := state.NewMemStore(), context.Background()
	acct, app := memEdgeRuleSeedAccount(t, m, ctx, api.PlanPro, "attack-mode")
	runAttackModeRuleStoreSteps(t, m, ctx, acct, app)
}

func TestPgStore_EdgeRuleAttackMode_OutsideQuotaAndRollback(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "attack-mode")
	runAttackModeRuleStoreSteps(t, s, ctx, acct, app)
}
