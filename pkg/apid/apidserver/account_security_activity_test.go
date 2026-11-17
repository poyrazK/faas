package apidserver_test

import (
	"encoding/json"
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

// The account page links a provider to the signed-in account whatever the
// provider identity's email is; the intent is refused without that browser
// session, and an identity linked to another account stays there.
func TestAccountPageLinksProviderBySession(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	mgr := *srv.Sessions()
	acct, _ := flowAccountKey(t, store, "linker@example.com", true)
	session := flowSession(t, store, mgr, acct.ID)
	intent := &http.Cookie{Name: apidserver.OAuthLinkCookie, Value: acct.ID}

	callback := func(cookies ...*http.Cookie) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/v1/auth/github/callback", nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		return req
	}
	if handled := srv.FinishOAuthLink(httptest.NewRecorder(), callback(session), "github", "gh-other", "someone-else@example.com"); handled {
		t.Fatal("a callback without a link intent was treated as a link")
	}
	rec := httptest.NewRecorder()
	if !srv.FinishOAuthLink(rec, callback(intent), "github", "gh-other", "someone-else@example.com") ||
		!strings.Contains(rec.Header().Get("Location"), "account_error=link_session") {
		t.Fatalf("link intent without the signed-in browser = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if _, err := store.OAuthLinkByProviderSubject(t.Context(), "github", "gh-other"); err == nil {
		t.Fatal("refused link still linked the identity")
	}

	rec = httptest.NewRecorder()
	if !srv.FinishOAuthLink(rec, callback(intent, session), "github", "gh-other", "someone-else@example.com") ||
		!strings.Contains(rec.Header().Get("Location"), "provider=linked") {
		t.Fatalf("link from the signed-in browser = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	link, err := store.OAuthLinkByProviderSubject(t.Context(), "github", "gh-other")
	if err != nil || link.AccountID != acct.ID {
		t.Fatalf("identity with a different email not linked to the account: %+v %v", link, err)
	}
	srv.WaitPasswordResetMail()
	if got := flowMailsTo(mailer, acct.Email); len(got) == 0 {
		t.Fatal("linking mailed no notice")
	}

	other, _ := flowAccountKey(t, store, "other-linker@example.com", true)
	otherIntent := &http.Cookie{Name: apidserver.OAuthLinkCookie, Value: other.ID}
	rec = httptest.NewRecorder()
	srv.FinishOAuthLink(rec, callback(otherIntent, flowSession(t, store, mgr, other.ID)), "github", "gh-other", "x@example.com")
	if !strings.Contains(rec.Header().Get("Location"), "account_error=link_taken") {
		t.Fatalf("identity linked to another account = %q, want link_taken", rec.Header().Get("Location"))
	}

	if body, _ := flowAccountPage(t, h, session, ""); !strings.Contains(body, "Security activity") {
		t.Fatal("account page has no security activity section")
	}
}

// The security activity feed lists only security kinds, newest first,
// with fixed summaries, and needs a full-access credential.
func TestSecurityActivity(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	acct, key := flowAccountKey(t, store, "activity@example.com", true)
	id := uuid.MustParse(acct.ID)
	now := time.Now()
	for i, row := range []struct {
		kind string
		data string
	}{
		{"auth.session.created", `{"method":"password","issued_ip":"198.51.100.7"}`},
		{"account.release_webhook_created", `{}`},
		{"api_key.created", `{"created_ip":"203.0.113.9"}`},
		{"account.password_set", `{"proof":"current_password"}`},
	} {
		if err := store.InsertAuditLog(t.Context(), state.AuditLog{
			ID: uuid.New(), Kind: row.kind, AccountID: &id, AccountEmail: acct.Email,
			ReceivedAt: now.Add(time.Duration(i) * time.Minute), Data: json.RawMessage(row.data),
		}); err != nil {
			t.Fatal(err)
		}
	}
	rec := flowBearer(h, http.MethodGet, "/v1/account/security-activity", key, "")
	var out api.SecurityActivityResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("security activity = %d %s", rec.Code, rec.Body.String())
	}
	var kinds []string
	for _, e := range out.Events {
		kinds = append(kinds, e.Kind)
	}
	want := []string{"account.password_set", "api_key.created", "auth.session.created"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("kinds = %v, want %v (security kinds only, newest first)", kinds, want)
	}
	if out.Events[1].IP != "203.0.113.9" || out.Events[2].IP != "198.51.100.7" || out.Events[2].Detail != "password" {
		t.Fatalf("fields = %+v", out.Events)
	}
	if out.Events[0].Summary != "Password set or changed" {
		t.Fatalf("summary = %q", out.Events[0].Summary)
	}

	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), acct.ID, hash, "read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	if code := flowBearer(h, http.MethodGet, "/v1/account/security-activity", plain, "").Code; code != http.StatusForbidden {
		t.Fatalf("read-only key = %d, want 403", code)
	}
}

// Creating an API key mails the owner; the notice names the label.
func TestAPIKeyCreationMailsNotice(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	acct, key := flowAccountKey(t, store, "keys@example.com", true)
	rec := flowBearer(h, http.MethodPost, "/v1/keys", key, `{"label":"ci-runner","scopes":["deploy:write"]}`)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("create key = %d %s", rec.Code, rec.Body.String())
	}
	srv.WaitPasswordResetMail()
	got := flowMailsTo(mailer, acct.Email)
	if len(got) != 1 || !strings.Contains(got[0], "An API key was created") || !strings.Contains(got[0], "ci-runner") {
		t.Fatalf("key notice = %q", got)
	}
}
