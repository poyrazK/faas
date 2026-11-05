// adr: 570
package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// trafficEnvironmentDomainFixture binds api.example.test to a staging
// environment and lets a foreign wildcard, whose policy is oversized, cover it.
func trafficEnvironmentDomainFixture(t *testing.T, store Store, account, peerAccount Account, peer App, seed func(EdgeRule)) (Project, EdgeRule) {
	t.Helper()
	ctx := t.Context()
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "removal-env", ScanSource: ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	web, err := store.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, WorkloadName: "web", Slug: "removal-env-web", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	staging, err := store.CreateProjectEnvironment(ctx, ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.(interface {
		CreateCustomDomainInEnvironmentIfUnderQuota(context.Context, string, string, string, string, int, int) (CustomDomain, error)
	}).CreateCustomDomainInEnvironmentIfUnderQuota(ctx, "api.example.test", web.ID, staging.ID, "bound-token", 100, 500); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, "api.example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomain(ctx, "*.example.test", peer.ID, "fallback-token"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, "*.example.test"); err != nil {
		t.Fatal(err)
	}
	legacy := memTrafficRule(peerAccount, peer, "api.example.test", 520)
	rule := EdgeRule{ID: uuid.NewString(), AccountID: peerAccount.ID, AppID: peer.ID, MatchHost: "api.example.test", MatchPath: "/", Enabled: true, Kind: legacy.Kind, Action: legacy.Action}
	seed(rule)
	return project, rule
}

// Deleting an environment cascades its bound domains; the foreign wildcard's
// oversized policy would newly serve the host, so the delete is refused.
func testTrafficEnvironmentRemoval(t *testing.T, store Store, account, peerAccount Account, peer App, seed func(EdgeRule), intent func() string) {
	t.Helper()
	project, rule := trafficEnvironmentDomainFixture(t, store, account, peerAccount, peer, seed)
	before := intent()
	err := store.DeleteProjectEnvironment(t.Context(), account.ID, project.ID, "staging")
	var aggregate *TrafficPolicyAggregateError
	if !errors.As(err, &aggregate) || aggregate.Observed <= aggregate.Limit {
		t.Fatalf("environment deletion exposed an oversized fallback: %v", err)
	}
	if before != intent() {
		t.Fatal("refused environment deletion changed environment or domain intent")
	}
	if err := store.DeleteEdgeRule(t.Context(), rule.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProjectEnvironment(t.Context(), account.ID, project.ID, "staging"); err != nil {
		t.Fatalf("environment deletion after repair: %v", err)
	}
	if _, err := store.DomainByName(t.Context(), "api.example.test"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("environment deletion kept its bound domain: %v", err)
	}
}

// DNS drift records that the customer's DNS no longer points here. It always
// commits, like platform-tenant suspension, even when the withdrawn domain
// lets a foreign wildcard with an oversized policy serve the host.
func testTrafficDNSDriftWithdrawal(t *testing.T, store Store, account, peerAccount Account, peer App, seed func(EdgeRule)) {
	t.Helper()
	trafficEnvironmentDomainFixture(t, store, account, peerAccount, peer, seed)
	transitioned, err := store.(CustomDomainDNSDriftStore).MarkCustomDomainDNSDrifted(t.Context(), "api.example.test", time.Now().UTC(), "CNAME no longer targets the platform")
	if err != nil || !transitioned {
		t.Fatalf("DNS drift withdrawal was refused: transitioned=%v err=%v", transitioned, err)
	}
	if drifted, err := store.DomainByName(t.Context(), "api.example.test"); err != nil || !drifted.VerifiedAt.IsZero() {
		t.Fatalf("drifted domain kept its verification: %+v %v", drifted, err)
	}
}

func memTrafficEnvironmentPeer(t *testing.T, m *MemStore) (Account, App) {
	t.Helper()
	peerAccount, err := m.CreateAccount(t.Context(), "environment-removal-peer@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := m.CreateApp(t.Context(), App{AccountID: peerAccount.ID, Slug: "environment-removal-peer", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	return peerAccount, peer
}

func TestMemTrafficEnvironmentRemoval(t *testing.T) {
	m, account, _, _, _ := memTrafficFixture(t)
	peerAccount, peer := memTrafficEnvironmentPeer(t, m)
	testTrafficEnvironmentRemoval(t, m, account, peerAccount, peer, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() string {
		return trafficTenantIntentDigest(t, []any{memTrafficAppIntent(t, m), m.projectEnvironments, m.projectEnvironmentEdgePolicies})
	})
}

func TestMemTrafficDNSDriftWithdrawal(t *testing.T) {
	m, account, _, _, _ := memTrafficFixture(t)
	peerAccount, peer := memTrafficEnvironmentPeer(t, m)
	testTrafficDNSDriftWithdrawal(t, m, account, peerAccount, peer, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule })
}
