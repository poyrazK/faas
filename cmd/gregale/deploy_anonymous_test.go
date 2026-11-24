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

// A deploy with no credential falls back to an anonymous session in an
// agent's shell, but never in CI or when switched off (ADR-20261124080000).
// Tests run without a terminal, so the unset case is the agent case.
func TestAnonymousDeployByDefault(t *testing.T) {
	for _, tt := range []struct {
		name string
		env  map[string]string
		want bool
	}{
		{name: "agent shell", want: true},
		{name: "ci", env: map[string]string{"CI": "true"}, want: false},
		{name: "ci false", env: map[string]string{"CI": "false"}, want: true},
		{name: "opted out", env: map[string]string{"GREGALE_NO_ANONYMOUS_DEPLOY": "1"}, want: false},
		{name: "opt-out zero", env: map[string]string{"GREGALE_NO_ANONYMOUS_DEPLOY": "0"}, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := main.AnonymousDeployByDefault(func(k string) string { return tt.env[k] }); got != tt.want {
				t.Fatalf("AnonymousDeployByDefault = %v, want %v", got, tt.want)
			}
		})
	}
}

// `gregale claim` prints a fresh claim link for an anonymous deploy's key
// and explains a regular account has nothing to claim.
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
