package main

// adr: 732
// Production fork intent API: operator gate, plan gate, scopes, TTL bounds,
// active-fork limits, live-deployment requirement, cancellation and IDOR.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func appForkEnv(t *testing.T, plan api.Plan) testEnv {
	t.Helper()
	e := setup(t, plan)
	e.s.WithAppForksEnabled(true)
	return e
}

func decodeAppFork(t *testing.T, body []byte) api.AppForkResponse {
	t.Helper()
	var out api.AppForkResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode fork: %v; body=%s", err, body)
	}
	return out
}

func ttl(seconds int) *int { return &seconds }

func TestAppForks_DisabledByDefault(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedAppTaskDeployment(t, e, "my-api")
	for _, req := range []struct{ method, path string }{
		{http.MethodPost, "/v1/apps/my-api/forks"},
		{http.MethodGet, "/v1/apps/my-api/forks"},
		{http.MethodGet, "/v1/apps/my-api/forks/00000000-0000-0000-0000-000000000001"},
		{http.MethodDelete, "/v1/apps/my-api/forks/00000000-0000-0000-0000-000000000001"},
	} {
		rec := e.do(t, req.method, req.path, api.CreateAppForkRequest{}, nil)
		if rec.Code != http.StatusNotImplemented || !strings.Contains(rec.Body.String(), api.CodeAppForksNotEnabled) {
			t.Errorf("%s %s = %d %s, want 501 %s", req.method, req.path, rec.Code, rec.Body.String(), api.CodeAppForksNotEnabled)
		}
	}
}

func TestAppForks_PlanGateBelowPro(t *testing.T) {
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby} {
		e := appForkEnv(t, plan)
		seedAppTaskDeployment(t, e, "my-api")
		rec := e.do(t, http.MethodPost, "/v1/apps/my-api/forks", api.CreateAppForkRequest{}, nil)
		assertProblem(t, rec, http.StatusPaymentRequired, api.CodePlanAppForksNotAllowed)
	}
}

func TestAppForks_CreateGetListAndLimit(t *testing.T) {
	e := appForkEnv(t, api.PlanPro)
	_, deployment := seedAppTaskDeployment(t, e, "my-api")

	rec := e.do(t, http.MethodPost, "/v1/apps/my-api/forks", api.CreateAppForkRequest{}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create = %d %s, want 202", rec.Code, rec.Body.String())
	}
	fork := decodeAppFork(t, rec.Body.Bytes())
	if fork.Status != "queued" || fork.DeploymentID != deployment.ID || fork.TTLSeconds != 3600 {
		t.Fatalf("fork = %+v, want queued on the live deployment with the 1h default TTL", fork)
	}

	got := e.do(t, http.MethodGet, "/v1/apps/my-api/forks/"+fork.ID, nil, nil)
	if got.Code != http.StatusOK || decodeAppFork(t, got.Body.Bytes()).ID != fork.ID {
		t.Fatalf("get = %d %s", got.Code, got.Body.String())
	}
	list := e.do(t, http.MethodGet, "/v1/apps/my-api/forks", nil, nil)
	var listed api.AppForkListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || len(listed.Items) != 1 || listed.Items[0].ID != fork.ID {
		t.Fatalf("list = %d %s (%v)", list.Code, list.Body.String(), err)
	}

	second := e.do(t, http.MethodPost, "/v1/apps/my-api/forks", api.CreateAppForkRequest{}, nil)
	assertProblem(t, second, http.StatusConflict, api.CodeAppForkLimit)
	if !strings.Contains(second.Body.String(), `"limit":1`) || !strings.Contains(second.Body.String(), `"observed":1`) {
		t.Errorf("limit problem lacks limit/observed: %s", second.Body.String())
	}
}

func TestAppForks_TTLBounds(t *testing.T) {
	e := appForkEnv(t, api.PlanPro)
	seedAppTaskDeployment(t, e, "my-api")
	for _, seconds := range []int{59, 4*3600 + 1} {
		rec := e.do(t, http.MethodPost, "/v1/apps/my-api/forks", api.CreateAppForkRequest{TTLSeconds: ttl(seconds)}, nil)
		assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	}
	rec := e.do(t, http.MethodPost, "/v1/apps/my-api/forks", api.CreateAppForkRequest{TTLSeconds: ttl(600)}, nil)
	if rec.Code != http.StatusAccepted || decodeAppFork(t, rec.Body.Bytes()).TTLSeconds != 600 {
		t.Fatalf("600s fork = %d %s", rec.Code, rec.Body.String())
	}
}

func TestAppForks_RequiresALiveDeployment(t *testing.T) {
	e := appForkEnv(t, api.PlanPro)
	mustSeedApp(t, e, "my-api")
	rec := e.do(t, http.MethodPost, "/v1/apps/my-api/forks", api.CreateAppForkRequest{}, nil)
	assertProblem(t, rec, http.StatusConflict, api.CodeAppForkUnavailable)
}

// TestAppForks_CreateNeedsSecretsRead pins the ADR-732 rule that a fork is
// a secrets read: deploy:write alone is refused, both scopes are accepted.
func TestAppForks_CreateNeedsSecretsRead(t *testing.T) {
	for _, tc := range []struct {
		scopes []string
		want   int
	}{
		{[]string{api.ScopeDeployWrite}, http.StatusForbidden},
		{[]string{api.ScopeSecretsRead}, http.StatusForbidden},
		{[]string{api.ScopeDeployWrite, api.ScopeSecretsRead}, http.StatusAccepted},
	} {
		t.Run(strings.Join(tc.scopes, ","), func(t *testing.T) {
			e := appForkEnv(t, api.PlanPro)
			seedAppTaskDeployment(t, e, "my-api")
			plain, hash, err := api.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.CreateAPIKey(context.Background(), e.acct.ID, hash, "fork", tc.scopes); err != nil {
				t.Fatal(err)
			}
			e.key = plain
			rec := e.do(t, http.MethodPost, "/v1/apps/my-api/forks", api.CreateAppForkRequest{}, nil)
			if rec.Code != tc.want {
				t.Fatalf("scopes %v = %d %s, want %d", tc.scopes, rec.Code, rec.Body.String(), tc.want)
			}
		})
	}
}

func TestAppForks_CancelFreesTheSlot(t *testing.T) {
	e := appForkEnv(t, api.PlanPro)
	seedAppTaskDeployment(t, e, "my-api")
	fork := decodeAppFork(t, e.do(t, http.MethodPost, "/v1/apps/my-api/forks", api.CreateAppForkRequest{}, nil).Body.Bytes())

	rec := e.do(t, http.MethodDelete, "/v1/apps/my-api/forks/"+fork.ID, nil, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("cancel = %d %s", rec.Code, rec.Body.String())
	}
	if cancelled := decodeAppFork(t, rec.Body.Bytes()); cancelled.Status != "cancelled" || cancelled.FinishedAt == nil {
		t.Fatalf("cancelled = %+v, want terminal", cancelled)
	}
	if again := e.do(t, http.MethodDelete, "/v1/apps/my-api/forks/"+fork.ID, nil, nil); again.Code != http.StatusAccepted {
		t.Fatalf("second cancel = %d, want idempotent 202", again.Code)
	}
	if next := e.do(t, http.MethodPost, "/v1/apps/my-api/forks", api.CreateAppForkRequest{}, nil); next.Code != http.StatusAccepted {
		t.Fatalf("fork after cancel = %d %s", next.Code, next.Body.String())
	}
	for _, id := range []string{"not-a-uuid", "00000000-0000-0000-0000-000000000001"} {
		if rec := e.do(t, http.MethodGet, "/v1/apps/my-api/forks/"+id, nil, nil); rec.Code != http.StatusNotFound {
			t.Errorf("GET fork %q = %d, want 404", id, rec.Code)
		}
	}
}

func TestAppForks_CrossAccount404(t *testing.T) {
	e := appForkEnv(t, api.PlanPro)
	other := state.NewMemStore()
	otherAcct, _ := other.CreateAccount(t.Context(), "other@pro.com", api.PlanPro)
	mustSeedAppFor(t, other, otherAcct.ID, "their-api")
	rec := e.do(t, http.MethodPost, "/v1/apps/their-api/forks", api.CreateAppForkRequest{}, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account create = %d, want 404", rec.Code)
	}
}
