package state_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261101225122 — an organization's agent policy defaults to allowing
// every capability, and a sign-in's code remembers the organization chosen
// for it until the code is claimed.
func exerciseOrgAgents(t *testing.T, store contractTestStore) {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().Format("150405.000000")
	org, err := store.CreateOrg(ctx, state.Org{Slug: "agents-co-" + time.Now().Format("150405000000"), Name: "Agents Co", Plan: api.PlanPro})
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	policy, err := store.OrgAgentPolicy(ctx, org.ID)
	if err != nil || !policy.AgentsAllowed || !slices.Equal(policy.AllowedScopes, api.MCPGrantableScopes) {
		t.Fatalf("default policy = %+v, %v", policy, err)
	}
	set, err := store.SetOrgAgentPolicy(ctx, state.OrgAgentPolicy{OrgID: org.ID, AgentsAllowed: true, AllowedScopes: []string{api.ScopeDeployWrite}})
	if err != nil || !set.AgentsAllowed || !slices.Equal(set.AllowedScopes, []string{api.ScopeDeployWrite}) || set.UpdatedAt.IsZero() {
		t.Fatalf("SetOrgAgentPolicy = %+v, %v", set, err)
	}
	if _, err := store.SetOrgAgentPolicy(ctx, state.OrgAgentPolicy{OrgID: org.ID, AllowedScopes: []string{}}); err != nil {
		t.Fatalf("SetOrgAgentPolicy (agents off): %v", err)
	}
	if got, err := store.OrgAgentPolicy(ctx, org.ID); err != nil || got.AgentsAllowed || len(got.AllowedScopes) != 0 {
		t.Fatalf("policy after switching agents off = %+v, %v", got, err)
	}

	acct, err := store.CreateAccount(ctx, "org-agents-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	sum := sha256.Sum256([]byte("org-agents-code-" + suffix))
	if err := store.IssueCliAuthCode(ctx, sum[:], time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("IssueCliAuthCode: %v", err)
	}
	if got, err := store.CliAuthCodeOrg(ctx, sum[:]); err != nil || got != "" {
		t.Fatalf("org of a fresh code = %q, %v; want personal", got, err)
	}
	if err := store.SetCliAuthCodeOrg(ctx, sum[:], org.ID); err != nil {
		t.Fatalf("SetCliAuthCodeOrg: %v", err)
	}
	if err := store.ClaimCliAuthCode(ctx, sum[:], acct.ID); err != nil {
		t.Fatalf("ClaimCliAuthCode: %v", err)
	}
	if err := store.SetCliAuthCodeOrg(ctx, sum[:], ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rebinding a claimed code = %v, want ErrNotFound", err)
	}
	if status, account, err := store.ConsumeCliAuthCode(ctx, sum[:]); err != nil || status != api.CliAuthStatusConsumed || account != acct.ID {
		t.Fatalf("ConsumeCliAuthCode = %v, %q, %v", status, account, err)
	}
	if got, err := store.CliAuthCodeOrg(ctx, sum[:]); err != nil || got != org.ID {
		t.Fatalf("org of the consumed code = %q, %v; want %s", got, err, org.ID)
	}
}

func TestMemStoreOrgAgents(t *testing.T) {
	exerciseOrgAgents(t, state.NewMemStore())
}

func TestPgStoreOrgAgents(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseOrgAgents(t, state.NewPgStore(pool))
}
