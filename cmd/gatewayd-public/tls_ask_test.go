package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestInstallOnDemandTLSAskFollowsFlag(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	if _, err := store.CreateCustomDomain(ctx, "shop.example.com", "app-1", "tok"); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.MarkDomainVerifiedIfChallenge(ctx, "shop.example.com", "tok"); err != nil || !ok {
		t.Fatalf("verify = %v, %v", ok, err)
	}
	for _, tc := range []struct {
		mode      string
		installed bool
		status    int
	}{
		{mode: "", installed: false, status: http.StatusNotFound},
		{mode: "on_demand", installed: true, status: http.StatusOK},
	} {
		t.Run("mode="+tc.mode, func(t *testing.T) {
			t.Setenv("FAAS_CUSTOM_DOMAIN_TLS", tc.mode)
			mux := http.NewServeMux()
			got := installOnDemandTLSAsk(mux, store, "gregale.dev", prometheus.NewRegistry(), slog.New(slog.DiscardHandler))
			if got != tc.installed {
				t.Fatalf("installed = %v, want %v", got, tc.installed)
			}
			req := httptest.NewRequest(http.MethodGet, gateway.OnDemandTLSAskPath+"?domain=shop.example.com", nil)
			req.RemoteAddr = "127.0.0.1:50000"
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
		})
	}
}
