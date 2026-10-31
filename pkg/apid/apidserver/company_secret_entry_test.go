package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

var secretEntryCSRF = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

// A secret request on a company app needs the company's permission to manage
// the app's secrets at both ends, and only the member who asked may finish
// it. A developer can request one and type the value; before, the route fell
// back to the app's creator. A creator removed from the company cannot finish
// a request they opened while still a member.
//
// adr: 20261026174727
func TestCompanySecretEntryFollowsTheMembersAccess(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	creator := *e.Acct()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "vault", "Vault", api.PlanPro)
	if r := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "vault-api"}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create company app = %d %s", r.Code, r.Body)
	}
	app, err := store.AppBySlug(t.Context(), "vault-api")
	if err != nil {
		t.Fatal(err)
	}
	sessions := *(*e.S()).Sessions()
	sessionOf := func(acct state.Account) *http.Cookie {
		t.Helper()
		token, err := apidserver.MintDashboardSession(t.Context(), store, sessions, acct.ID)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: apidserver.SessionCookie, Value: token}
	}
	request := func(as map[string]string, key string) api.SecretEntryResponse {
		t.Helper()
		r := e.Do(t, http.MethodPost, "/v1/apps/vault-api/secret-requests", api.SecretEntryRequest{Key: key, Reason: "payments"}, as)
		if r.Code != http.StatusCreated {
			t.Fatalf("secret request = %d %s", r.Code, r.Body)
		}
		var out api.SecretEntryResponse
		if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	open := func(entry api.SecretEntryResponse, session *http.Cookie) (string, *http.Cookie) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, entry.EntryURL, nil)
		r.AddCookie(session)
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		csrf := secretEntryCSRF.FindStringSubmatch(w.Body.String())
		for _, cookie := range w.Result().Cookies() {
			if cookie.Name == middleware.CookieNameAuthenticated && len(csrf) == 2 {
				return csrf[1], cookie
			}
		}
		return "", nil
	}
	submit := func(entry api.SecretEntryResponse, session *http.Cookie, csrf string, form *http.Cookie, value string) *httptest.ResponseRecorder {
		t.Helper()
		body := url.Values{"secret": {entry.Ticket}, "csrf_token": {csrf}, "value": {value}}
		r := httptest.NewRequest(http.MethodPost, "/cli-auth", strings.NewReader(body.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(session)
		r.AddCookie(form)
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	stored := func(key string) bool {
		t.Helper()
		secret, err := store.GetAppSecret(t.Context(), app.AccountID, app.ID, key)
		return err == nil && secret != nil
	}

	// The creator opens a request and its form, then leaves the company.
	creatorSession := sessionOf(creator)
	left := request(nil, "LEFT_BEHIND")
	csrf, form := open(left, creatorSession)
	if form == nil {
		t.Fatal("the creator could not open their own request's form")
	}
	admin, asAdmin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	if err := store.TransferOrgOwnership(t.Context(), org.ID, creator.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, http.MethodDelete, "/v1/orgs/"+org.Slug+"/members/"+creator.ID, nil, asAdmin); r.Code >= 300 {
		t.Fatalf("remove creator = %d %s", r.Code, r.Body)
	}
	if r := submit(left, creatorSession, csrf, form, "sk_live_after_leaving"); r.Code < 400 || stored("LEFT_BEHIND") {
		t.Fatalf("the departed creator finished their request: %d %.200s", r.Code, r.Body)
	}
	if _, form := open(left, creatorSession); form != nil {
		t.Fatal("the departed creator could still open the form")
	}

	// A developer requests a secret and is the one to type it.
	developer, asDeveloper := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	entry := request(asDeveloper, "STRIPE_KEY")
	if _, form := open(entry, sessionOf(admin)); form != nil {
		t.Fatal("another member opened the developer's request")
	}
	developerSession := sessionOf(developer)
	csrf, form = open(entry, developerSession)
	if form == nil {
		t.Fatal("the developer could not open their request's form")
	}
	if r := submit(entry, developerSession, csrf, form, "sk_live_typed_by_the_developer"); r.Code != http.StatusOK || !stored("STRIPE_KEY") {
		t.Fatalf("developer's secret entry = %d %s", r.Code, regexp.MustCompile(`could not save[^<]*`).FindString(r.Body.String()))
	}
	r := e.Do(t, http.MethodPost, "/v1/apps/vault-api/secret-requests/status", api.SecretEntryStatusRequest{Ticket: entry.Ticket, ExchangeCode: entry.ExchangeCode}, asDeveloper)
	var status api.SecretEntryStatus
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &status) != nil || status.Status != api.SecretEntrySet {
		t.Fatalf("developer's request status = %d %s", r.Code, r.Body)
	}
	if bytes.Contains(r.Body.Bytes(), []byte("sk_live_typed_by_the_developer")) {
		t.Fatal("the status returned the secret value")
	}
}
