package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// GREGALE_TOKEN is the documented variable; FAAS_TOKEN, its earlier name,
// still works and loses to GREGALE_TOKEN when both are set.
func TestTokenEnvVariablesAuthenticateInOrder(t *testing.T) {
	documented, legacy := main.TestAPIKey('g'), main.TestAPIKey('f')
	for _, tc := range []struct {
		name          string
		gregale, faas string
		wantToken     string
		wantSource    string
	}{
		{name: "documented", gregale: documented, wantToken: documented, wantSource: "env:GREGALE_TOKEN"},
		{name: "legacy", faas: legacy, wantToken: legacy, wantSource: "env:FAAS_TOKEN"},
		{name: "both", gregale: documented, faas: legacy, wantToken: documented, wantSource: "env:GREGALE_TOKEN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main.SetupConnectionProfiles(t)
			t.Setenv("GREGALE_TOKEN", tc.gregale)
			t.Setenv("FAAS_TOKEN", tc.faas)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "Bearer "+tc.wantToken {
					t.Errorf("Authorization = %q", got)
				}
				_, _ = w.Write([]byte(`{"id":"acct-1","email":"me@example.com","plan":"free","status":"active"}`))
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			previousJSON := *main.JsonOutput
			*main.JsonOutput = true
			t.Cleanup(func() { *main.JsonOutput = previousJSON })
			var out bytes.Buffer
			if exit := main.CaptureStdoutSwap(t, &out, func() int { return main.Run([]string{"--profile", "staging", "profile", "check"}) }); exit != 0 {
				t.Fatalf("exit=%d output=%s", exit, out.String())
			}
			var report main.ProfileCheckReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.CredentialSource != tc.wantSource {
				t.Fatalf("credential_source=%s, want %s", report.CredentialSource, tc.wantSource)
			}
		})
	}
}
