// adr: 375
package state

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var trafficDomainPublicationForms = []string{"plain", "quota", "activity", "environment", "environment_activity"}

func createTrafficPublicationDomain(ctx context.Context, store Store, form, domain, app, environment string) (CustomDomain, int64, error) {
	entry := OrgActivity{OrgID: uuid.New(), Kind: "domain.created", ActorType: OrgActivityActorSystem,
		ActorLabel: "publication-test", ResourceType: "domain", ResourceID: domain, ResourceLabel: domain,
		SourceType: "domain.created", SourceID: "publication-" + form}
	switch form {
	case "plain":
		d, err := store.CreateCustomDomain(ctx, domain, app, "current-private-token")
		return d, 0, err
	case "quota":
		d, err := store.(interface {
			CreateCustomDomainIfUnderQuota(context.Context, string, string, string, int, int) (CustomDomain, error)
		}).CreateCustomDomainIfUnderQuota(ctx, domain, app, "current-private-token", 100, 500)
		return d, 0, err
	case "activity":
		return store.(OrgActivityDomainMutationStore).CreateCustomDomainIfUnderQuotaWithActivity(ctx, domain, app, "current-private-token", 100, 500, entry)
	case "environment":
		d, err := store.(interface {
			CreateCustomDomainInEnvironmentIfUnderQuota(context.Context, string, string, string, string, int, int) (CustomDomain, error)
		}).CreateCustomDomainInEnvironmentIfUnderQuota(ctx, domain, app, environment, "current-private-token", 100, 500)
		return d, 0, err
	default:
		return store.(interface {
			CreateCustomDomainInEnvironmentIfUnderQuotaWithActivity(context.Context, string, string, string, string, int, int, OrgActivity) (CustomDomain, int64, error)
		}).CreateCustomDomainInEnvironmentIfUnderQuotaWithActivity(ctx, domain, app, environment, "current-private-token", 100, 500, entry)
	}
}

func publicationLegacyRule(account Account, app App, host string) EdgeRule {
	intent := memTrafficRule(account, app, host, 520)
	return EdgeRule{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, MatchHost: host,
		MatchPath: "/", Enabled: true, Kind: intent.Kind, Action: intent.Action}
}

func testTrafficDomainPublicationShadowAndActivation(t *testing.T, store Store, account Account, app App, environment ProjectEnvironment, peerAccount Account, peer App, form string, seed func(EdgeRule), countOutbox func() int) {
	t.Helper()
	const host, fallback = "publication.example.test", "*.example.test"
	if _, err := store.CreateCustomDomain(t.Context(), fallback, peer.ID, "private-fallback-token"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(t.Context(), fallback); err != nil {
		t.Fatal(err)
	}
	// Existing foreign exposure is grandfathered. Publishing a pending exact
	// claim removes that exposure, so it must remain a valid repair operation.
	seed(publicationLegacyRule(peerAccount, peer, host))
	outboxBefore := countOutbox()
	claim, id, err := createTrafficPublicationDomain(t.Context(), store, form, host, app.ID, environment.ID)
	if err != nil || claim.Verified() || strings.Contains(form, "activity") != (id > 0) {
		t.Fatalf("safe shadow publication: verified=%v outbox=%d err=%v", claim.Verified(), id, err)
	}
	wantOutbox := outboxBefore
	if strings.Contains(form, "activity") {
		wantOutbox++
	}
	if countOutbox() != wantOutbox {
		t.Fatal("claim and activity publication were not atomic")
	}
	// The new owner cannot reuse the fallback owner's former allowance.
	legacy := publicationLegacyRule(account, app, host)
	seed(legacy)
	matched, err := store.(CustomDomainChallengeVerifier).MarkDomainVerifiedIfChallenge(t.Context(), host, claim.ChallengeToken)
	var aggregate *TrafficPolicyAggregateError
	if matched || !errors.As(err, &aggregate) || aggregate.Observed <= aggregate.Limit {
		t.Fatalf("new binding inherited foreign allowance: matched=%v err=%v", matched, err)
	}
	if after, err := store.DomainByName(t.Context(), host); err != nil || !reflect.DeepEqual(claim, after) || countOutbox() != wantOutbox {
		t.Fatalf("refused activation changed claim/activity: app=%q err=%v", after.AppID, err)
	}
	if err := store.DeleteEdgeRule(t.Context(), legacy.ID); err != nil {
		t.Fatal(err)
	}
	if matched, err := store.(CustomDomainChallengeVerifier).MarkDomainVerifiedIfChallenge(t.Context(), host, claim.ChallengeToken); err != nil || !matched {
		t.Fatalf("repaired activation: matched=%v err=%v", matched, err)
	}
}

func testTrafficDomainPublicationRetainsForeignShadow(t *testing.T, store Store, account Account, app App, peer App, mode string, seed func(EdgeRule)) {
	t.Helper()
	host, blocker := "nested.api.example.test", "nested.api.example.test"
	if strings.HasPrefix(mode, "wildcard") {
		blocker = "*.api.example.test"
	}
	if _, err := store.CreateCustomDomain(t.Context(), blocker, peer.ID, "blocking-private-token"); err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(mode, "verified") && !strings.HasSuffix(mode, "unverified") {
		if err := store.MarkDomainVerified(t.Context(), blocker); err != nil {
			t.Fatal(err)
		}
	}
	const fallback = "*.example.test"
	if _, err := store.CreateCustomDomain(t.Context(), fallback, app.ID, "current-private-token"); err != nil {
		t.Fatal(err)
	}
	legacy := publicationLegacyRule(account, app, host)
	seed(legacy)
	// Verification may publish the wildcard, but this foreign claim still
	// blocks the only overloaded hostname. Account-local domain projection
	// incorrectly treats the wildcard as selected here.
	if matched, err := store.(CustomDomainChallengeVerifier).MarkDomainVerifiedIfChallenge(t.Context(), fallback, "current-private-token"); err != nil || !matched {
		t.Fatalf("shadowed verification refused: matched=%v err=%v", matched, err)
	}
	var aggregate *TrafficPolicyAggregateError
	if err := store.(CustomDomainRemovalOwnerStore).DeleteCustomDomainForApp(t.Context(), blocker, peer.ID); !errors.As(err, &aggregate) {
		t.Fatalf("removal exposed the newly verified overloaded wildcard: %v", err)
	}
	if err := store.DeleteEdgeRule(t.Context(), legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.(CustomDomainRemovalOwnerStore).DeleteCustomDomainForApp(t.Context(), blocker, peer.ID); err != nil {
		t.Fatalf("repaired shadow removal: %v", err)
	}
}

func publicationMemPeer(t *testing.T, m *MemStore) (Account, App) {
	t.Helper()
	account, err := m.CreateAccount(t.Context(), "publication-peer@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(t.Context(), App{AccountID: account.ID, Slug: "publication-peer", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	return account, app
}

func TestMemTrafficDomainPublicationShadowAndActivation(t *testing.T) {
	for _, form := range trafficDomainPublicationForms {
		t.Run(form, func(t *testing.T) {
			m, account, _, app, environment := memTrafficFixture(t)
			peerAccount, peer := publicationMemPeer(t, m)
			testTrafficDomainPublicationShadowAndActivation(t, m, account, app, environment, peerAccount, peer, form,
				func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() int { return len(m.orgActivityOutbox) })
		})
	}
}

func TestMemTrafficDomainPublicationRetainsForeignShadow(t *testing.T) {
	for _, mode := range []string{"exact-verified", "exact-unverified", "wildcard-verified", "wildcard-unverified"} {
		t.Run(mode, func(t *testing.T) {
			m, account, _, app, _ := memTrafficFixture(t)
			_, peer := publicationMemPeer(t, m)
			testTrafficDomainPublicationRetainsForeignShadow(t, m, account, app, peer, mode, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule })
		})
	}
}

func TestMemTrafficDomainPublicationCancellation(t *testing.T) {
	for _, form := range trafficDomainPublicationForms {
		t.Run(form, func(t *testing.T) {
			m, _, _, app, environment := memTrafficFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			d, id, err := createTrafficPublicationDomain(ctx, m, form, "canceled.example.test", app.ID, environment.ID)
			if !errors.Is(err, context.Canceled) || d.Domain != "" || id != 0 || len(m.domains) != 0 || len(m.orgActivityOutbox) != 0 {
				t.Fatalf("canceled creation published intent: domain=%q outbox=%d err=%v", d.Domain, id, err)
			}
		})
	}
}
