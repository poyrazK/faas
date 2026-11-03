package main_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// requestTimes masks timestamps commands derive from the clock, so two runs
// of the same command compare equal.
var requestTimes = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T[0-9%A-Za-z:.+-]*?Z`)

// appArgumentRun runs one CLI invocation against a recording fake API and
// returns its exit code and the first request it made.
func appArgumentRun(t *testing.T, args []string) (int, string) {
	t.Helper()
	var mu sync.Mutex
	var first string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if first == "" {
			first = r.Method + " " + requestTimes.ReplaceAllString(r.URL.RequestURI(), "TIME")
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"about:blank","title":"Not found","status":404,"code":"not_found"}`))
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Chdir(t.TempDir()) // commands such as product pilot init write local files
	var out, errOut bytes.Buffer
	*main.OsStdout, *main.OsStderr, *main.OsStdin = &out, &errOut, strings.NewReader("")
	done := make(chan int, 1)
	go func() { done <- main.Run(append([]string{"--non-interactive"}, args...)) }()
	select {
	case code := <-done:
		mu.Lock()
		defer mu.Unlock()
		return code, first
	case <-time.After(20 * time.Second):
		t.Fatalf("gregale %s did not finish", strings.Join(args, " "))
		return 0, ""
	}
}

// Every command that names its app only with --app takes it as the first
// argument too. Before this rule each one rejected a bare argument without a
// request, so the rule adds meaning instead of changing it; with the rule,
// `X verb app` behaves exactly like `X verb --app app`.
func TestAppArgumentMatchesAppFlag(t *testing.T) {
	enableEveryCLIFeature(t)
	t.Setenv("FAAS_TOKEN", "test-token")
	oldOut, oldErr, oldIn, oldEnabled := *main.OsStdout, *main.OsStderr, *main.OsStdin, *main.AppArgumentEnabled
	t.Cleanup(func() {
		*main.OsStdout, *main.OsStderr, *main.OsStdin, *main.AppArgumentEnabled = oldOut, oldErr, oldIn, oldEnabled
	})
	paths := main.AppArgumentPaths()
	if len(paths) < 100 {
		t.Fatalf("found only %d commands; the walk is broken", len(paths))
	}
	for _, path := range paths {
		name := strings.Join(path, " ")
		*main.AppArgumentEnabled = false
		if code, request := appArgumentRun(t, append(append([]string{}, path...), "zz-app")); code == 0 || request != "" {
			t.Errorf("%s: a bare argument already meant something (exit %d, request %q); declare its positional in the manifest", name, code, request)
			continue
		}
		*main.AppArgumentEnabled = true
		positionalCode, positionalRequest := appArgumentRun(t, append(append([]string{}, path...), "zz-app"))
		flagCode, flagRequest := appArgumentRun(t, append(append([]string{}, path...), "--app", "zz-app"))
		if positionalCode != flagCode || positionalRequest != flagRequest {
			t.Errorf("%s: app argument (exit %d, %q) differs from --app (exit %d, %q)", name, positionalCode, positionalRequest, flagCode, flagRequest)
		}
	}
}

func TestAppArgumentRejectsTwoDifferentApps(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "test-token")
	oldOut, oldErr := *main.OsStdout, *main.OsStderr
	t.Cleanup(func() { *main.OsStdout, *main.OsStderr = oldOut, oldErr })
	var errOut bytes.Buffer
	*main.OsStdout, *main.OsStderr = &bytes.Buffer{}, &errOut
	if code := main.Run([]string{"alerts", "list", "one-app", "--app", "other-app"}); code != 1 || !strings.Contains(errOut.String(), "name different apps") {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
}

func TestAppArgumentHelpSaysSo(t *testing.T) {
	oldOut := *main.OsStdout
	t.Cleanup(func() { *main.OsStdout = oldOut })
	var out bytes.Buffer
	*main.OsStdout = &out
	if code := main.Run([]string{"alerts", "list", "--help"}); code != 0 {
		t.Fatalf("alerts list --help = %d", code)
	}
	if !strings.Contains(out.String(), "The app can also be the first argument: gregale alert list my-api") {
		t.Fatalf("help lacks the app argument hint:\n%s", out.String())
	}
}
