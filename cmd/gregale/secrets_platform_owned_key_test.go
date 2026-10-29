package main_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Production-us rc.294: `secrets set PORT=9999` and `FAAS_APP_ID=x` were
// saved without a word, then guest-init stamped the platform values over
// them; h14-py still saw PORT=8000 (Hunt 14 H14-44).
func TestSecretsSetWarnsThatPlatformOwnedKeysAreIgnored(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/secrets/"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/x/secrets":
			main.WriteJSONTest(w, api.AppSecretListResponse{Quota: 25, Count: 3})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/account":
			main.WriteJSONTest(w, api.AccountResponse{Plan: string(api.PlanHobby)})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout, stderr bytes.Buffer
	oldOut, oldErr := *main.OsStdout, *main.OsStderr
	*main.OsStdout, *main.OsStderr = &stdout, &stderr
	t.Cleanup(func() { *main.OsStdout, *main.OsStderr = oldOut, oldErr })

	if code := main.CmdSecrets([]string{"set", "--app", "x", "PORT=9999", "FAAS_APP_ID=x", "API_TOKEN=v2"}); code != 0 {
		t.Fatalf("exit = %d: %s%s", code, stdout.String(), stderr.String())
	}
	got := stderr.String()
	for _, want := range []string{"PORT is ignored", "FAAS_APP_ID is ignored"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stderr missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "API_TOKEN") {
		t.Fatalf("an ordinary key was warned about:\n%s", got)
	}
}
