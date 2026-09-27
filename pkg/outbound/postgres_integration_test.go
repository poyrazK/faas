package outbound_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// TestPostgresBackendSharesBudgetAcrossGatewayInstances is the production
// shape of the shared-budget contract: every handler has its own resolver and
// HTTP client, while all of them contend on the same Postgres admission row.
// It is skipped by pgtest when DATABASE_URL is unavailable.
func TestPostgresBackendSharesBudgetAcrossGatewayInstances(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, fmt.Sprintf("outbound-pg-%s@example.com", uuid.NewString()), api.PlanHobby)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "outbound-pg-" + uuid.NewString()[:8], RAMMB: 128})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}

	entered := make(chan struct{}, 5)
	finish := make(chan struct{})
	var providerCalls atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer provider-secret" {
			t.Errorf("provider Authorization = %q", got)
		}
		providerCalls.Add(1)
		entered <- struct{}{}
		<-finish
		w.WriteHeader(http.StatusNoContent)
	}))
	defer provider.Close()

	integration, err := outbound.NewIntegration(
		uuid.NewString(), provider.URL, "secret", []string{app.ID}, .001, 5, 20, time.Minute,
	)
	if err != nil {
		t.Fatalf("new integration: %v", err)
	}
	integration.ProviderAuthMode = outbound.ProviderAuthManaged
	integration.AllowedMethods = []string{http.MethodGet}
	integration.AllowedPathPrefixes = []string{"/v1/items"}
	if err := outbound.EnsureIntegration(ctx, pool, outbound.IntegrationRecord{
		AccountID: uuid.MustParse(account.ID), Name: "payments", Policy: integration,
	}); err != nil {
		t.Fatalf("ensure integration: %v", err)
	}
	resolver, err := outbound.NewPostgresResolver(pool)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := outbound.NewPostgresBackend(pool)
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "test-key", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	jwks, err := json.Marshal(signer.JWKS())
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := outbound.NewWorkloadIdentityVerifier(jwks, workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	assertion, err := signer.Mint(time.Now(), account.ID, app.ID, "instance-1", "gregale:outbound:"+integration.ID)
	if err != nil {
		t.Fatal(err)
	}
	handlers := make([]*outbound.Handler, 20)
	for i := range handlers {
		handlers[i], err = outbound.NewHandler(resolver, backend, provider.Client())
		if err != nil {
			t.Fatal(err)
		}
		if err := handlers[i].SetManagedAuthorizations(map[string]string{integration.ID: "Bearer provider-secret"}); err != nil {
			t.Fatal(err)
		}
		handlers[i].IdentityVerifier = verifier
	}

	responses := make(chan int, len(handlers))
	var wg sync.WaitGroup
	for _, handler := range handlers {
		wg.Add(1)
		go func(h *outbound.Handler) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "http://gateway.test/i/"+integration.ID+"/v1/items", nil)
			req.Header.Set(outbound.WorkloadIdentityHeader, assertion.AccessToken)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			responses <- rr.Code
		}(handler)
	}
	for i := 0; i < 5; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatalf("provider received %d/5 admitted requests", i)
		}
	}
	close(finish)
	wg.Wait()
	close(responses)

	var granted, rejected int
	for status := range responses {
		switch status {
		case http.StatusNoContent:
			granted++
		case http.StatusTooManyRequests:
			rejected++
		default:
			t.Errorf("unexpected handler status %d", status)
		}
	}
	if providerCalls.Load() != 5 || granted != 5 || rejected != 15 {
		t.Fatalf("shared postgres budget: provider_calls=%d granted=%d rejected=%d; want 5/5/15", providerCalls.Load(), granted, rejected)
	}
}

func TestPostgresOutboundRequestPolicyUpdatesApplyToAdmissions(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, fmt.Sprintf("outbound-policy-%s@example.com", uuid.NewString()), api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	defaultPolicy := api.DefaultOutboundRequestPolicy()
	offer, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: account.ID, Name: "request-policy",
		Origin: "https://api.example.com", AllowedMethods: []string{http.MethodGet},
		AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: outbound.CredentialSourceCustomerSealed, OwnerKind: outbound.IntegrationOwnerCustomer,
		RequestPolicy: defaultPolicy,
	})
	if err != nil {
		t.Fatalf("create customer integration: %v", err)
	}
	resolver, err := outbound.NewPostgresResolver(pool)
	if err != nil {
		t.Fatal(err)
	}
	before, err := resolver.Integration(ctx, offer.ID)
	if err != nil || before.RatePerSecond != defaultPolicy.RatePerSecond || before.Burst != defaultPolicy.Burst {
		t.Fatalf("initial resolved policy = %+v, %v", before, err)
	}
	updatedPolicy := api.OutboundRequestPolicy{RatePerSecond: 1, Burst: 1, MaxInFlight: 1, RequestTimeoutMS: 1500}
	if err := store.SetOutboundRequestPolicy(ctx, account.ID, offer.ID, updatedPolicy); err != nil {
		t.Fatalf("update customer policy: %v", err)
	}
	after, err := resolver.Integration(ctx, offer.ID)
	if err != nil || after.RatePerSecond != 1 || after.Burst != 1 || after.MaxInFlight != 1 || after.RequestTimeout != 1500*time.Millisecond {
		t.Fatalf("resolved updated policy = %+v, %v", after, err)
	}

	backend, err := outbound.NewPostgresBackend(pool)
	if err != nil {
		t.Fatal(err)
	}
	staleResolverSnapshot := outbound.AdmissionSpec{
		IntegrationID: offer.ID, RatePerSecond: defaultPolicy.RatePerSecond,
		Burst: defaultPolicy.Burst, MaxInFlight: defaultPolicy.MaxInFlight,
		LeaseTTL: time.Duration(defaultPolicy.RequestTimeoutMS) * time.Millisecond,
	}
	first, err := backend.Admit(ctx, staleResolverSnapshot)
	if err != nil || !first.Granted || first.RequestTimeout != 1500*time.Millisecond {
		t.Fatalf("admission after policy update = %+v, %v", first, err)
	}
	second, err := backend.Admit(ctx, staleResolverSnapshot)
	if err != nil || second.Granted || second.Reason != outbound.ReasonConcurrency || second.RequestTimeout != 1500*time.Millisecond {
		t.Fatalf("stale snapshot bypassed concurrency update: %+v, %v", second, err)
	}
	if err := backend.Release(ctx, offer.ID, first.LeaseID); err != nil {
		t.Fatal(err)
	}
	third, err := backend.Admit(ctx, staleResolverSnapshot)
	if err != nil || third.Granted || third.Reason != outbound.ReasonRate {
		t.Fatalf("stale snapshot bypassed rate update: %+v, %v", third, err)
	}
}

func TestPostgresCustomerSealedCredentialRotation(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "outbound-key-"+uuid.NewString()+"@example.com", api.PlanPro)
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
	if err := outbound.EnsureIntegration(ctx, pool, outbound.IntegrationRecord{AccountID: uuid.MustParse(account.ID), Name: "customer-key", Policy: integration}); err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	otherAccount, err := store.CreateAccount(ctx, "outbound-key-other-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := outbound.NewPostgresSealedCredentialResolver(pool, []*age.X25519Identity{identity}, []string{integration.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Authorization(ctx, uuid.NewString()); err == nil {
		t.Fatal("unconfigured integration credential was resolvable")
	}
	for _, value := range []string{"Bearer first", "Bearer rotated"} {
		sealed, err := secretbox.SealBytes(identity.Recipient(), outbound.ManagedAuthorizationSealNamespace, []byte(value), outbound.ManagedAuthorizationMaxBytes)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetOutboundCredential(ctx, otherAccount.ID, integration.ID, sealed); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("cross-account credential write error = %v", err)
		}
		if err := store.SetOutboundCredential(ctx, account.ID, integration.ID, sealed); err != nil {
			t.Fatal(err)
		}
		var stored []byte
		if err := pool.QueryRow(ctx, `SELECT authorization_sealed FROM outbound_integration_credentials WHERE integration_id = $1`, integration.ID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(stored, []byte(value)) {
			t.Fatal("provider credential stored in plaintext")
		}
		got, err := resolver.Authorization(ctx, integration.ID)
		if err != nil || got != value {
			t.Fatalf("resolved credential = %q, %v", got, err)
		}
	}
	if err := store.DeleteOutboundCredential(ctx, otherAccount.ID, integration.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account credential deletion error = %v", err)
	}
	if err := store.DeleteOutboundCredential(ctx, account.ID, integration.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Authorization(ctx, integration.ID); err == nil {
		t.Fatal("revoked credential was still resolvable")
	}
	sealed, err := secretbox.SealBytes(identity.Recipient(), outbound.ManagedAuthorizationSealNamespace, []byte("Bearer stale"), outbound.ManagedAuthorizationMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetOutboundCredential(ctx, account.ID, integration.ID, sealed); err != nil {
		t.Fatal(err)
	}
	integration.CredentialSource = outbound.CredentialSourceOperatorEnv
	if err := outbound.EnsureIntegration(ctx, pool, outbound.IntegrationRecord{AccountID: uuid.MustParse(account.ID), Name: "customer-key", Policy: integration}); err != nil {
		t.Fatal(err)
	}
	integration.CredentialSource = outbound.CredentialSourceCustomerSealed
	if err := outbound.EnsureIntegration(ctx, pool, outbound.IntegrationRecord{AccountID: uuid.MustParse(account.ID), Name: "customer-key", Policy: integration}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Authorization(ctx, integration.ID); err == nil {
		t.Fatal("credential revived after switching the source away and back")
	}
}

func TestCustomerBindingControlsManagedIntegrationAttachment(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "outbound-bind-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "outbound-bind-" + uuid.NewString()[:8], RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	integration, err := outbound.NewIntegration(uuid.NewString(), "https://api.example.com", "unused-token", nil, 10, 10, 10, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.ProviderAuthMode = outbound.ProviderAuthManaged
	integration.AllowedMethods = []string{http.MethodGet}
	integration.AllowedPathPrefixes = []string{"/v1/widgets"}
	if err := outbound.EnsureIntegration(ctx, pool, outbound.IntegrationRecord{AccountID: uuid.MustParse(account.ID), Name: "widgets", Policy: integration}); err != nil {
		t.Fatal(err)
	}
	resolver, err := outbound.NewPostgresResolver(pool)
	if err != nil {
		t.Fatal(err)
	}
	before, err := resolver.Integration(ctx, integration.ID)
	if err != nil || before.AllowsApp(app.ID) {
		t.Fatalf("before binding: allows=%t err=%v", before.AllowsApp(app.ID), err)
	}
	if _, err := store.BindOutboundIntegration(ctx, account.ID, app.ID, integration.ID); err != nil {
		t.Fatal(err)
	}
	otherAccount, err := store.CreateAccount(ctx, "outbound-other-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BindOutboundIntegration(ctx, otherAccount.ID, app.ID, integration.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account bind error = %v; want not found", err)
	}
	if err := outbound.EnsureIntegration(ctx, pool, outbound.IntegrationRecord{
		AccountID: uuid.MustParse(otherAccount.ID), Name: "widgets", Policy: integration,
	}); err == nil {
		t.Fatal("integration ownership transfer succeeded")
	}
	after, err := resolver.Integration(ctx, integration.ID)
	if err != nil || !after.AllowsApp(app.ID) || !after.AllowsRequest(http.MethodGet, "/v1/widgets") {
		t.Fatalf("after binding: allows=%t err=%v", after.AllowsApp(app.ID), err)
	}
	if err := store.UpdateOutboundBindingPolicy(ctx, account.ID, app.ID, integration.ID,
		[]string{http.MethodGet}, []string{"/v1/widgets/safe"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateOutboundBindingPolicy(ctx, account.ID, app.ID, integration.ID,
		[]string{http.MethodGet}, []string{"/v1/widgets-extra"}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("wider route policy error = %v; want invalid argument", err)
	}
	narrowed, err := resolver.Integration(ctx, integration.ID)
	if err != nil || !narrowed.AllowsAppRequest(app.ID, http.MethodGet, "/v1/widgets/safe/123") ||
		narrowed.AllowsAppRequest(app.ID, http.MethodGet, "/v1/widgets/unsafe") {
		t.Fatalf("gateway customer route policy = %+v, err=%v", narrowed.CustomerAppRoutes, err)
	}
	if err := store.UnbindOutboundIntegration(ctx, account.ID, app.ID, integration.ID); err != nil {
		t.Fatal(err)
	}
	removed, err := resolver.Integration(ctx, integration.ID)
	if err != nil || removed.AllowsApp(app.ID) {
		t.Fatalf("after unbinding: allows=%t err=%v", removed.AllowsApp(app.ID), err)
	}
}
