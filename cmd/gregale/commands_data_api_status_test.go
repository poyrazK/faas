package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDataAPIStatusDoesNotMutateOrExposeCredentials(t *testing.T) {
	for _, state := range []string{"active", "paused_owned", "paused_unowned", "paused_without_token", "contract_mismatch", "coordination_disabled", "invalid", "missing-jwt", "invalid-token"} {
		t.Run(state, func(t *testing.T) {
			fp := strings.Repeat("a", 64)
			refresh := strings.Repeat("b", 64)
			t.Setenv("GREGALE_DATA_API_ACCESS_TOKEN", "application.test.token")
			t.Setenv("GREGALE_DATA_API_REFRESH_TOKEN", refresh)
			if state == "paused_without_token" {
				refresh = ""
				t.Setenv("GREGALE_DATA_API_REFRESH_TOKEN", "")
			}
			if state == "missing-jwt" {
				t.Setenv("GREGALE_DATA_API_ACCESS_TOKEN", "")
			}
			if state == "invalid-token" {
				t.Setenv("GREGALE_DATA_API_REFRESH_TOKEN", "unsafe-token")
			}
			reads := 0
			serving := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads++
				if r.Method != "GET" || r.URL.Path != "/__gregale/schema" || r.Header.Get("Authorization") != "Bearer application.test.token" || r.Header.Get("X-Gregale-Refresh-Token") != refresh {
					t.Errorf("unexpected serving request")
				}
				body := map[string]any{"version": 1, "ready": true, "fingerprint": fp}
				if state != "coordination_disabled" {
					body["refresh_protocol"] = 1
					body["refresh_paused"] = strings.HasPrefix(state, "paused_")
					body["refresh_verified"] = state == "paused_owned"
					body["refresh_fingerprint"] = fp
				}
				if state == "invalid" {
					delete(body, "refresh_paused")
				}
				if state == "contract_mismatch" {
					body["refresh_fingerprint"] = strings.Repeat("c", 64)
				}
				main.WriteJSONTest(w, body)
			}))
			defer serving.Close()
			original := http.DefaultTransport
			http.DefaultTransport = serving.Client().Transport
			defer func() { http.DefaultTransport = original }()
			management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/v1/apps/notes" {
					t.Errorf("status mutated app: %s %s", r.Method, r.URL.Path)
				}
				main.WriteJSONTest(w, api.AppResponse{Slug: "notes", URL: serving.URL})
			}))
			defer management.Close()
			t.Setenv("FAAS_API", management.URL)
			t.Setenv("FAAS_TOKEN", "owner-test-token")
			stdout, stderr, restore := main.SwapIO(t)
			defer restore()
			code := main.Run([]string{"--json", "data-api", "status", "notes"})
			invalid := state == "invalid" || state == "missing-jwt" || state == "invalid-token"
			if (code != 0) != invalid {
				t.Fatalf("exit=%d output=%s error=%s", code, stdout.String(), stderr())
			}
			if !invalid {
				var result main.DataAPIStatus
				if err := json.Unmarshal([]byte(stdout.String()), &result); err != nil {
					t.Fatal(err)
				}
				expectedState := state
				if state == "paused_without_token" {
					expectedState = "paused_unowned"
				}
				if result.State != expectedState || result.NextAction == "" {
					t.Fatalf("incorrect status: %+v", result)
				}
			}
			if (state == "missing-jwt" || state == "invalid-token") && reads != 0 {
				t.Fatal("invalid credentials reached app")
			}
			for _, secret := range []string{refresh, "application.test.token", "owner-test-token", "unsafe-token"} {
				if secret != "" && strings.Contains(stdout.String()+stderr(), secret) {
					t.Fatal("status leaked credentials")
				}
			}
		})
	}
}

func TestDataAPIStatusBaselineIsSeparateFromAdmissionState(t *testing.T) {
	paused := false
	fp := strings.Repeat("a", 64)
	contract := main.DataAPIServingContract{Fingerprint: fp, RefreshProtocol: 1, RefreshPaused: &paused, RefreshFingerprint: fp}
	result, err := main.DescribeDataAPIStatus("notes", contract, strings.Repeat("b", 64))
	if err != nil || result.State != "active" || result.BaselineMatches == nil || *result.BaselineMatches {
		t.Fatalf("baseline mismatch changed admission state: %+v %v", result, err)
	}
}
