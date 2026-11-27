package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func runSourceRunCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	out, stderr, restore := main.SwapIO(t)
	defer restore()
	code := main.Run(args)
	return code, out.String(), stderr()
}

func TestCmdRunIdempotencyKeySurvivesLostResponse(t *testing.T) {
	const key = "customer-42-report-1"
	const receipt = `{"id":"run-original","status":"queued","runtime":"node24"}`
	var posts atomic.Int32
	bodies := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/executions" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Idempotency-Key"); got != key {
			t.Errorf("Idempotency-Key = %q, want %q", got, key)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		attempt := posts.Add(1)
		if attempt == 1 {
			bodies <- string(body)
			// Admission completed, but the caller cannot decode its receipt.
			_, _ = io.WriteString(w, `{"id":"run-original"`)
			return
		}
		if attempt > 2 {
			t.Error("submission retried without an explicit caller invocation")
			return
		}
		if string(body) != <-bodies {
			t.Errorf("retry changed the original request")
		}
		_, _ = io.WriteString(w, receipt)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	args := []string{"run", "--idempotency-key", key, "--runtime", "node24", "--source", "return 42", "--workflow-id", "report-42"}
	code, _, stderr := runSourceRunCLI(t, args...)
	if code == 0 || !strings.Contains(stderr, "Could not confirm whether the run was created") ||
		!strings.Contains(stderr, "sandbox list --workflow-id") || !strings.Contains(stderr, "original --idempotency-key") {
		t.Fatalf("uncertain submission: exit=%d stderr=%s", code, stderr)
	}
	code, stdout, stderr := runSourceRunCLI(t, append([]string{"--json"}, args...)...)
	var got api.ExecutionResponse
	if err := json.Unmarshal([]byte(stdout), &got); code != 0 || err != nil || got.ID != "run-original" {
		t.Fatalf("retry: exit=%d receipt=%+v stdout=%s stderr=%s", code, got, stdout, stderr)
	}
	if posts.Load() != 2 {
		t.Fatalf("posts=%d; want two explicit submissions", posts.Load())
	}
}

func TestCmdRunIdempotencyKeyOnlyAppliesToSubmission(t *testing.T) {
	var posts, reads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := "succeeded"
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/executions":
			posts.Add(1)
			status = "queued"
			if got := r.Header.Get("Idempotency-Key"); got != "report-1" {
				t.Errorf("submission key = %q", got)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/executions/run-1":
			reads.Add(1)
			if got := r.Header.Get("Idempotency-Key"); got != "" {
				t.Errorf("status read carried mutation key %q", got)
			}
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "run-1", "status": status, "runtime": "node22"})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	code, stdout, stderr := runSourceRunCLI(t, "--json", "run", "--source", "return 1", "--idempotency-key", " report-1 ", "--wait", "--poll-interval", "1ms")
	if code != 0 || posts.Load() != 1 || reads.Load() != 1 {
		t.Fatalf("exit=%d posts=%d reads=%d stdout=%s stderr=%s", code, posts.Load(), reads.Load(), stdout, stderr)
	}
}

func TestCmdRunIdempotencyKeyValidationPrecedesSourceReads(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	for _, key := range []string{strings.Repeat("k", 256), "key\nvalue", "key value", "key\x7fvalue", "unicode-☃"} {
		code, stdout, stderr := runSourceRunCLI(t, "--json", "run", "--idempotency-key", key, "--file", "missing-source-file.js")
		if code == 0 || stdout != "" || !strings.Contains(stderr, "Invalid --idempotency-key") || strings.Contains(stderr, "Could not read source") {
			t.Fatalf("invalid key: exit=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid keys caused %d requests", requests.Load())
	}
}

func TestCmdRunIdempotencySubmissionErrorsPreserveOutputContract(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		code       string
		json       bool
		keyed      bool
		wantAdvice bool
	}{
		{name: "uncertain keyed submission", status: http.StatusInternalServerError, keyed: true, wantAdvice: true},
		{name: "uncertain unkeyed submission", status: http.StatusInternalServerError, wantAdvice: true},
		{name: "JSON stays one Problem", status: http.StatusInternalServerError, keyed: true, json: true},
		{name: "explicit rejection", status: http.StatusUnprocessableEntity, keyed: true},
		{name: "source runtime disabled", status: http.StatusNotImplemented, code: api.CodeNotImplemented, keyed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(tc.status)
				code := tc.code
				if code == "" {
					code = api.CodeValidation
				}
				_ = json.NewEncoder(w).Encode(api.NewProblem(tc.status, code, "Submission refused", "fixture error"))
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "test-token")
			t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
			args := []string{"run", "--source", "return 1"}
			if tc.keyed {
				args = append(args, "--idempotency-key", "report-1")
			}
			if tc.json {
				args = append([]string{"--json"}, args...)
			}
			code, stdout, stderr := runSourceRunCLI(t, args...)
			if code == 0 || stdout != "" || requests.Load() != 1 {
				t.Fatalf("exit=%d requests=%d stdout=%s stderr=%s", code, requests.Load(), stdout, stderr)
			}
			if got := strings.Contains(stderr, "Could not confirm whether the run was created"); got != tc.wantAdvice {
				t.Errorf("recovery advice=%v, want %v: %s", got, tc.wantAdvice, stderr)
			}
			if tc.json && !json.Valid([]byte(strings.TrimSpace(stderr))) {
				t.Errorf("JSON error contains extra text: %s", stderr)
			}
			if !tc.keyed && strings.Contains(stderr, "original --idempotency-key") {
				t.Errorf("unkeyed submission told to reuse an unavailable key: %s", stderr)
			}
		})
	}
}

func TestCmdRunWithoutExplicitKeyCreatesDistinctSubmissions(t *testing.T) {
	keys := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/executions" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		keys <- r.Header.Get("Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"run-1","status":"queued","runtime":"node22"}`)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	for range 2 {
		if code, stdout, stderr := runSourceRunCLI(t, "--json", "run", "--source", "return 1"); code != 0 {
			t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
	}
	first, second := <-keys, <-keys
	if first == "" || second == "" || first == second {
		t.Fatalf("independent invocations reused a key: %q, %q", first, second)
	}
}

func TestCmdRunIdempotencyRoutingPreservesImageCommands(t *testing.T) {
	for _, args := range [][]string{
		{"--idempotency-key", "report-1", "--source", "return 1"},
		{"--idempotency-key=report-1", "--file", "handler.js"},
	} {
		if main.RunImageRequested(args) {
			t.Errorf("source submission routed to image mode: %q", args)
		}
	}
	for _, args := range [][]string{
		{"alpine", "tool", "--idempotency-key", "child-key"},
		{"--image", "alpine", "tool", "--idempotency-key", "child-key"},
	} {
		if !main.RunImageRequested(args) {
			t.Errorf("image command routed to source mode: %q", args)
		}
		opts, ok := main.ParseImageRunArgs(args)
		request := *opts.Request()
		if !ok || request.Args == nil || !reflect.DeepEqual(*request.Args, []string{"tool", "--idempotency-key", "child-key"}) {
			t.Errorf("image command changed: request=%+v ok=%v", request, ok)
		}
	}
}
