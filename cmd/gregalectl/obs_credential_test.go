package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// An explicit --admin-token wins; otherwise a logged-in operator reads with
// the MFA session even with $FAAS_ADMIN_TOKEN exported; the environment
// token is the fallback for automation.
func TestResolveObsCredentialOrder(t *testing.T) {
	for _, tc := range []struct {
		name               string
		session            string // "", "live" or "expired"
		flag, env          string
		want               obsCredential
		wantOK             bool
		wantStderrContains string
	}{
		{name: "flag beats session and env", session: "live", flag: "flag-token", env: "env-token", want: obsCredential{bearer: "flag-token"}, wantOK: true},
		{name: "session beats env", session: "live", env: "env-token", want: obsCredential{sessionCookie: "opaque-session"}, wantOK: true},
		{name: "env without a session", env: "env-token", want: obsCredential{bearer: "env-token"}, wantOK: true},
		{name: "env when the session expired", session: "expired", env: "env-token", want: obsCredential{bearer: "env-token"}, wantOK: true},
		{name: "nothing", wantStderrContains: "run 'gregalectl auth login --email <email>', or pass --admin-token"},
		{name: "expired session and no env", session: "expired", wantStderrContains: "operator session expired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(operatorSessionFile, t.TempDir()+"/operator-session.json")
			t.Setenv("FAAS_APID_URL", "https://api.gregale.test")
			t.Setenv("FAAS_ADMIN_TOKEN", tc.env)
			if tc.session != "" {
				expires := time.Now().UTC().Add(time.Hour)
				if tc.session == "expired" {
					expires = time.Now().UTC().Add(-time.Minute)
				}
				if err := saveOperatorSession(operatorSession{BaseURL: "https://api.gregale.test", Cookie: "opaque-session", ExpiresAt: expires}); err != nil {
					t.Fatal(err)
				}
			}
			_, stderr, restore := captureOperatorIO()
			defer restore()
			got, ok := resolveObsCredential("obs summary", tc.flag)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("resolveObsCredential = %+v, %t; want %+v, %t (stderr=%q)", got, ok, tc.want, tc.wantOK, stderr)
			}
			if !strings.Contains(stderr.String(), tc.wantStderrContains) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr, tc.wantStderrContains)
			}
		})
	}
}

// Every obs read sends the operator session, not $FAAS_ADMIN_TOKEN, once
// the operator has logged in. The fake apid refuses each request, so this
// checks only how each command authenticates.
func TestObsReadsSendTheOperatorSession(t *testing.T) {
	alertRef := "platform_alert/" + strings.Repeat("a", 64)
	for _, args := range [][]string{
		{subObsHealth},
		{subObsInputHealth},
		{subObsRuleHealth},
		{subObsRuleCoverage},
		{subObsRecordingCoverage},
		{subObsNotificationHealth},
		{subObsSummary},
		{subObsResponseTimes},
		{subObsRecurrence},
		{subObsCollectionHistory},
		{subObsIncidents},
		{subObsTriageHistory, "--incident", alertRef},
		{subObsEvidence, "--incident", obsBundleTestRef},
		{subObsContext, "--incident", obsBundleTestRef},
		{subObsBundle, "--incident", obsBundleTestRef, "--output", "-"},
		{subObsOverview},
		{subObsCapacity},
	} {
		t.Run(args[0], func(t *testing.T) {
			var mu sync.Mutex
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests++
				mu.Unlock()
				if cookie, err := r.Cookie("faas_sid"); err != nil || cookie.Value != "opaque-session" {
					t.Errorf("%s: session cookie = %v, %v", r.URL.Path, cookie, err)
				}
				if got := r.Header.Get("Authorization"); got != "" {
					t.Errorf("%s: Authorization = %q, want none", r.URL.Path, got)
				}
				if got := r.UserAgent(); got != operatorUserAgent {
					t.Errorf("%s: User-Agent = %q, want %q", r.URL.Path, got, operatorUserAgent)
				}
				http.Error(w, "refused by the test", http.StatusServiceUnavailable)
			}))
			defer srv.Close()
			installTestOperatorSession(t, srv.URL, "opaque-session")
			t.Setenv("FAAS_ADMIN_TOKEN", "env-token")
			_, stderr, restore := captureOperatorIO()
			defer restore()

			if code := cmdObsDispatch(args); code != 1 {
				t.Fatalf("exit = %d, want 1 from the refused request (stderr=%q)", code, stderr)
			}
			mu.Lock()
			defer mu.Unlock()
			if requests == 0 {
				t.Fatal("the command sent no request")
			}
		})
	}
}

// "--ownership mine" names the caller, so it needs the session even when an
// admin token is supplied.
func TestObsIncidentsMineNeedsTheSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	_, stderr, restore := captureOperatorIO()
	defer restore()
	if code := cmdObsIncidents([]string{"--ownership", "mine", "--admin-token", "flag-token"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "gregalectl auth login") {
		t.Fatalf("stderr = %q", stderr)
	}
}
