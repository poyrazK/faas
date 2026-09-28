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
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer provider.Close()

	integration, err := outbound.NewIntegration(
		uuid.NewString(), provider.URL, "secret", []string{app.ID}, .001, 5, 20, time.Minute,
	)
	if err != nil {
		t.Fatalf("new integration: %v", err)
	}
	integration.MaxRetries = 1
	integration.RetryBudgetPerMinute = 1
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
	resolvedIntegration, err := resolver.Integration(ctx, integration.ID)
	if err != nil {
		t.Fatalf("resolve persisted integration: %v", err)
	}
	if resolvedIntegration.AccountID != account.ID || resolvedIntegration.Name != "payments" {
		t.Fatalf("resolved integration identity = account %q name %q, want account %q name payments",
			resolvedIntegration.AccountID, resolvedIntegration.Name, account.ID)
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
		case http.StatusServiceUnavailable:
			granted++
		case http.StatusTooManyRequests:
			rejected++
		default:
			t.Errorf("unexpected handler status %d", status)
		}
	}
	if providerCalls.Load() != 6 || granted != 5 || rejected != 15 {
		t.Fatalf("shared postgres budgets: provider_calls=%d granted=%d rejected=%d; want 6/5/15 (five first attempts plus one fleet-wide retry)", providerCalls.Load(), granted, rejected)
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
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "outbound-policy-" + uuid.NewString()[:8], RAMMB: 128})
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
	if _, err := store.BindOutboundIntegration(ctx, account.ID, app.ID, offer.ID); err != nil {
		t.Fatalf("bind customer integration: %v", err)
	}
	resolver, err := outbound.NewPostgresResolver(pool)
	if err != nil {
		t.Fatal(err)
	}
	before, err := resolver.Integration(ctx, offer.ID)
	if err != nil || before.RatePerSecond != defaultPolicy.RatePerSecond || before.Burst != defaultPolicy.Burst {
		t.Fatalf("initial resolved policy = %+v, %v", before, err)
	}
	updatedPolicy := api.OutboundRequestPolicy{
		RatePerSecond: 1, Burst: 1, MaxInFlight: 1, RequestTimeoutMS: 1500,
		MaxRetries: 2, ResponseCacheTTLSeconds: 60,
		CircuitBreakerFailureThreshold: 3, CircuitBreakerOpenSeconds: 30,
		RetryBudgetPerMinute: 7,
	}
	if err := store.SetOutboundRequestPolicy(ctx, account.ID, offer.ID, updatedPolicy); err != nil {
		t.Fatalf("update customer policy: %v", err)
	}
	after, err := resolver.Integration(ctx, offer.ID)
	if err != nil || after.RatePerSecond != 1 || after.Burst != 1 || after.MaxInFlight != 1 || after.RequestTimeout != 1500*time.Millisecond || after.MaxRetries != 2 || after.ResponseCacheTTLSeconds != 60 || after.CircuitBreakerFailureThreshold != 3 || after.CircuitBreakerOpenSeconds != 30 || after.RetryBudgetPerMinute != 7 {
		t.Fatalf("resolved updated policy = %+v, %v", after, err)
	}

	backend, err := outbound.NewPostgresBackend(pool)
	if err != nil {
		t.Fatal(err)
	}
	staleResolverSnapshot := outbound.AdmissionSpec{
		IntegrationID: offer.ID, RatePerSecond: defaultPolicy.RatePerSecond,
		Burst: defaultPolicy.Burst, MaxInFlight: defaultPolicy.MaxInFlight,
		BindingAppID: app.ID,
		LeaseTTL:     time.Duration(defaultPolicy.RequestTimeoutMS) * time.Millisecond,
	}
	first, err := backend.Admit(ctx, staleResolverSnapshot)
	if err != nil || !first.Granted || first.RequestTimeout != 1500*time.Millisecond || first.RetryBudgetPerMinute != 7 {
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

func TestPostgresRetryBudgetCoordinatesGatewayInstances(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, fmt.Sprintf("outbound-retry-budget-%s@example.com", uuid.NewString()), api.PlanPro)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "outbound-retry-budget-" + uuid.NewString()[:8], RAMMB: 128})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	policy := api.DefaultOutboundRequestPolicy()
	policy.RetryBudgetPerMinute = 1
	offer, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: account.ID, Name: "retry-budget",
		Origin: "https://api.example.com", AllowedMethods: []string{http.MethodGet},
		AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: outbound.CredentialSourceCustomerSealed, OwnerKind: outbound.IntegrationOwnerCustomer,
		RequestPolicy: policy,
	})
	if err != nil {
		t.Fatalf("create customer integration: %v", err)
	}
	if _, err := store.BindOutboundIntegration(ctx, account.ID, app.ID, offer.ID); err != nil {
		t.Fatalf("bind integration: %v", err)
	}
	backendA, err := outbound.NewPostgresBackend(pool)
	if err != nil {
		t.Fatal(err)
	}
	backendB, err := outbound.NewPostgresBackend(pool)
	if err != nil {
		t.Fatal(err)
	}
	spec := outbound.AdmissionSpec{
		IntegrationID: offer.ID, RatePerSecond: 100, Burst: 20, MaxInFlight: 20,
		BindingAppID: app.ID, RetryBudgetPerMinute: policy.RetryBudgetPerMinute,
		LeaseTTL: time.Minute,
	}
	decision, err := backendA.Admit(ctx, spec)
	if err != nil || !decision.Granted || decision.RetryBudgetPerMinute != 1 {
		t.Fatalf("admission = %+v, %v", decision, err)
	}
	type result struct {
		allowed bool
		err     error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, backend := range []*outbound.PostgresBackend{backendA, backendB} {
		wg.Add(1)
		go func(backend *outbound.PostgresBackend) {
			defer wg.Done()
			allowed, err := backend.ConsumeRetryToken(ctx, offer.ID, policy.RetryBudgetPerMinute)
			results <- result{allowed: allowed, err: err}
		}(backend)
	}
	wg.Wait()
	close(results)
	var consumed int
	for got := range results {
		if got.err != nil {
			t.Errorf("consume retry token: %v", got.err)
		}
		if got.allowed {
			consumed++
		}
	}
	if consumed != 1 {
		t.Fatalf("independent gateway backends consumed %d retry tokens, want exactly 1", consumed)
	}
}

func TestPostgresProviderCooldownIsSharedAndNeverShortened(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, fmt.Sprintf("outbound-provider-cooldown-%s@example.com", uuid.NewString()), api.PlanPro)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "outbound-cooldown-" + uuid.NewString()[:8], RAMMB: 128})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	offer, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: account.ID, Name: "provider-cooldown",
		Origin: "https://api.example.com", AllowedMethods: []string{http.MethodGet},
		AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: outbound.CredentialSourceCustomerSealed, OwnerKind: outbound.IntegrationOwnerCustomer,
		RequestPolicy: api.DefaultOutboundRequestPolicy(),
	})
	if err != nil {
		t.Fatalf("create integration: %v", err)
	}
	if _, err := store.BindOutboundIntegration(ctx, account.ID, app.ID, offer.ID); err != nil {
		t.Fatalf("bind integration: %v", err)
	}
	backendA, err := outbound.NewPostgresBackend(pool)
	if err != nil {
		t.Fatal(err)
	}
	backendB, err := outbound.NewPostgresBackend(pool)
	if err != nil {
		t.Fatal(err)
	}
	spec := outbound.AdmissionSpec{
		IntegrationID: offer.ID, RatePerSecond: 100, Burst: 20, MaxInFlight: 20,
		BindingAppID: app.ID, LeaseTTL: time.Minute,
	}
	decision, err := backendA.Admit(ctx, spec)
	if err != nil || !decision.Granted {
		t.Fatalf("admission = %+v, %v", decision, err)
	}
	policyRevision := int64(42)
	if err := backendA.RecordProviderCooldown(ctx, offer.ID, policyRevision, 20*time.Second); err != nil {
		t.Fatalf("record provider cooldown: %v", err)
	}
	if err := backendB.RecordProviderCooldown(ctx, offer.ID, policyRevision, time.Second); err != nil {
		t.Fatalf("record shorter provider cooldown: %v", err)
	}
	gate, err := backendB.AllowProviderRequest(ctx, offer.ID, policyRevision)
	if err != nil || gate.Allowed || gate.RetryAfter < 15*time.Second || gate.RetryAfter > 20*time.Second {
		t.Fatalf("shared provider cooldown = %+v, %v; want a remaining duration near 20 seconds", gate, err)
	}
	updatedPolicy, err := backendB.AllowProviderRequest(ctx, offer.ID, policyRevision+1)
	if err != nil || !updatedPolicy.Allowed {
		t.Fatalf("provider gate after policy revision change = %+v, %v; want allowed", updatedPolicy, err)
	}
}

func TestPostgresCircuitBreakerCoordinatesOneHalfOpenProbe(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, fmt.Sprintf("outbound-circuit-%s@example.com", uuid.NewString()), api.PlanPro)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "outbound-circuit-" + uuid.NewString()[:8], RAMMB: 128})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	policy := api.DefaultOutboundRequestPolicy()
	// Customer admissions use the persisted request timeout as their lease TTL.
	// Keep this short so the crash-expiry half-open probe scenario can reclaim it.
	policy.RequestTimeoutMS = 250
	policy.CircuitBreakerFailureThreshold = 1
	policy.CircuitBreakerOpenSeconds = 1
	offer, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: account.ID, Name: "circuit-breaker",
		Origin: "https://api.example.com", AllowedMethods: []string{http.MethodGet},
		AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: outbound.CredentialSourceCustomerSealed, OwnerKind: outbound.IntegrationOwnerCustomer,
		RequestPolicy: policy,
	})
	if err != nil {
		t.Fatalf("create customer integration: %v", err)
	}
	if _, err := store.BindOutboundIntegration(ctx, account.ID, app.ID, offer.ID); err != nil {
		t.Fatalf("bind customer integration: %v", err)
	}
	backendA, err := outbound.NewPostgresBackend(pool)
	if err != nil {
		t.Fatal(err)
	}
	backendB, err := outbound.NewPostgresBackend(pool)
	if err != nil {
		t.Fatal(err)
	}
	spec := outbound.AdmissionSpec{
		IntegrationID: offer.ID, RatePerSecond: 100, Burst: 20, MaxInFlight: 20,
		BindingAppID: app.ID, CircuitBreakerFailureThreshold: 1, CircuitBreakerOpenSeconds: 1,
		LeaseTTL: time.Second,
	}

	first, err := backendA.Admit(ctx, spec)
	if err != nil || !first.Granted || first.CircuitBreakerFailureThreshold != 1 || first.CircuitBreakerOpenSeconds != 1 {
		t.Fatalf("initial circuit admission = %+v, %v", first, err)
	}
	gate, err := backendA.AllowCircuit(ctx, offer.ID, first.LeaseID, 1, 1)
	if err != nil || !gate.Allowed || gate.Probe {
		t.Fatalf("initial circuit gate = %+v, %v", gate, err)
	}
	if err := backendA.RecordCircuitOutcome(ctx, offer.ID, first.LeaseID, 1, 1, outbound.CircuitOutcomeFailure); err != nil {
		t.Fatalf("trip circuit: %v", err)
	}
	if err := backendA.Release(ctx, offer.ID, first.LeaseID); err != nil {
		t.Fatal(err)
	}

	openLease, err := backendB.Admit(ctx, spec)
	if err != nil || !openLease.Granted {
		t.Fatalf("open-state admission = %+v, %v", openLease, err)
	}
	openGate, err := backendB.AllowCircuit(ctx, offer.ID, openLease.LeaseID, 1, 1)
	if err != nil || openGate.Allowed || openGate.RetryAfter <= 0 {
		t.Fatalf("open-state gate = %+v, %v", openGate, err)
	}
	if err := backendB.Release(ctx, offer.ID, openLease.LeaseID); err != nil {
		t.Fatal(err)
	}

	time.Sleep(1100 * time.Millisecond)
	probeLease, err := backendA.Admit(ctx, spec)
	if err != nil || !probeLease.Granted {
		t.Fatalf("half-open admission = %+v, %v", probeLease, err)
	}
	probeGate, err := backendA.AllowCircuit(ctx, offer.ID, probeLease.LeaseID, 1, 1)
	if err != nil || !probeGate.Allowed || !probeGate.Probe {
		t.Fatalf("half-open gate = %+v, %v", probeGate, err)
	}

	competitor, err := backendB.Admit(ctx, spec)
	if err != nil || !competitor.Granted {
		t.Fatalf("competing admission = %+v, %v", competitor, err)
	}
	competitorGate, err := backendB.AllowCircuit(ctx, offer.ID, competitor.LeaseID, 1, 1)
	if err != nil || competitorGate.Allowed || competitorGate.Probe || competitorGate.RetryAfter <= 0 {
		t.Fatalf("competing half-open gate = %+v, %v", competitorGate, err)
	}
	if err := backendB.Release(ctx, offer.ID, competitor.LeaseID); err != nil {
		t.Fatal(err)
	}

	// The first probe's admission lease expires without a recorded outcome,
	// simulating a crashed gateway replica. A later admission must reclaim the
	// probe without writing the stale lease foreign key back to the state row.
	time.Sleep(1100 * time.Millisecond)
	replacementProbe, err := backendB.Admit(ctx, spec)
	if err != nil || !replacementProbe.Granted {
		t.Fatalf("replacement probe admission after expiry = %+v, %v", replacementProbe, err)
	}
	replacementGate, err := backendB.AllowCircuit(ctx, offer.ID, replacementProbe.LeaseID, 1, 1)
	if err != nil || !replacementGate.Allowed || !replacementGate.Probe {
		t.Fatalf("replacement half-open gate = %+v, %v", replacementGate, err)
	}
	if err := backendB.RecordCircuitOutcome(ctx, offer.ID, replacementProbe.LeaseID, 1, 1, outbound.CircuitOutcomeSuccess); err != nil {
		t.Fatalf("close circuit after successful probe: %v", err)
	}
	if err := backendB.Release(ctx, offer.ID, replacementProbe.LeaseID); err != nil {
		t.Fatal(err)
	}

	closedLease, err := backendB.Admit(ctx, spec)
	if err != nil || !closedLease.Granted {
		t.Fatalf("post-probe admission = %+v, %v", closedLease, err)
	}
	closedGate, err := backendB.AllowCircuit(ctx, offer.ID, closedLease.LeaseID, 1, 1)
	if err != nil || !closedGate.Allowed || closedGate.Probe {
		t.Fatalf("successful probe did not close shared circuit: %+v, %v", closedGate, err)
	}
}

func TestPostgresBackendEnforcesDailyLimitPerBinding(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, fmt.Sprintf("outbound-binding-budget-%s@example.com", uuid.NewString()), api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	appOne, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "binding-budget-a-" + uuid.NewString()[:8], RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	appTwo, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "binding-budget-b-" + uuid.NewString()[:8], RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: account.ID, Name: "binding-budget",
		Origin: "https://api.example.com", AllowedMethods: []string{http.MethodGet},
		AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: outbound.CredentialSourceCustomerSealed, OwnerKind: outbound.IntegrationOwnerCustomer,
		RequestPolicy: api.OutboundRequestPolicy{RatePerSecond: 100, Burst: 20, MaxInFlight: 20, RequestTimeoutMS: 30_000},
	})
	if err != nil {
		t.Fatalf("create customer integration: %v", err)
	}
	for _, app := range []state.App{appOne, appTwo} {
		if _, err := store.BindOutboundIntegration(ctx, account.ID, app.ID, offer.ID); err != nil {
			t.Fatalf("bind app %s: %v", app.ID, err)
		}
	}
	appOneLimit, appTwoLimit := int64(3), int64(2)
	if err := store.SetOutboundBindingDailyRequestLimit(ctx, account.ID, appOne.ID, offer.ID, &appOneLimit); err != nil {
		t.Fatal(err)
	}
	if err := store.SetOutboundBindingDailyRequestLimit(ctx, account.ID, appTwo.ID, offer.ID, &appTwoLimit); err != nil {
		t.Fatal(err)
	}
	resolver, err := outbound.NewPostgresResolver(pool)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Integration(ctx, offer.ID)
	if err != nil || !resolved.AllowsApp(appOne.ID) || !resolved.AllowsApp(appTwo.ID) ||
		len(resolved.BindingAppIDs) != 2 ||
		resolved.BindingDailyRequestLimits[appOne.ID] == nil || *resolved.BindingDailyRequestLimits[appOne.ID] != appOneLimit ||
		resolved.BindingDailyRequestLimits[appTwo.ID] == nil || *resolved.BindingDailyRequestLimits[appTwo.ID] != appTwoLimit {
		t.Fatalf("resolved binding daily limits = %+v, %v", resolved.BindingDailyRequestLimits, err)
	}
	backend, err := outbound.NewPostgresBackend(pool)
	if err != nil {
		t.Fatal(err)
	}
	// A stale resolver snapshot advertises a higher cap; admission must use
	// the locked binding row so concurrent gateways cannot bypass the write.
	staleLimit := int64(20)
	baseSpec := outbound.AdmissionSpec{
		IntegrationID: offer.ID, RatePerSecond: 100, Burst: 20, MaxInFlight: 20,
		BindingDailyRequestLimit: &staleLimit, LeaseTTL: time.Minute,
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var appOneGranted int
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			spec := baseSpec
			spec.BindingAppID = appOne.ID
			decision, err := backend.Admit(ctx, spec)
			if err != nil {
				t.Errorf("app one admission: %v", err)
				return
			}
			if decision.Granted {
				mu.Lock()
				appOneGranted++
				mu.Unlock()
				if err := backend.Release(ctx, offer.ID, decision.LeaseID); err != nil {
					t.Errorf("release app one admission: %v", err)
				}
			} else if decision.Reason != outbound.ReasonDailyLimit {
				t.Errorf("app one rejection reason = %q, want daily limit", decision.Reason)
			}
		}()
	}
	wg.Wait()
	if appOneGranted != int(appOneLimit) {
		t.Fatalf("app one grants = %d, want %d", appOneGranted, appOneLimit)
	}
	for range 3 {
		spec := baseSpec
		spec.BindingAppID = appTwo.ID
		decision, err := backend.Admit(ctx, spec)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Granted {
			if err := backend.Release(ctx, offer.ID, decision.LeaseID); err != nil {
				t.Fatal(err)
			}
		} else if decision.Reason != outbound.ReasonDailyLimit {
			t.Fatalf("app two rejection reason = %q, want daily limit", decision.Reason)
		}
	}
	usageOne, err := store.GetOutboundBindingUsage(ctx, account.ID, appOne.ID, offer.ID)
	if err != nil || usageOne.DailyRequestCount != appOneLimit {
		t.Fatalf("app one binding usage = %+v, %v", usageOne, err)
	}
	usageTwo, err := store.GetOutboundBindingUsage(ctx, account.ID, appTwo.ID, offer.ID)
	if err != nil || usageTwo.DailyRequestCount != appTwoLimit {
		t.Fatalf("app two binding usage = %+v, %v", usageTwo, err)
	}
	if err := store.UnbindOutboundIntegration(ctx, account.ID, appOne.ID, offer.ID); err != nil {
		t.Fatal(err)
	}
	staleSpec := baseSpec
	staleSpec.BindingAppID = appOne.ID
	if decision, err := backend.Admit(ctx, staleSpec); err != nil || decision.Granted || decision.Reason != outbound.ReasonAppNotAttached {
		t.Fatalf("admission after stale unbind = %+v, %v", decision, err)
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

// TestPostgresGatewayKeepsManagedCredentialBehindIdentityAndRouteChecks joins
// the database-backed policy resolver, workload identity verifier, sealed
// credential resolver, admission backend, and outbound handler in one request
// path. It is intentionally an explicit-gateway integration test; it does not
// claim transparent interception of guest egress.
func TestPostgresGatewayKeepsManagedCredentialBehindIdentityAndRouteChecks(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "outbound-boundary-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "outbound-boundary-" + uuid.NewString()[:8], RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	unboundApp, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "outbound-unbound-" + uuid.NewString()[:8], RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}

	const providerAuthorization = "Bearer integration-test-secret"
	var providerCalls atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		if got := r.Header.Get("Authorization"); got != providerAuthorization {
			t.Errorf("provider Authorization = %q, want configured credential", got)
		}
		for _, header := range []string{outbound.WorkloadIdentityHeader, outbound.TokenHeader, outbound.AppHeader} {
			if got := r.Header.Get(header); got != "" {
				t.Errorf("internal header %s reached provider", header)
			}
		}
		if r.URL.Path != "/v1/widgets/safe/item-1" {
			t.Errorf("provider path = %q", r.URL.Path)
		}
		w.Header().Set("X-Provider-Result", "accepted")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer provider.Close()

	integrationID := uuid.NewString()
	integration, err := outbound.NewIntegration(integrationID, provider.URL, "unused-test-token", nil, 10, 1, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.ProviderAuthMode = outbound.ProviderAuthManaged
	integration.CredentialSource = outbound.CredentialSourceCustomerSealed
	integration.AllowedMethods = []string{http.MethodGet}
	integration.AllowedPathPrefixes = []string{"/v1/widgets"}
	if err := outbound.EnsureIntegration(ctx, pool, outbound.IntegrationRecord{
		AccountID: uuid.MustParse(account.ID), Name: "boundary-test", Policy: integration,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BindOutboundIntegration(ctx, account.ID, app.ID, integrationID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateOutboundBindingPolicy(ctx, account.ID, app.ID, integrationID,
		[]string{http.MethodGet}, []string{"/v1/widgets/safe"}); err != nil {
		t.Fatal(err)
	}

	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := secretbox.SealBytes(identity.Recipient(), outbound.ManagedAuthorizationSealNamespace,
		[]byte(providerAuthorization), outbound.ManagedAuthorizationMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetOutboundCredential(ctx, account.ID, integrationID, sealed); err != nil {
		t.Fatal(err)
	}
	var ciphertext []byte
	if err := pool.QueryRow(ctx, `SELECT authorization_sealed FROM outbound_integration_credentials WHERE integration_id = $1`, integrationID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte(providerAuthorization)) {
		t.Fatal("provider credential is stored in plaintext")
	}

	resolver, err := outbound.NewPostgresResolver(pool)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := outbound.NewPostgresBackend(pool)
	if err != nil {
		t.Fatal(err)
	}
	credentialResolver, err := outbound.NewPostgresSealedCredentialResolver(pool, []*age.X25519Identity{identity}, []string{integrationID})
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
	mintAssertion := func(appID string) string {
		t.Helper()
		assertion, err := signer.Mint(time.Now(), account.ID, appID, "instance-1", "gregale:outbound:"+integrationID)
		if err != nil {
			t.Fatal(err)
		}
		return assertion.AccessToken
	}
	handler, err := outbound.NewHandler(resolver, backend, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	handler.IdentityVerifier = verifier
	handler.CredentialResolver = credentialResolver

	request := func(path, appAssertion, spoofedAppID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://gateway.test/i/"+integrationID+path, nil)
		req.Header.Set(outbound.WorkloadIdentityHeader, appAssertion)
		req.Header.Set(outbound.AppHeader, spoofedAppID)
		req.Header.Set(outbound.TokenHeader, "guest-controlled-token")
		req.Header.Set("Authorization", "Bearer guest-controlled-provider-key")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	deniedRoute := request("/v1/widgets/unsafe", mintAssertion(app.ID), app.ID)
	if deniedRoute.Code != http.StatusForbidden {
		t.Fatalf("out-of-policy route status = %d, want 403", deniedRoute.Code)
	}
	unboundCaller := request("/v1/widgets/safe/item-1", mintAssertion(unboundApp.ID), app.ID)
	if unboundCaller.Code != http.StatusForbidden {
		t.Fatalf("unbound caller status = %d, want 403", unboundCaller.Code)
	}
	var admissionRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbound_admission_state WHERE integration_id = $1`, integrationID).Scan(&admissionRows); err != nil {
		t.Fatal(err)
	}
	if admissionRows != 0 || providerCalls.Load() != 0 {
		t.Fatalf("denied requests reached admission/provider: admission rows=%d provider calls=%d", admissionRows, providerCalls.Load())
	}

	allowed := request("/v1/widgets/safe/item-1", mintAssertion(app.ID), unboundApp.ID)
	if allowed.Code != http.StatusNoContent || providerCalls.Load() != 1 {
		t.Fatalf("allowed request status=%d provider calls=%d", allowed.Code, providerCalls.Load())
	}
	if bytes.Contains(allowed.Body.Bytes(), []byte(providerAuthorization)) ||
		bytes.Contains([]byte(allowed.Header().Get("X-Provider-Result")), []byte(providerAuthorization)) ||
		allowed.Header().Get("Authorization") != "" {
		t.Fatal("provider credential leaked in the gateway response")
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
