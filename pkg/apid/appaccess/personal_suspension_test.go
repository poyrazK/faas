package appaccess

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// H87: a member whose personal account is suspended for billing (a Free
// quota stop) still reads and deploys the company's app, whose status the
// company decides, while their own personal app answers 402.
func TestBillingSuspendedMemberKeepsCompanyAppsButNotPersonalOnes(t *testing.T) {
	store, _, actor, org, companyApp := fixture(t, state.OrgRoleDeveloper)
	personal, err := store.CreateApp(t.Context(), state.App{AccountID: actor.ID, Slug: "hobby", Type: state.AppTypeApp, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	actor.Status = state.AccountSuspended

	serve := func(app state.App, key *state.APIKey, pattern, path string) int {
		r := request(actor, key, pattern, path, app.Slug, "")
		w := httptest.NewRecorder()
		Wrap(store, nil, func(w http.ResponseWriter, _ *http.Request, _ state.Account) {
			w.WriteHeader(http.StatusNoContent)
		})(w, r, actor)
		return w.Code
	}
	orgKey := &state.APIKey{OrgID: org.ID}
	for _, tc := range []struct {
		name          string
		app           state.App
		key           *state.APIKey
		pattern, path string
		want          int
	}{
		{"company app read", companyApp, orgKey, "GET /v1/apps/{slug}", "/v1/apps/team-api", http.StatusNoContent},
		{"company app deploy", companyApp, orgKey, "POST /v1/apps/{slug}/deployments", "/v1/apps/team-api/deployments", http.StatusNoContent},
		{"personal app read", personal, nil, "GET /v1/apps/{slug}", "/v1/apps/hobby", http.StatusPaymentRequired},
		{"personal app deploy", personal, nil, "POST /v1/apps/{slug}/deployments", "/v1/apps/hobby/deployments", http.StatusPaymentRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := serve(tc.app, tc.key, tc.pattern, tc.path); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}
