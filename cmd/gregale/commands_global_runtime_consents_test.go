package main

// adr: 774
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestGlobalRuntimeConsentCLIOriginalSetupAndRevoke(t *testing.T) {
	id := uuid.NewString()
	key, _, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	for _, verb := range []string{"setup-runtime-consent", "show-runtime-consent", "revoke-runtime-consent"} {
		t.Run(verb, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				method := http.MethodGet
				if verb == "setup-runtime-consent" {
					method = http.MethodPut
				}
				if verb == "revoke-runtime-consent" {
					method = http.MethodDelete
				}
				if r.Method != method || r.URL.Path != "/v1/global-apps/consent-global/runtime-consents/"+id || r.Header.Get("Authorization") != "Bearer directory-key" {
					t.Error("wrong scoped directory request")
				}
				if method == http.MethodPut {
					var req api.PutGlobalRuntimeConsentRequest
					if json.NewDecoder(r.Body).Decode(&req) != nil || req.RegionalAPIKey != key || req.Region != "eu-fsn" || !req.ExpiresAt.Equal(expiry) {
						t.Error("setup identity changed")
					}
				}
				if method == http.MethodDelete {
					var req api.RevokeGlobalRuntimeConsentRequest
					if json.NewDecoder(r.Body).Decode(&req) != nil || req.RegionalAPIKey != key {
						t.Error("revocation key absent")
					}
				}
				_ = json.NewEncoder(w).Encode(api.GlobalRuntimeConsentResponse{ID: id, Status: "active", ExpiresAt: expiry, Binding: api.GlobalRuntimeConsentBindingResponse{Region: "eu-fsn"}})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "directory-key")
			t.Setenv("GREGALE_TEST_REGIONAL_KEY", key)
			out, errOut, restore := swapIO(t)
			defer restore()
			args := []string{verb, "consent-global", "--consent", id}
			if verb != "show-runtime-consent" {
				args = append(args, "--regional-token-env", "GREGALE_TEST_REGIONAL_KEY")
			}
			if verb == "setup-runtime-consent" {
				args = append(args, "--region", "eu-fsn", "--expires-at", expiry.Format(time.RFC3339))
			}
			if code := cmdGlobalApp(args); code != 0 || calls != 1 {
				t.Fatal("request failed", code, calls)
			}
			if !strings.Contains(out.String(), id) || strings.Contains(out.String()+errOut(), key) || strings.Contains(out.String()+errOut(), "directory-key") {
				t.Fatal("wrong or secret CLI output")
			}
			if code := cmdGlobalApp([]string{verb, "consent-global", "--consent", "bad"}); code == 0 || calls != 1 {
				t.Fatal("invalid UUID reached network")
			}
		})
	}
}
