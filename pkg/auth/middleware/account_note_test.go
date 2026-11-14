package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestRequireSessionNotesTheCredentialsAccount pins ADR-20261114090000: the
// middleware names the account a bearer key belongs to on the request's
// note, including when it refuses the key, and names none for an unknown
// key.
func TestRequireSessionNotesTheCredentialsAccount(t *testing.T) {
	revoked := mkKey("key-1")
	revoked.AccountID = "acct-1"
	cases := []struct {
		name   string
		result authResult
		status int
		want   string
	}{
		{"active key", authResult{acct: mkActiveAccount("acct-1"), key: mkKey("key-1", api.ScopeAdmin)}, http.StatusOK, "acct-1"},
		{"revoked key", authResult{key: revoked, err: state.ErrAPIKeyRevoked}, http.StatusUnauthorized, "acct-1"},
		{"expired key", authResult{key: revoked, err: state.ErrAPIKeyExpired}, http.StatusUnauthorized, "acct-1"},
		{"suspended account", authResult{acct: state.Account{ID: "acct-2", Email: "s@x", Status: state.AccountSuspended}, key: mkKey("key-2", api.ScopeAdmin)},
			http.StatusPaymentRequired, "acct-2"},
		{"unknown key", authResult{err: state.ErrNotFound}, http.StatusUnauthorized, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			authn := newFakeAuthn()
			authn.authKey[string(api.HashAPIKey(validBearerKey))] = tc.result
			mw := newMW(t, authn, nil, nil, &fakeAuditor{})
			h := mw.RequireSession(func(w http.ResponseWriter, _ *http.Request, _ state.Account) { w.WriteHeader(http.StatusOK) })

			note := &authmw.AccountNote{}
			r := mkRequest("GET", "/v1/apps", map[string]string{"Authorization": "Bearer " + validBearerKey}, nil)
			r = r.WithContext(authmw.WithAccountNote(r.Context(), note))
			rec := httptest.NewRecorder()
			h(rec, r)
			if rec.Code != tc.status || note.AccountID != tc.want {
				t.Fatalf("status=%d note=%q, want %d and %q", rec.Code, note.AccountID, tc.status, tc.want)
			}
		})
	}
	if authmw.AccountNoteFrom(mkRequest("GET", "/", nil, nil).Context()) != nil {
		t.Fatal("a request without a note returned one")
	}
}
