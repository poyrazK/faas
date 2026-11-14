package dataapibudget

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/dataapiworkload/workloadtest"
	budget "github.com/onebox-faas/faas/pkg/state/dataapibudget"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type recorder struct {
	account, app string
	calls        int
}

func (s *recorder) Take(_ context.Context, account, app, key string, limit, seconds int) (budget.Decision, error) {
	s.account, s.app = account, app
	s.calls++
	return budget.Decision{Allowed: true, RetryAfter: 10}, nil
}
func TestBudgetRequiresDedicatedAudienceAndDerivesOwnerFromAssertion(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "budget-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	store := &recorder{}
	live := workloadtest.Production("11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333")
	h := Handler{Store: store, Verifier: verifier, Workloads: live}
	body := `{"version":1,"key":"` + strings.Repeat("a", 64) + `","requests":7,"window_seconds":60}`
	for _, tc := range []struct {
		audience, body string
		want           int
	}{{workloadidentity.DataAPIUsageAudience, body, 401}, {workloadidentity.DataAPIBudgetAudience, body, 200}, {workloadidentity.DataAPIBudgetAudience, strings.Replace(body, `"requests":7`, `"requests":0`, 1), 400}, {workloadidentity.DataAPIBudgetAudience, strings.Replace(body, `"version":1`, `"version":1,"app_id":"other"`, 1), 400}} {
		token, err := signer.Mint(time.Now(), "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333", tc.audience)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "/budget", strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != tc.want {
			t.Fatalf("got %d: %s", res.Code, res.Body.String())
		}
	}
	if store.calls != 1 || store.account != "11111111-1111-4111-8111-111111111111" || store.app != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("bad authorization: %+v", store)
	}
	// ADR-20261112120000: stages and dead instances must not spend production credit.
	for _, tc := range []struct {
		scope, state string
		status       int
	}{
		{"staging", live.Instance.State, 403},
		{"production", "", 401},
	} {
		live.Deployment.Scope, live.Instance.State = tc.scope, tc.state
		token, mintErr := signer.Mint(time.Now(), live.Account.ID, live.App.ID, live.Instance.ID, workloadidentity.DataAPIBudgetAudience)
		if mintErr != nil {
			t.Fatal(mintErr)
		}
		req := httptest.NewRequest("POST", "/budget", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != tc.status || store.calls != 1 {
			t.Fatalf("scope=%s status=%d calls=%d", tc.scope, res.Code, store.calls)
		}
	}
}
