package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Exercise CLI retries against the production request fingerprinter and receipt
// store. Drop the first response after recording acceptance, as a lost response
// must not turn a manual retry into a new logical invocation (ADR-20261022204036).
func TestInvokeIdempotencyKeyLostResponse(t *testing.T) {
	main.ResetJSONOut(t)
	store := state.NewMemStore()
	var calls, executions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/billing/invoke/async" || r.Header.Get("Idempotency-Key") != "invoice-4821" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected invocation target or key", http.StatusBadRequest)
			return
		}
		call := calls.Add(1)
		key := r.Method + " " + r.URL.Path + "\n" + r.Header.Get("Idempotency-Key")
		fingerprint := httpkit.IdempotencyFingerprint(r, []byte("invocation-test-host-hmac-key-32"))
		res, err := store.ReserveIdempotentRequest(r.Context(), "account", key, fingerprint, time.Minute)
		if err != nil {
			t.Errorf("reserve receipt: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if res.Conflict {
			api.WriteProblem(w, api.ErrIdempotencyKeyReused())
			return
		}
		if res.InFlight {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Request in progress", "retry the same request once it completes"))
			return
		}
		body, status := res.Body, res.Status
		if res.Reserved {
			var req api.InvokeRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || string(req.Payload) != `{"invoice":9007199254740993}` || req.Method != "PUT" || req.Path != "/invoices" {
				t.Errorf("changed invocation input: %+v", req)
			}
			id := fmt.Sprintf("inv-%d", executions.Add(1))
			body, _ = json.Marshal(api.AsyncInvokeResponse{ID: id, StatusURL: "/v1/invocations/" + id})
			status = http.StatusAccepted
			if err := store.PutIdempotent(context.WithoutCancel(r.Context()), "account", key, status, body); err != nil {
				t.Errorf("save receipt: %v", err)
			}
		}
		if call == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("drop response: %v", err)
				return
			}
			_ = conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_invoke-idempotency-private")
	run := func(payload string) (int, string, string) {
		t.Helper()
		return runInvokeKeyCLI(t, "--json", "invoke", "billing", "--async", "--idempotency-key", "invoice-4821", "--payload", payload, "--method", "PUT", "--path", "/invoices")
	}
	const original = `{"invoice":9007199254740993}`
	if code, out, stderr := run(original); code == 0 || out != "" || calls.Load() != 1 || executions.Load() != 1 {
		t.Fatalf("lost response: exit=%d calls=%d executions=%d stdout=%s stderr=%s", code, calls.Load(), executions.Load(), out, stderr)
	}
	for _, payload := range []string{original, `{"invoice":9007199254740994}`, original} {
		code, out, stderr := run(payload)
		if payload != original {
			var problem api.Problem
			if code == 0 || out != "" || json.Unmarshal([]byte(stderr), &problem) != nil || problem.Status != 422 || problem.Code != api.CodeIdempotencyKeyReused {
				t.Fatalf("changed input: exit=%d stdout=%s stderr=%s", code, out, stderr)
			}
		} else {
			var receipt api.AsyncInvokeResponse
			if code != 0 || json.Unmarshal([]byte(out), &receipt) != nil || receipt.ID != "inv-1" {
				t.Fatalf("retry: exit=%d stdout=%s stderr=%s", code, out, stderr)
			}
		}
	}
	if calls.Load() != 4 || executions.Load() != 1 {
		t.Fatalf("calls=%d executions=%d; wanted four submissions and one logical invocation", calls.Load(), executions.Load())
	}
}

func TestInvokeIdempotencyKeyValidationPrecedesPayloadAndNetwork(t *testing.T) {
	main.ResetJSONOut(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_invoke-idempotency-private")
	for i, args := range [][]string{
		{"--idempotency-key", "invoice-4821"},
		{"--async=false", "--idempotency-key", "invoice-4821"},
		{"--async", "--idempotency-key="},
		{"--async", "--idempotency-key", "  \t "},
		{"--async", "--idempotency-key", "private\nkey"},
		{"--async", "--idempotency-key", "private\x00key"},
		{"--async", "--idempotency-key", "private\x7fkey"},
		{"--async", "--idempotency-key", "private key"},
		{"--async", "--idempotency-key", strings.Repeat("k", 256)},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			args = append([]string{"--json", "invoke", "billing", "--payload", "@/nonexistent-invocation-payload"}, args...)
			code, out, stderr := runInvokeKeyCLI(t, args...)
			if code != 1 || out != "" || calls.Load() != 0 || !strings.Contains(stderr, "--idempotency-key") || strings.Contains(stderr, "private") || strings.Contains(stderr, "read payload") {
				t.Fatalf("exit=%d calls=%d stdout=%s stderr=%s", code, calls.Load(), out, stderr)
			}
		})
	}
}

func TestInvokeIdempotencyKeyIsRequestScoped(t *testing.T) {
	main.ResetJSONOut(t)
	var mu sync.Mutex
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Path != "/v1/invocations/inv-1" || r.Header.Get("Idempotency-Key") != "" {
				t.Error("submission key leaked into an invocation read")
			}
			_ = json.NewEncoder(w).Encode(api.Invocation{ID: "inv-1", State: "completed"})
			return
		}
		mu.Lock()
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(api.AsyncInvokeResponse{ID: "inv-1"})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_invoke-idempotency-private")
	for _, args := range [][]string{
		{"invoke", "billing", "--async", "--idempotency-key", "  invoice-4821\t"},
		{"invoke", "--async", "--idempotency-key", strings.Repeat("k", 255), "billing"},
		{"invoke", "billing", "--async"}, {"invoke", "billing", "--async"},
		{"invocation", "get", "inv-1"},
	} {
		if code, out, stderr := runInvokeKeyCLI(t, append([]string{"--json"}, args...)...); code != 0 {
			t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, stderr)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(keys) != 4 || keys[0] != "invoice-4821" || keys[1] != strings.Repeat("k", 255) || keys[2] == "" || keys[3] == "" || keys[2] == keys[3] || keys[2] == keys[0] || keys[3] == keys[0] {
		t.Fatalf("unexpected submission identities: count=%d", len(keys))
	}
}

func runInvokeKeyCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	out, stderr, restore := main.SwapIO(t)
	defer restore()
	code := main.Run(args)
	return code, out.String(), stderr()
}
