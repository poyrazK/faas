package main_test

import (
	"context"
	"encoding/json"
	"errors"
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

func TestHTTPRetriesInvocationAndDiagnostics(t *testing.T) {
	main.SetupConnectionProfiles(t)
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
	var failures, requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/usage/summary" || r.Header.Get("Authorization") != "Bearer "+main.TestAPIKey('a') {
			t.Error("retry changed API target or authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "cookie-private")
		if failures.Add(-1) >= 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"code":"service_unavailable","detail":"body-private"}`)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	command := []string{"--json", "billing", "usage", "summary"}
	code, baseline, stderr := runHTTPDebugCLI(t, command...)
	if code != 0 || stderr != "" {
		t.Fatalf("baseline exit=%d stderr=%s", code, stderr)
	}
	for _, prefix := range [][]string{{"--http-retries", "1"}, {"--http-retries=2"}, {"--http-retries=0", "--http-retries=1"}, {"--http-retries", "1", "--profile", "staging"}, {"--profile=staging", "--http-retries=1"}} {
		failures.Store(1)
		before := requests.Load()
		args := append(append([]string{}, prefix...), command...)
		code, stdout, stderr := runHTTPDebugCLI(t, args...)
		if code != 0 || stdout != baseline || stderr != "" || requests.Load()-before != 2 {
			t.Fatalf("prefix=%q exit=%d stderr=%s requests=%d", prefix, code, stderr, requests.Load()-before)
		}
	}
	failures.Store(1)
	code, stdout, stderr := runHTTPDebugCLI(t, "--debug-http", "--http-retries=1", "--json", "billing", "usage", "summary")
	if code != 0 || stdout != baseline {
		t.Fatalf("debug exit=%d stderr=%s", code, stderr)
	}
	type record struct {
		Event   string `json:"event"`
		Request int    `json:"request_seq"`
		Attempt int    `json:"attempt"`
		Max     int    `json:"max_attempts"`
		Delay   int64  `json:"retry_delay_ms"`
		Route   string `json:"route"`
	}
	var rows []record
	decoder := json.NewDecoder(strings.NewReader(stderr))
	for {
		var row record
		err := decoder.Decode(&row)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if len(rows) != 4 {
		t.Fatalf("expected two request/response pairs, got %d: %s", len(rows), stderr)
	}
	for i, row := range rows {
		if row.Attempt != i/2+1 || row.Max != 2 || row.Route != "/v1/usage/summary" || row.Request != i/2+1 || i%2 == 0 && row.Event != "request_start" || i%2 == 1 && row.Event != "response_headers" {
			t.Fatalf("invalid retry diagnostic: %+v", row)
		}
		if row.Attempt == 2 && row.Delay < 125 {
			t.Fatalf("missing retry delay: %+v", row)
		}
	}
	for _, secret := range []string{server.URL, main.TestAPIKey('a'), "cookie-private", "body-private"} {
		if strings.Contains(stderr, secret) {
			t.Fatal("retry diagnostic exposed private request or response content")
		}
	}
	for _, prefix := range [][]string{{"--http-retries="}, {"--http-retries=-1"}, {"--http-retries=6"}, {"--http-retries=1.5"}, {"--http-retries=private-invalid"}, {"--http-retries", "--json=private-invalid"}} {
		before := requests.Load()
		args := append(append([]string{}, prefix...), command...)
		code, stdout, stderr := runHTTPDebugCLI(t, args...)
		if code == 0 || stdout != "" || !strings.Contains(stderr, "requires an integer from 0 to 5") || strings.Contains(stderr, "private-invalid") || requests.Load() != before {
			t.Fatalf("invalid prefix=%q exit=%d stderr=%s", prefix, code, stderr)
		}
	}
	if code, _, stderr := runHTTPDebugCLI(t, "--http-retries"); code == 0 || !strings.Contains(stderr, "requires an integer") {
		t.Fatalf("missing retry count: exit=%d stderr=%s", code, stderr)
	}
	failures.Store(3)
	before := requests.Load()
	if code, _, stderr := runHTTPDebugCLI(t, "--http-retries=1", "--json", "billing", "usage", "summary"); code == 0 || requests.Load()-before != 2 {
		t.Fatalf("retry budget was not enforced: exit=%d stderr=%s", code, stderr)
	}
	// Prior success and parse failures must not enable retries in later runs.
	for _, prefix := range [][]string{nil, {"--http-retries=0"}} {
		failures.Store(1)
		before := requests.Load()
		args := append(append([]string{}, prefix...), command...)
		if code, _, stderr := runHTTPDebugCLI(t, args...); code == 0 || requests.Load()-before != 1 {
			t.Fatalf("retry state leaked: exit=%d stderr=%s", code, stderr)
		}
	}
	if code, _, stderr := runHTTPDebugCLI(t, "billing", "usage", "summary", "--http-retries=1"); code == 0 || !strings.Contains(stderr, "--http-retries is a global flag: put it before the command") {
		t.Fatalf("placement hint missing: exit=%d stderr=%s", code, stderr)
	}
}

func TestHTTPRetriesCompletion(t *testing.T) {
	main.SetupConnectionProfiles(t)
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			code, script, stderr := runHTTPDebugCLI(t, "completion", shell)
			if code != 0 || !strings.Contains(script, "http-retries") {
				t.Fatalf("completion missing: exit=%d stderr=%s", code, stderr)
			}
			if shell != "bash" {
				return
			}
			for _, words := range []string{"gregale --http-retries 3 logs app --fo", "gregale --http-retries=3 logs app --fo", "gregale --http-retries 3 --profile staging logs app --fo"} {
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				cmd := exec.CommandContext(ctx, "bash", "--noprofile", "--norc")
				cmd.Stdin = strings.NewReader(script + "\ngregale() { return 1; }\nCOMP_WORDS=(" + words + "); COMP_CWORD=$((${#COMP_WORDS[@]}-1)); __gregale; printf '%s\\n' \"${COMPREPLY[@]}\"\n")
				out, err := cmd.CombinedOutput()
				cancel()
				if err != nil || !strings.Contains(string(out), "--follow") {
					t.Fatalf("completion after %q: %s, %v", words, out, err)
				}
			}
		})
	}
}
