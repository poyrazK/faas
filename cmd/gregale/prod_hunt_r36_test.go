package main_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// linkCheckout makes a temporary checkout linked to app demo and enters it.
func linkCheckout(t *testing.T) {
	t.Helper()
	repo := t.TempDir()
	for path, body := range map[string]string{".git/HEAD": "ref: refs/heads/main\n", ".gregale/project.json": `{"version":1,"project":"demo","app":"demo"}`} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(repo)
}

// Prod hunt r36: `bucket list` in a linked checkout printed only its usage.
func TestBucketListDefaultsToLinkedApp(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got == "" {
			got = r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	linkCheckout(t)
	if code, stdout, stderr := runCLI(t, "bucket", "list"); code != 0 || !strings.Contains(got, "/apps/demo/") {
		t.Fatalf("exit %d, first request %q\n%s%s", code, got, stdout, stderr)
	}
}

// Prod hunt r36: `slo list` answered "no app named list"; the SLO verbs live
// under `alert slo`.
func TestSLOVerbPointsToAlertSLO(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "test-token")
	for _, verb := range []string{"list", "create", "rm"} {
		code, _, stderr := runCLI(t, "slo", verb)
		if code == 0 || !strings.Contains(stderr, "gregale alert slo") {
			t.Errorf("slo %s = %d %q, want a pointer to gregale alert slo", verb, code, stderr)
		}
	}
}
