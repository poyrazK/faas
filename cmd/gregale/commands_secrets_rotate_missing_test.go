package main_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Hunt 32 H32-82: `env rotate H32_NOPE=v1` on a key that was never set
// silently created it.
func TestCmdSecretsRotateRefusesAMissingKey(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	rotated := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/x/secrets":
			main.WriteJSONTest(w, api.AppSecretListResponse{Secrets: []api.AppSecretResponse{{Key: "API_TOKEN"}}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/rotate"):
			rotated = true
			main.WriteJSONTest(w, api.RotateAppSecretResponse{Key: "H32_NOPE"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	oldOut, oldErr := *main.OsStdout, *main.OsStderr
	var stderr bytes.Buffer
	*main.OsStdout, *main.OsStderr = &bytes.Buffer{}, &stderr
	t.Cleanup(func() { *main.OsStdout, *main.OsStderr = oldOut, oldErr })

	if code := main.CmdSecrets([]string{"rotate", "--app", "x", "H32_NOPE=v1"}); code == 0 {
		t.Fatal("rotating a missing key succeeded")
	}
	if rotated {
		t.Fatal("the rotation reached the API")
	}
	if !strings.Contains(stderr.String(), "gregale env set H32_NOPE=VALUE") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
