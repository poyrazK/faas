package main_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestHTTPTimeoutAndReadRetriesShareInvocation(t *testing.T) {
	main.SetupConnectionProfiles(t)
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
	var failNext atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if failNext.Swap(false) {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	for _, prefix := range [][]string{
		{"--http-timeout", "2s", "--http-retries", "1"},
		{"--http-retries", "1", "--http-timeout", "2s"},
	} {
		failNext.Store(true)
		before := requests.Load()
		args := append(append([]string{}, prefix...), "--json", "billing", "usage", "summary")
		if code, stdout, stderr := runHTTPDebugCLI(t, args...); code != 0 || requests.Load() != before+2 || !json.Valid([]byte(stdout)) || stderr != "" {
			t.Fatalf("combined options failed: exit=%d requests=%d stdout=%q stderr=%q", code, requests.Load()-before, stdout, stderr)
		}
	}
	failNext.Store(true)
	before := requests.Load()
	if code, stdout, stderr := runHTTPDebugCLI(t, "--json", "billing", "usage", "summary"); code != 3 || requests.Load() != before+1 || stdout != "" {
		t.Fatalf("retry selection leaked: exit=%d requests=%d stdout=%q stderr=%q", code, requests.Load()-before, stdout, stderr)
	}
}

func TestHTTPTimeoutInvocationAndValidation(t *testing.T) {
	main.SetupConnectionProfiles(t)
	main.SetFakeKeyring(t)
	t.Setenv("FAAS_JSON", "")
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
	var requests atomic.Int32
	var slow atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/usage/summary" || r.Header.Get("Authorization") != "Bearer "+main.TestAPIKey('a') {
			t.Error("timeout flag changed the API target or authentication")
		}
		if slow.Load() {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	command := []string{"--json", "billing", "usage", "summary"}
	code, baseline, stderr := runHTTPDebugCLI(t, command...)
	if code != 0 || stderr != "" {
		t.Fatalf("baseline exit=%d stderr=%q", code, stderr)
	}
	for _, prefix := range [][]string{{"--http-timeout", "90s"}, {"--http-timeout=2m"}, {"--http-timeout=1ns", "--http-timeout", "90s"}, {"--debug-http", "--http-timeout=90s"}, {"--http-timeout", "90s", "--profile", "staging"}, {"--profile=staging", "--http-timeout=90s"}} {
		args := append(append([]string{}, prefix...), command...)
		if code, stdout, stderr := runHTTPDebugCLI(t, args...); code != 0 || stdout != baseline {
			t.Fatalf("prefix %q changed output: exit=%d stdout=%q stderr=%q", prefix, code, stdout, stderr)
		}
	}
	for _, prefix := range [][]string{{"--http-timeout="}, {"--http-timeout", "0"}, {"--http-timeout=-1s"}, {"--http-timeout=secret-value"}, {"--http-timeout", "--json=secret-value"}, {"--http-timeout", "1w"}} {
		before := requests.Load()
		args := append(append([]string{}, prefix...), command...)
		code, stdout, stderr := runHTTPDebugCLI(t, args...)
		if code == 0 || stdout != "" || !strings.Contains(stderr, "--http-timeout requires a positive duration") || strings.Contains(stderr, "secret-value") || requests.Load() != before {
			t.Fatalf("invalid timeout %q: exit=%d stdout=%q stderr=%q", prefix, code, stdout, stderr)
		}
	}
	before := requests.Load()
	if code, _, stderr := runHTTPDebugCLI(t, "--http-timeout"); code == 0 || !strings.Contains(stderr, "requires a positive duration") || requests.Load() != before {
		t.Fatalf("missing duration: exit=%d stderr=%q", code, stderr)
	}
	slow.Store(true)
	if code, stdout, stderr := runHTTPDebugCLI(t, "--http-timeout=50ms", "--json", "billing", "usage", "summary"); code == 0 || stdout != "" || !strings.Contains(stderr, `"code":"transport_error"`) || !strings.Contains(stderr, "timed out") {
		t.Fatalf("timeout did not bound request: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runHTTPDebugCLI(t, command...); code != 0 || stdout != baseline || stderr != "" {
		t.Fatalf("timeout leaked into next invocation: exit=%d stderr=%q", code, stderr)
	}
	// A later parse failure must also restore the invocation-only selection.
	_, _, _ = runHTTPDebugCLI(t, "--http-timeout=1ns", "--http-timeout=invalid")
	if code, _, stderr := runHTTPDebugCLI(t, command...); code != 0 {
		t.Fatalf("timeout leaked after parse error: exit=%d stderr=%q", code, stderr)
	}
	if code, _, stderr := runHTTPDebugCLI(t, "billing", "usage", "summary", "--http-timeout=90s"); code == 0 || !strings.Contains(stderr, "--http-timeout is a global flag: put it before the command") {
		t.Fatalf("missing placement hint: exit=%d stderr=%q", code, stderr)
	}
}

func TestHTTPTimeoutCompletion(t *testing.T) {
	main.SetupConnectionProfiles(t)
	main.SetFakeKeyring(t)
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			code, script, stderr := runHTTPDebugCLI(t, "completion", shell)
			if code != 0 || !strings.Contains(script, "http-timeout") {
				t.Fatalf("completion missing timeout: exit=%d stderr=%q", code, stderr)
			}
			if shell != "bash" {
				return
			}
			for _, words := range []string{
				"gregale --http-timeout 90s logs app --fo",
				"gregale --http-timeout=90s logs app --fo",
				"gregale --http-timeout 90s --profile staging logs app --fo",
			} {
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				body := script + "\ngregale() { return 1; }\nCOMP_WORDS=(" + words + "); COMP_CWORD=$((${#COMP_WORDS[@]}-1)); __gregale; printf '%s\\n' \"${COMPREPLY[@]}\"\n"
				cmd := exec.CommandContext(ctx, "bash", "--noprofile", "--norc")
				cmd.Stdin = strings.NewReader(body)
				out, err := cmd.CombinedOutput()
				if err != nil || !strings.Contains(string(out), "--follow") {
					t.Fatalf("completion after %q: %s, %v", words, out, err)
				}
			}
		})
	}
}
