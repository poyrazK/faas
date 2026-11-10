package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// A dashboard session unused for api.DashboardSessionIdleTimeout ends and
// its row is revoked, however long the cookie has left; an active one, or
// one never touched but signed in recently, goes on (ADR 20261110120000).
func TestRequireSessionCookie_IdleTimeout(t *testing.T) {
	now := time.Now()
	ago := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	cases := []struct {
		name     string
		issued   time.Time
		lastSeen *time.Time
		idle     bool
	}{
		{"active", now.Add(-6 * 24 * time.Hour), ago(time.Hour), false},
		{"fresh, never touched", now.Add(-time.Hour), nil, false},
		{"idle past the timeout", now.Add(-5 * 24 * time.Hour), ago(api.DashboardSessionIdleTimeout + time.Minute), true},
		{"never touched since an old sign-in", now.Add(-api.DashboardSessionIdleTimeout - time.Minute), nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lk := &fakeLookups{sess: state.Session{ID: "sid-idle", AccountID: "acct-1", IssuedAt: tc.issued, LastSeenAt: tc.lastSeen}}
			au := &fakeAuditor{}
			mw := newMW(t, newFakeAuthn(), &fakeSessions{}, lk, au)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
			_, handled, err := mw.RequireSessionCookie(rec, req, session.Envelope{AccountID: "acct-1", Sid: "sid-idle"})
			if err != nil {
				t.Fatal(err)
			}
			if handled != tc.idle {
				t.Fatalf("handled = %v (%d %s), want %v", handled, rec.Code, rec.Body.String(), tc.idle)
			}
			if !tc.idle {
				return
			}
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("idle session = %d, want 401", rec.Code)
			}
			lk.mu.Lock()
			revoked := len(lk.revokeCalls) == 1 && lk.revokeCalls[0].sid == "sid-idle"
			lk.mu.Unlock()
			if !revoked {
				t.Errorf("idle session row not revoked: %+v", lk.revokeCalls)
			}
			if rows := au.rowsOf("auth.session.idle_expired"); len(rows) != 1 {
				t.Errorf("idle audit rows = %d, want 1", len(rows))
			}
		})
	}
}
