package main_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ADR-20261026145832: logs view is one place for saved log filters; local
// views stay local, --shared ones live on the server.
func TestLogsViewLocalAndShared(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_, _ = fmt.Fprint(w, `{"searches":[{"name":"errors","query":"level=error"}]}`)
		case http.MethodPut:
			_, _ = fmt.Fprint(w, `{"name":"errors","query":"level=error"}`)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"logs", "view", "save", "errors", "--shared", "--level", "error", "--app", "demo"}, "PUT /v1/apps/demo/log-searches/errors"},
		{[]string{"logs", "view", "list", "--shared", "--app", "demo"}, "GET /v1/apps/demo/log-searches"},
		{[]string{"logs", "view", "delete", "errors", "--shared", "--app", "demo"}, "DELETE /v1/apps/demo/log-searches/errors"},
	} {
		requests = nil
		code, stdout, stderr := runCLI(t, tc.args...)
		if code != 0 || strings.Join(requests, " ") != tc.want {
			t.Errorf("%v = %d, requests %v, want %q\n%s%s", tc.args, code, requests, tc.want, stdout, stderr)
		}
	}

	requests = nil
	if code, stdout, stderr := runCLI(t, "logs", "view", "save", "slow-errors", "--since", "1h", "--level", "error"); code != 0 || len(requests) != 0 {
		t.Fatalf("local save = %d, requests %v\n%s%s", code, requests, stdout, stderr)
	}
	if code, stdout, _ := runCLI(t, "logs", "view", "list"); code != 0 || !strings.Contains(stdout, "slow-errors") {
		t.Fatalf("local list = %d, want slow-errors:\n%s", code, stdout)
	}

	wantCode, wantOut, wantErr := runCLI(t, "logs", "defaults", "set", "--dry-run", "slow-errors")
	gotCode, gotOut, gotErr := runCLI(t, "logs", "view", "use", "slow-errors", "--default", "--dry-run")
	if gotCode != wantCode || gotOut != wantOut || gotErr != wantErr {
		t.Errorf("view use --default = %d\n%s%s\nwant defaults set = %d\n%s%s", gotCode, gotOut, gotErr, wantCode, wantOut, wantErr)
	}

	if code, _, stderr := runCLI(t, "logs", "view", "use", "slow-errors"); code == 0 || !strings.Contains(stderr, "--view") {
		t.Errorf("view use without --default = %d %q, want a pointer at --view", code, stderr)
	}
	if code, _, stderr := runCLI(t, "logs", "view", "rename", "a", "b", "--shared"); code == 0 || !strings.Contains(stderr, "--shared views support") {
		t.Errorf("--shared rename = %d %q, want a usage error", code, stderr)
	}
}
