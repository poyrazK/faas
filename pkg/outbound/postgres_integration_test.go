package outbound_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/state"
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
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	handlers := make([]*outbound.Handler, 20)
	for i := range handlers {
		handlers[i], err = outbound.NewHandler(resolver, backend, provider.Client())
		if err != nil {
			t.Fatal(err)
		}
	}

	responses := make(chan int, len(handlers))
	var wg sync.WaitGroup
	for _, handler := range handlers {
		wg.Add(1)
		go func(h *outbound.Handler) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "http://gateway.test/i/"+integration.ID+"/v1/items", nil)
			req.Header.Set(outbound.TokenHeader, "secret")
			req.Header.Set(outbound.AppHeader, app.ID)
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
