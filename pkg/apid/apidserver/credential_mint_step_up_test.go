package apidserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Minting a credential outlives the session that mints it, so it needs the
// same fresh step-up as rotating one (POST /v1/keys/{id}/rotate and the
// org-key twin already required it; the personal mint routes did not).
func TestCredentialMintRequiresFreshStepUp(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
	}{
		{"api key", "/v1/keys", `{"name":"ci","scopes":["admin"]}`},
		{"deploy token", "/v1/apps/mint-app/deploy-tokens", `{"label":"ci"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			if _, err := (*e.Store()).CreateApp(context.Background(), state.App{
				AccountID: (*e.Acct()).ID, Slug: "mint-app", Status: state.AppActive, RAMMB: 256,
			}); err != nil {
				t.Fatal(err)
			}
			cookieAt := func(stepUp time.Time) *http.Cookie {
				sid := uuid.NewString()
				if _, err := (*e.Store()).CreateSession(context.Background(), sid, (*e.Acct()).ID, "192.0.2.30", "mint-test"); err != nil {
					t.Fatal(err)
				}
				token, err := (*(*e.S()).Sessions()).IssueWithSessionAndBindingHashAndStepUp(sid, (*e.Acct()).ID, "", stepUp, false)
				if err != nil {
					t.Fatal(err)
				}
				return &http.Cookie{Name: apidserver.SessionCookie, Value: token}
			}
			post := func(c *http.Cookie) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
				req.AddCookie(c)
				rec := httptest.NewRecorder()
				(*e.H()).ServeHTTP(rec, req)
				return rec
			}
			stale := post(cookieAt(time.Now().Add(-10 * time.Minute)))
			apidserver.AssertProblem(t, stale, http.StatusForbidden, api.CodeStepUpRequired)
			if fresh := post(cookieAt(time.Now())); fresh.Code != http.StatusCreated && fresh.Code != http.StatusOK {
				t.Fatalf("fresh step-up mint = %d, want success: %s", fresh.Code, fresh.Body.String())
			}
		})
	}
}
