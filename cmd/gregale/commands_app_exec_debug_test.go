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

// `app exec --debug` (ADR-20261117070620) submits an interactive session
// whose command is the debug marker followed by the user's command, `sh`
// when none is given.
func TestCmdAppExecDebugSubmitsMarkedSession(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"my-app", "exec", "--debug"}, api.AppTaskDebugCommand + "|sh"},
		{[]string{"my-app", "exec", "--debug", "--no-tty", "--", "ls", "-la", "/"}, api.AppTaskDebugCommand + "|ls|-la|/"},
	} {
		var got string
		var interactive bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				Command     []string `json:"command"`
				Interactive bool     `json:"interactive"`
			}
			_ = json.NewDecoder(r.Body).Decode(&request)
			got, interactive = strings.Join(request.Command, "|"), request.Interactive
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"title":"stop","status":409,"detail":"test stops here"}`))
		}))
		t.Setenv("FAAS_API", server.URL)
		t.Setenv("FAAS_TOKEN", "test-token")
		_, _, restore := main.SwapIO(t)
		code := main.CmdAppDispatch(tc.args)
		restore()
		server.Close()
		if code == 0 || got != tc.want || !interactive {
			t.Fatalf("%v: exit %d, command %q interactive %v; want %q", tc.args, code, got, interactive, tc.want)
		}
	}
}

// --debug runs a command directly, so --shell is refused before any request.
func TestCmdAppExecDebugRefusesShell(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	_, _, restore := main.SwapIO(t)
	defer restore()
	if code := main.CmdAppDispatch([]string{"my-app", "exec", "--debug", "--shell", "--", "ls /"}); code == 0 {
		t.Fatal("--debug --shell accepted")
	}
}
