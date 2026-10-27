// adr: 20261026112500
package apidserver_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/sessioncookie"
	"github.com/onebox-faas/faas/pkg/state"
)

// Over HTTPS a sign-in issues __Host-faas_sid and expires the legacy
// faas_sid; over plain HTTP (where browsers refuse __Host- cookies) it keeps
// the legacy name.
func TestLoginIssuesTheHostPrefixedSessionCookie(t *testing.T) {
	for name, tc := range map[string]struct {
		forwardedProto string
		wantName       string
		wantLegacyGone bool
	}{
		"https":      {"https", sessioncookie.Name, true},
		"plain http": {"", sessioncookie.LegacyName, false},
	} {
		t.Run(name, func(t *testing.T) {
			store := state.NewMemStore()
			const email, password = "alice@example.com", "correct-horse-battery-staple"
			acct, err := store.CreateAccount(context.Background(), email, api.PlanFree)
			if err != nil {
				t.Fatal(err)
			}
			phc, err := auth.Encode(password)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetAccountPassword(context.Background(), acct.ID, phc); err != nil {
				t.Fatal(err)
			}
			srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})

			form := url.Values{"email": {email}, "password": {password}}
			req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.forwardedProto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.forwardedProto)
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("/login status = %d (body=%s)", rec.Code, rec.Body.String())
			}

			var session, legacyExpiry *http.Cookie
			for _, c := range rec.Result().Cookies() {
				switch {
				case c.Name == tc.wantName && c.Value != "":
					session = c
				case c.Name == sessioncookie.LegacyName && c.MaxAge < 0:
					legacyExpiry = c
				}
			}
			if session == nil {
				t.Fatalf("no %s session cookie in %v", tc.wantName, rec.Result().Cookies())
			}
			if tc.wantName == sessioncookie.Name && (!session.Secure || session.Path != "/" || session.Domain != "") {
				t.Fatalf("__Host- cookie %+v must be Secure, Path=/, host-only", session)
			}
			if (legacyExpiry != nil) != tc.wantLegacyGone {
				t.Fatalf("legacy expiry = %v, want %v", legacyExpiry, tc.wantLegacyGone)
			}

			// The issued cookie authenticates the dashboard.
			dash := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
			dash.AddCookie(&http.Cookie{Name: session.Name, Value: session.Value})
			dashRec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(dashRec, dash)
			if loc := dashRec.Header().Get("Location"); dashRec.Code == http.StatusFound && strings.HasPrefix(loc, "/login") {
				t.Fatalf("dashboard rejected the issued %s cookie: %d %s", session.Name, dashRec.Code, loc)
			}
		})
	}
}
