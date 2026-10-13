package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestGetAccountRateLimits(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	now := time.Now().UTC()
	for range 4 {
		if _, err := (*e.Store()).ConsumeAccountDeployRate(t.Context(), (*e.Acct()).ID, 10, now); err != nil {
			t.Fatal(err)
		}
	}

	rec := e.Do(t, http.MethodGet, "/v1/account/rate-limits", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got api.AccountRateLimitsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Deploys.Used != 4 || got.Deploys.Limit != 10 || got.Deploys.Remaining != 6 {
		t.Fatalf("response = %+v", got)
	}
	if got.Deploys.WindowResetsAt.Before(now.Add(59 * time.Minute)) {
		t.Fatalf("reset too early: %s", got.Deploys.WindowResetsAt)
	}
}

func TestCreateDeploymentReturnsDeployRateHeadersAnd429(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	created := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "rate-app"}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create app = %d: %s", created.Code, created.Body.String())
	}
	now := time.Now().UTC()
	for range 9 {
		if _, err := (*e.Store()).ConsumeAccountDeployRate(t.Context(), (*e.Acct()).ID, 10, now); err != nil {
			t.Fatal(err)
		}
	}
	body := api.CreateDeploymentRequest{Image: "registry.example/rate@sha256:" + apidserver.Repeat("a", 64)}
	accepted := e.Do(t, http.MethodPost, "/v1/apps/rate-app/deployments", body, map[string]string{"Idempotency-Key": "rate-accepted"})
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("accepted status = %d: %s", accepted.Code, accepted.Body.String())
	}
	if got := accepted.Header().Get("RateLimit-Remaining"); got != "0" {
		t.Fatalf("accepted remaining = %q", got)
	}
	replayed := e.Do(t, http.MethodPost, "/v1/apps/rate-app/deployments", body, map[string]string{"Idempotency-Key": "rate-accepted"})
	if replayed.Code != http.StatusAccepted {
		t.Fatalf("idempotent replay status = %d: %s", replayed.Code, replayed.Body.String())
	}

	blocked := e.Do(t, http.MethodPost, "/v1/apps/rate-app/deployments", body, map[string]string{"Idempotency-Key": "rate-blocked"})
	apidserver.AssertProblem(t, blocked, http.StatusTooManyRequests, api.CodeDeployRateLimited)
	for name, want := range map[string]string{"RateLimit-Limit": "10", "RateLimit-Remaining": "0"} {
		if got := blocked.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	for _, name := range []string{"RateLimit-Reset", "Retry-After"} {
		value := blocked.Header().Get(name)
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds < 1 || seconds > 3600 {
			t.Errorf("%s = %q, want seconds in [1,3600]", name, value)
		}
	}
}

func TestInvalidDeploymentDoesNotConsumeDeployRate(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	created := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "invalid-rate-app"}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create app = %d: %s", created.Code, created.Body.String())
	}
	rec := e.Do(t, http.MethodPost, "/v1/apps/invalid-rate-app/deployments",
		api.CreateDeploymentRequest{Image: "not-digest-pinned"}, map[string]string{"Idempotency-Key": "invalid-rate"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid deploy = %d: %s", rec.Code, rec.Body.String())
	}
	snapshot, err := (*e.Store()).ReadAccountDeployRate(t.Context(), (*e.Acct()).ID, 10, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Used != 0 {
		t.Fatalf("invalid deploy consumed rate budget: %+v", snapshot)
	}
}
