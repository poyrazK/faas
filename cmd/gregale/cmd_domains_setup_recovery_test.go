package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDomainSetupResumesExpiredVerificationClaim(t *testing.T) {
	for _, scenario := range []string{"expired", "live", "verified", "other-app", "other-environment", "expires-during-wait"} {
		t.Run(scenario, func(t *testing.T) {
			main.ResetJSONOut(t)
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("FAAS_TOKEN", "test-token")
			now := time.Now()
			binding := api.CustomDomainResponse{Domain: "api.example.com", AppID: "app-1", ChallengeToken: "old-proof", VerificationExpiresAt: now.Add(-time.Hour).Format(time.RFC3339), TXTRecord: "old-proof"}
			switch scenario {
			case "live", "expires-during-wait":
				binding.VerificationExpiresAt = now.Add(time.Hour).Format(time.RFC3339)
			case "verified":
				binding.Verified = true
			case "other-app":
				binding.AppID = "app-2"
			case "other-environment":
				binding.Environment = "staging"
			}
			creates, verifies := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/v1/capabilities":
					_ = json.NewEncoder(w).Encode(api.CapabilitiesResponse{})
				case r.URL.Path == "/v1/apps/demo":
					_, _ = w.Write([]byte(`{"id":"app-1","slug":"demo"}`))
				case r.URL.Path == "/v1/domains" && r.Method == http.MethodGet:
					_ = json.NewEncoder(w).Encode([]api.CustomDomainResponse{binding})
				case r.URL.Path == "/v1/domains" && r.Method == http.MethodPost:
					creates++
					var request api.CreateCustomDomainRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if request.Domain != binding.Domain || request.AppID != "demo" || request.Environment != "" {
						t.Errorf("reclaim request: %+v", request)
					}
					binding.ChallengeToken = "fresh-proof"
					binding.TXTRecord = "fresh-proof"
					binding.VerificationExpiresAt = now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
					w.WriteHeader(http.StatusAccepted)
					_ = json.NewEncoder(w).Encode(binding)
				case r.URL.Path == "/v1/domains/api.example.com/verify":
					verifies++
					expired := binding
					expired.VerificationExpiresAt = now.Add(-time.Minute).Format(time.RFC3339)
					_ = json.NewEncoder(w).Encode(expired)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			oldTTY, oldMode, oldIn, oldOut, oldErr := *main.TestOnlyTTY, *main.NonInteractive, *main.OsStdin, *main.OsStdout, *main.OsStderr
			tty := true
			*main.TestOnlyTTY = &tty
			*main.NonInteractive = false
			answer := "n\n"
			if scenario == "expires-during-wait" {
				answer = "y\n"
			}
			*main.OsStdin = strings.NewReader(answer)
			var output bytes.Buffer
			*main.OsStdout = &output
			*main.OsStderr = &output
			t.Cleanup(func() {
				*main.TestOnlyTTY = oldTTY
				*main.NonInteractive = oldMode
				*main.OsStdin = oldIn
				*main.OsStdout = oldOut
				*main.OsStderr = oldErr
			})
			code := main.Run([]string{"domains", "setup", binding.Domain, "--app", "demo"})
			wantCreates := 0
			if scenario == "expired" {
				wantCreates = 1
			}
			wantCode := 0
			if scenario == "other-app" || scenario == "other-environment" || scenario == "expires-during-wait" {
				wantCode = 1
			}
			if code != wantCode || creates != wantCreates {
				t.Fatalf("exit=%d creates=%d, want %d/%d; %s", code, creates, wantCode, wantCreates, output.String())
			}
			if scenario == "expired" && (!strings.Contains(output.String(), "fresh-proof") || strings.Contains(output.String(), "old-proof")) {
				t.Fatalf("instructions: %s", output.String())
			}
			if scenario == "expires-during-wait" && (verifies != 1 || !strings.Contains(output.String(), "expired") || !strings.Contains(output.String(), "obtain a new TXT proof")) {
				t.Fatalf("expiry should stop polling with recovery: %s", output.String())
			}
		})
	}
}
