package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/state"
)

func budgetSignIn(h http.Handler, remote, email, password string) int {
	body, _ := json.Marshal(api.PasswordLoginRequest{Email: email, Password: password})
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remote
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// The failed-password budget lives in the store, so every apid sees the
// failures another one counted, and a wrong current password on an account
// change counts like a wrong sign-in (ADR-20261111150000).
func TestPasswordChecksShareTheSignInBudget(t *testing.T) {
	const email, password = "budget@example.com", "budget-password-123!"
	srv, _, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	flowSignup(t, h, email, password)
	acct, err := store.AccountByEmail(t.Context(), email)
	if err != nil {
		t.Fatal(err)
	}
	shared, ok := store.(state.LoginFailureBudgetStore)
	if !ok {
		t.Fatal("store has no shared login failure budget")
	}
	// Another apid counted all but one failure.
	budget := state.LoginFailureBudget{MaxFailures: api.LoginFailuresPerEmail, Window: api.LoginFailureEmailWindow}
	for range api.LoginFailuresPerEmail - 1 {
		if _, err := shared.RecordLoginFailure(t.Context(), auth.HashEmail(email), budget); err != nil {
			t.Fatal(err)
		}
	}
	if code := budgetSignIn(h, "198.51.100.7:1234", email, password); code != http.StatusOK {
		t.Fatalf("sign-in with budget left = %d, want 200", code)
	}
	// A successful sign-in does not reset the budget; a wrong current
	// password on an email change uses the last failure.
	mgr := *srv.Sessions()
	cookie := flowSession(t, store, mgr, acct.ID)
	if rec := flowSessionJSON(t, h, mgr, cookie, acct.ID, http.MethodPost, "/v1/account/email", "account_email_change",
		map[string]any{"email": "budget2@example.com", "current_password": "wrong-password-123!"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current password = %d %s, want 401", rec.Code, rec.Body.String())
	}
	if code := budgetSignIn(h, "203.0.113.9:1234", email, password); code != http.StatusTooManyRequests {
		t.Fatalf("sign-in after the budget ran out = %d, want 429", code)
	}
	if rec := flowSessionJSON(t, h, mgr, cookie, acct.ID, http.MethodPost, "/v1/account/email", "account_email_change",
		map[string]any{"email": "budget2@example.com", "current_password": password}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("email change after the budget ran out = %d %s, want 429", rec.Code, rec.Body.String())
	}
	// The pause is in the store, where every apid reads it.
	if wait, err := shared.LoginFailureRetryAfter(t.Context(), auth.HashEmail(email), budget); err != nil || wait <= 0 {
		t.Fatalf("shared retry-after = %s, %v; want a wait", wait, err)
	}
}

// Restoring an account scheduled for deletion re-enables its keys and apps,
// so it needs a full-access key and mails the owner (ADR-20261111150000).
func TestRestoreNeedsFullAccessKeyAndMails(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	acct, adminKey := flowAccountKey(t, store, "restore@example.com", true)
	deployKey, deployHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), acct.ID, deployHash, "ci", []string{api.ScopeDeployWrite}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountDeletionPending(t.Context(), acct.ID); err != nil {
		t.Fatal(err)
	}
	if rec := flowBearer(h, http.MethodPost, "/v1/account/restore", deployKey, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("restore with a deploy key = %d %s, want 403", rec.Code, rec.Body.String())
	}
	if rec := flowBearer(h, http.MethodPost, "/v1/account/restore", adminKey, ""); rec.Code != http.StatusOK {
		t.Fatalf("restore with a full-access key = %d %s, want 200", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, body := range flowMailsTo(mailer, acct.Email) {
			if strings.Contains(body, "deletion of your Gregale account") {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no deletion-cancelled notice; mails = %q", flowMailsTo(mailer, acct.Email))
}
