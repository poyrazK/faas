package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// A deploy with no credential falls back to an anonymous session only in a
// known coding agent's shell, never in CI, cron or a build server, and not
// when switched off (ADR-20261124080000).
func TestAnonymousDeployByDefault(t *testing.T) {
	agent := map[string]string{"CLAUDECODE": "1"}
	with := func(extra map[string]string) map[string]string {
		out := map[string]string{"CLAUDECODE": "1"}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	for _, tt := range []struct {
		name string
		env  map[string]string
		want bool
	}{
		{name: "coding agent", env: agent, want: true},
		{name: "unattended job without a terminal", env: map[string]string{}, want: false},
		{name: "jenkins", env: map[string]string{"JENKINS_URL": "https://ci.example"}, want: false},
		{name: "agent in ci", env: with(map[string]string{"CI": "true"}), want: false},
		{name: "agent, ci false", env: with(map[string]string{"CI": "false"}), want: true},
		{name: "opted out", env: with(map[string]string{"GREGALE_NO_ANONYMOUS_DEPLOY": "1"}), want: false},
		{name: "opt-out zero", env: with(map[string]string{"GREGALE_NO_ANONYMOUS_DEPLOY": "0"}), want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := main.AnonymousDeployByDefault(func(k string) string { return tt.env[k] }); got != tt.want {
				t.Fatalf("AnonymousDeployByDefault = %v, want %v", got, tt.want)
			}
		})
	}
}

// The anonymous deploy key lives in its own file, never the login store:
// later commands use it only while no login exists and its window is open.
func TestAnonymousSessionKeyIsNotTheLogin(t *testing.T) {
	main.SetupCLIRegression(t)
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("GREGALE_TOKEN", "")
	key := main.TestAPIKey('b')
	if err := main.SaveAnonymousSession(api.AnonymousSessionResponse{APIKey: key, ClaimURL: "https://api.example.test/claim/x", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	// Only deploy and claim use the key; every other command sees no login.
	if got := main.LoadToken(); got != "" {
		t.Fatalf("token with only an anonymous session = %q, want none", got)
	}
	if got := main.LoadStoredToken(); got != "" {
		t.Fatalf("anonymous key reached the login store: %q", got)
	}
	if got := main.LoadAnonymousSessionToken(); got != key {
		t.Fatalf("anonymous key = %q, want the saved one", got)
	}
	if err := main.SaveAnonymousSession(api.AnonymousSessionResponse{APIKey: key, ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if got := main.LoadAnonymousSessionToken(); got != "" {
		t.Fatalf("expired anonymous session still used: %q", got)
	}
}

// `gregale claim` prints a fresh claim link for an anonymous deploy's key
// and explains a regular account has nothing to claim.
// TestClaimWithEndedAnonymousKey: once the claim revoked the saved key,
// `gregale claim` says so and forgets the key.
func TestClaimWithEndedAnonymousKey(t *testing.T) {
	main.SetupCLIRegression(t)
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("GREGALE_TOKEN", "")
	key := main.TestAPIKey('c')
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(api.Problem{Status: http.StatusUnauthorized, Code: "unauthorized", Title: "Anonymous deploy key ended"})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	if err := main.SaveAnonymousSession(api.AnonymousSessionResponse{APIKey: key, ClaimURL: "https://api.example.test/claim/x", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	oldOut, oldErr := *main.OsStdout, *main.OsStderr
	*main.OsStdout, *main.OsStderr = &out, &errOut
	code := main.Run([]string{"claim"})
	*main.OsStdout, *main.OsStderr = oldOut, oldErr
	if code != 1 || !strings.Contains(out.String()+errOut.String(), "claimed or has ended") {
		t.Fatalf("exit = %d; out:\n%s%s", code, out.String(), errOut.String())
	}
	if gotAuth != "Bearer "+key {
		t.Fatalf("claim sent %q, want the anonymous key", gotAuth)
	}
	if got := main.LoadAnonymousSessionToken(); got != "" {
		t.Fatalf("refused anonymous key kept: %q", got)
	}
}

func TestClaimCommand(t *testing.T) {
	for _, tt := range []struct {
		name     string
		status   int
		wantCode int
		want     string
	}{
		{name: "link", status: http.StatusOK, wantCode: 0, want: "https://api.example.test/claim/tok"},
		{name: "regular account", status: http.StatusConflict, wantCode: 1, want: "regular Gregale account"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			main.SetupCLIRegression(t)
			var gotPath, gotMethod string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotMethod = r.URL.Path, r.Method
				if tt.status != http.StatusOK {
					w.Header().Set("Content-Type", "application/problem+json")
					w.WriteHeader(tt.status)
					_ = json.NewEncoder(w).Encode(api.Problem{Status: tt.status, Code: "conflict", Title: "Nothing to claim"})
					return
				}
				_ = json.NewEncoder(w).Encode(api.ClaimLinkResponse{ClaimURL: "https://api.example.test/claim/tok", ExpiresAt: time.Now().Add(time.Hour)})
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
			var out, errOut bytes.Buffer
			oldOut, oldErr := *main.OsStdout, *main.OsStderr
			*main.OsStdout, *main.OsStderr = &out, &errOut
			code := main.Run([]string{"claim"})
			*main.OsStdout, *main.OsStderr = oldOut, oldErr
			if code != tt.wantCode {
				t.Fatalf("exit = %d, want %d; out:\n%s%s", code, tt.wantCode, out.String(), errOut.String())
			}
			if gotMethod != http.MethodPost || gotPath != "/v1/account/claim-link" {
				t.Fatalf("request = %s %s", gotMethod, gotPath)
			}
			if !strings.Contains(out.String()+errOut.String(), tt.want) {
				t.Fatalf("output missing %q:\n%s%s", tt.want, out.String(), errOut.String())
			}
		})
	}
}
