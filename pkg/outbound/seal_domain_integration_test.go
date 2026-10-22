package outbound_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPostgresOutboundSealDomainRollout walks the outbound seal domain (ADR
// 20261022111812) through its steps against PostgreSQL: a dual-sealed
// credential opens for outboundd on either release, and after the re-seal
// only the domain identity opens it.
func TestPostgresOutboundSealDomainRollout(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "outbound-domain-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	integration, err := outbound.NewIntegration(uuid.NewString(), "https://api.example.com", "unused-token", nil, 10, 10, 10, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.ProviderAuthMode = outbound.ProviderAuthManaged
	integration.CredentialSource = outbound.CredentialSourceCustomerSealed
	integration.AllowedMethods = []string{http.MethodGet}
	integration.AllowedPathPrefixes = []string{"/v1"}
	if err := outbound.EnsureIntegration(ctx, pool, outbound.IntegrationRecord{AccountID: uuid.MustParse(account.ID), Name: "domain-key", Policy: integration}); err != nil {
		t.Fatal(err)
	}
	domain, fleet := mustAgeIdentity(t), mustAgeIdentity(t)
	sealed, err := secretbox.SealBytesTo([]*age.X25519Recipient{domain.Recipient(), fleet.Recipient()}, outbound.ManagedAuthorizationSealNamespace, []byte("Bearer dual"), outbound.ManagedAuthorizationMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetOutboundCredential(ctx, account.ID, integration.ID, sealed); err != nil {
		t.Fatal(err)
	}
	resolve := func(ids ...*age.X25519Identity) (string, error) {
		r, err := outbound.NewPostgresSealedCredentialResolver(pool, ids, []string{integration.ID})
		if err != nil {
			t.Fatal(err)
		}
		return r.Authorization(ctx, integration.ID)
	}
	for name, id := range map[string]*age.X25519Identity{"previous release (fleet only)": fleet, "this release (domain)": domain} {
		if got, err := resolve(id); err != nil || got != "Bearer dual" {
			t.Fatalf("%s: dual-sealed credential = %q, %v", name, got, err)
		}
	}

	report, err := sealmigrate.MigrateOutbound(ctx, store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	if report.Resealed < 1 || report.Failed != 0 {
		t.Fatalf("migrate report = %+v", report)
	}
	if got, err := resolve(domain); err != nil || got != "Bearer dual" {
		t.Fatalf("domain identity after re-seal = %q, %v", got, err)
	}
	if _, err := resolve(fleet); err == nil {
		t.Fatal("the fleet identity still opens a credential re-sealed to the outbound domain")
	}
	stale := state.OutboundSealedCredential{Kind: state.OutboundSealedCredentialPrimary, ID: integration.ID, Sealed: sealed}
	if replaced, err := store.ResealOutboundCredential(ctx, stale, sealed); err != nil || replaced {
		t.Fatalf("compare-and-swap against an old ciphertext = %v, %v; want no write", replaced, err)
	}
}

func mustAgeIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
