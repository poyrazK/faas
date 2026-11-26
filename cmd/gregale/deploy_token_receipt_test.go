package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Prod hunt r26: the deploy-token receipt read "expires: expires 2027-…" and
// named no variable for CI to put the token in. GREGALE_TOKEN is the token
// variable since #7959 (FAAS_TOKEN still works), so the receipt names it.
func TestDeployTokenReceiptNamesExpiryOnceAndTheCIVariable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/web/deploy-tokens" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"tok-1","app_id":"app-1","prefix":"fp_dep","status":"active","plaintext":"fp_dep_secret","expires_at":"2027-02-20T13:26:06Z"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	main.ResetJSONOut(t)
	stdout, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"app", "deploy-token", "create", "--app", "web"})
	restore()
	out := stdout.String()
	if code != 0 {
		t.Fatalf("exit %d\nstdout=%s\nstderr=%s", code, out, stderr())
	}
	if strings.Contains(out, "expires: expires") || !strings.Contains(out, "expires: 2027-02-20T13:26:06Z") {
		t.Fatalf("expiry line:\n%s", out)
	}
	if !strings.Contains(out, "GREGALE_TOKEN") || !strings.Contains(out, "gregale deploy --name web") {
		t.Fatalf("receipt does not name the CI variable:\n%s", out)
	}
}
