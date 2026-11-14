package main_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestDomainSetupRetriesAndCompletesWildcard(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/apps/my-api":
			_, _ = fmt.Fprint(w, `{"id":"app-1","slug":"my-api"}`)
		case r.URL.Path == "/v1/domains" && r.Method == http.MethodGet:
			_, _ = fmt.Fprint(w, `[]`)
		case r.URL.Path == "/v1/domains" && r.Method == http.MethodPost:
			_, _ = fmt.Fprint(w, `{"domain":"*.example.com","app_id":"app-1","challenge_token":"proof"}`)
		case r.URL.Path == "/v1/domains/*.example.com/verify":
			if attempts.Add(1) == 1 {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = fmt.Fprint(w, `{"status":422,"code":"domain_verification_failed","detail":"DNS is still propagating"}`)
			} else {
				_, _ = fmt.Fprint(w, `{"domain":"*.example.com","app_id":"app-1","verified":true,"cert_status":"issued","cert_not_after":"2099-01-01T00:00:00Z"}`)
			}
		default:
			_, _ = fmt.Fprint(w, `{}`)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	tty := true
	oldTTY, oldIn, oldOut, oldErr := *main.TestOnlyTTY, *main.OsStdin, *main.OsStdout, *main.OsStderr
	*main.TestOnlyTTY = &tty
	*main.OsStdin = strings.NewReader("y\n")
	var out, stderr bytes.Buffer
	*main.OsStdout, *main.OsStderr = &out, &stderr
	t.Cleanup(func() {
		*main.TestOnlyTTY, *main.OsStdin, *main.OsStdout, *main.OsStderr = oldTTY, oldIn, oldOut, oldErr
	})
	if code := main.Run([]string{"domain", "setup", "*.example.com", "--app", "my-api", "--poll-interval", "5s", "--timeout", "12s"}); code != 0 {
		t.Fatalf("setup exit %d, stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
	if attempts.Load() != 2 || !strings.Contains(out.String(), "'*.example.com'") || !strings.Contains(out.String(), "_faas-verify.example.com") || strings.Contains(stderr.String(), "Make *.example.com the default") {
		t.Fatalf("attempts=%d stdout=%q stderr=%q", attempts.Load(), out.String(), stderr.String())
	}
}
