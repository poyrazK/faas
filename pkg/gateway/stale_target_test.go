package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMarkStaleTargetClaimsRecoveryOnce(t *testing.T) {
	var calls atomic.Int32
	signal := &staleTargetSignal{onStale: func() { calls.Add(1) }}
	ctx := withStaleTargetSignal(context.Background(), signal)

	markStaleTarget(ctx)
	markStaleTarget(ctx)

	if got := calls.Load(); got != 1 {
		t.Fatalf("stale handler calls = %d, want 1", got)
	}
	if !staleTargetDetected(ctx) {
		t.Fatal("stale target was not marked")
	}
}

// adr: 1151 — vmmd's instance_frozen refusal marks the target frozen, so the
// handler withdraws it instead of quarantining it; other Unavailable errors
// do not.
func TestMarkStaleTargetErrFlagsFrozenRefusal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		frozen bool
	}{
		{name: "frozen guest", err: grpcerr.ToStatus(api.NewProblem(int(codes.Unavailable), api.CodeInstanceFrozen, "Instance frozen", "the guest is frozen")), frozen: true},
		{name: "dead target", err: status.Error(codes.Unavailable, "connection refused"), frozen: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			signal := &staleTargetSignal{onStale: func() { calls.Add(1) }}
			ctx := withStaleTargetSignal(context.Background(), signal)
			markStaleTargetErr(ctx, tc.err)
			if calls.Load() != 1 || !staleTargetDetected(ctx) {
				t.Fatalf("stale handler calls = %d, detected %v; want one call", calls.Load(), staleTargetDetected(ctx))
			}
			if got := signal.frozen.Load(); got != tc.frozen {
				t.Fatalf("frozen = %v, want %v", got, tc.frozen)
			}
		})
	}
}

type staleTargetTestBackend struct {
	*fakeBackend
	evictedApp      string
	evictedInstance string
	recoveredApp    string
	recoveredScope  string
	recoveredCap    int
}

func (b *staleTargetTestBackend) EvictInstance(appID, instanceID string) {
	b.evictedApp = appID
	b.evictedInstance = instanceID
}

func (b *staleTargetTestBackend) RecoverStaleTarget(_ context.Context, appID, scope string, maxConcurrency int) {
	b.recoveredApp = appID
	b.recoveredScope = scope
	b.recoveredCap = maxConcurrency
}

func TestHandlerEvictsOnlyForwarderMarkedStaleTarget(t *testing.T) {
	b := &staleTargetTestBackend{fakeBackend: &fakeBackend{
		app:      App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro},
		host:     "app.example.com",
		upstream: "node-1",
		running:  true,
	}}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.WithForwarding(func(_ Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			markStaleTarget(r.Context())
			if b.evictedInstance == "" {
				t.Errorf("stale target was not evicted before forwarder returned")
			}
			http.Error(w, "instance gone", http.StatusServiceUnavailable)
		})
	})

	req := httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if b.evictedApp != "app-1" || b.evictedInstance != "i-fake" {
		t.Fatalf("eviction = (%q, %q), want (app-1, i-fake)", b.evictedApp, b.evictedInstance)
	}
	if b.recoveredApp != "app-1" || b.recoveredCap <= 0 {
		t.Fatalf("recovery = (%q, %q, %d), want app-1 with a positive cap", b.recoveredApp, b.recoveredScope, b.recoveredCap)
	}
}

func TestHandlerDoesNotEvictOrdinaryGuestError(t *testing.T) {
	b := &staleTargetTestBackend{fakeBackend: &fakeBackend{
		app:      App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro},
		host:     "app.example.com",
		upstream: "node-1",
		running:  true,
	}}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.WithForwarding(func(_ Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "guest error", http.StatusBadGateway)
		})
	})

	req := httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if b.evictedApp != "" || b.evictedInstance != "" {
		t.Fatalf("ordinary guest error evicted target: (%q, %q)", b.evictedApp, b.evictedInstance)
	}
	if b.recoveredApp != "" {
		t.Fatalf("ordinary guest error triggered recovery for %q", b.recoveredApp)
	}
}
