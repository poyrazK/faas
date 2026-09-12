package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestClientGetAccountRateLimits(t *testing.T) {
	reset := time.Date(2026, 9, 12, 13, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/account/rate-limits" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(api.AccountRateLimitsResponse{Deploys: api.AccountDeployRateLimit{
			Used: 4, Limit: 10, Remaining: 6, WindowResetsAt: reset,
		}})
	}))
	defer srv.Close()

	got, err := api.NewClient(srv.URL, "token").GetAccountRateLimits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Deploys.Used != 4 || got.Deploys.Limit != 10 || got.Deploys.Remaining != 6 || !got.Deploys.WindowResetsAt.Equal(reset) {
		t.Fatalf("response = %+v", got)
	}
}

func TestClientPreservesDeployRateHeadersOnProblem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.Header().Set("Retry-After", "120")
		w.Header().Set("RateLimit-Limit", "10")
		w.Header().Set("RateLimit-Remaining", "0")
		w.Header().Set("RateLimit-Reset", "120")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(api.ErrDeployRateLimited(10, 120))
	}))
	defer srv.Close()

	_, err := api.NewClient(srv.URL, "token").GetAccountRateLimits(context.Background())
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T %v", err, err)
	}
	for name, want := range map[string]string{
		"Retry-After": "120", "RateLimit-Limit": "10", "RateLimit-Remaining": "0", "RateLimit-Reset": "120",
	} {
		got := apiErr.Problem.HasHeader(name)
		if len(got) != 1 || got[0] != want {
			t.Errorf("%s = %v, want [%s]", name, got, want)
		}
	}
}
