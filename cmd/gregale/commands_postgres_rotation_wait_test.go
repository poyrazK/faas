package main_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// Hunt 33 H33-59: `postgres bindings rotate --wait` gave up at 5 minutes
// while the rotation took about 9, and the timeout did not say the rotation
// carries on. The wait now defaults to 15 minutes and the timeout says how
// to follow it.
func TestPostgresBindingRotateWaitTimeoutSaysItContinues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"binding-1","database_id":"db-1","app_id":"app-1","scope":"production","environment_key":"DATABASE_URL","access":"read_write","credential_generation":2,"rotation_pending":true,"state":"ready"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var stdout, stderr bytes.Buffer
	oldOut, oldErr := *main.OsStdout, *main.OsStderr
	*main.OsStdout, *main.OsStderr = &stdout, &stderr
	defer func() { *main.OsStdout, *main.OsStderr = oldOut, oldErr }()
	code := main.Run([]string{"postgres", "bindings", "rotate", "binding-1", "--wait", "--wait-timeout", "30ms", "--poll-interval", "10ms"})
	if code != 1 || !strings.Contains(stderr.String(), "the rotation continues: check it with gregale postgres bindings get binding-1") {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	stdout.Reset()
	if code := main.Run([]string{"postgres", "bindings", "rotate", "--help"}); code != 0 || !strings.Contains(stdout.String(), "(default 15m)") {
		t.Fatalf("help exit %d: %s", code, stdout.String())
	}
}
