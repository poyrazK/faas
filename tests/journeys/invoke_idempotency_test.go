package journeys

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestInvokeIdempotencyKeyIsolatedAPI(t *testing.T) {
	configured := needs(t, tierSmoke, false)
	t.Parallel()
	var calls, refusal atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/billing/invoke/async" || r.Header.Get("Idempotency-Key") != "invoice-journey" {
			t.Error("async invocation changed target or explicit key")
		}
		if status := int(refusal.Load()); status != 0 {
			code := "conflict"
			if status == http.StatusUnprocessableEntity {
				code = "idempotency_key_reused"
			}
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "code": code, "title": "Submission refused"})
			return
		}
		_, _ = w.Write([]byte(`{"id":"inv-journey","status_url":"/v1/invocations/inv-journey"}`))
	}))
	defer server.Close()
	tg := &target{api: server.URL, cli: configured.cli, home: t.TempDir(), appsDomain: configured.appsDomain}
	for _, mode := range []string{"--json", "--json=false"} {
		for _, flags := range [][]string{
			{"--idempotency-key", "invoice-journey"},
			{"--async", "--idempotency-key="},
			{"--async", "--idempotency-key", "private\nkey"},
		} {
			args := append([]string{mode, "invoke", "billing"}, flags...)
			res := tg.exec(t, "fp_invoke-idempotency-private", tg.home, time.Minute, args...)
			if res.exit != 1 || len(res.stdout) != 0 || calls.Load() != 0 || !bytes.Contains(res.stderr, []byte("--idempotency-key")) || bytes.Contains(res.stderr, []byte("private")) {
				t.Fatalf("invalid key: exit=%d calls=%d stdout=%s stderr=%s", res.exit, calls.Load(), res.stdout, res.stderr)
			}
		}
	}
	for _, mode := range []string{"--json", "--json=false"} {
		res := tg.exec(t, "fp_invoke-idempotency-private", tg.home, time.Minute, mode, "invoke", "billing", "--async", "--idempotency-key", "invoice-journey")
		if res.exit != 0 || !bytes.Contains(res.stdout, []byte("inv-journey")) {
			t.Fatalf("explicit key: exit=%d stdout=%s stderr=%s", res.exit, res.stdout, res.stderr)
		}
	}
	for _, status := range []int{http.StatusConflict, http.StatusUnprocessableEntity} {
		refusal.Store(int32(status))
		res := tg.exec(t, "fp_invoke-idempotency-private", tg.home, time.Minute, "--json", "invoke", "billing", "--async", "--idempotency-key", "invoice-journey")
		var problem struct{ Status int }
		if res.exit == 0 || len(res.stdout) != 0 || json.Unmarshal(res.stderr, &problem) != nil || problem.Status != status {
			t.Fatalf("server refusal: exit=%d stdout=%s stderr=%s", res.exit, res.stdout, res.stderr)
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("submission count=%d, want 4 (one per command)", calls.Load())
	}
}

// The public API, rather than a simulated receipt cache, must replay the same
// accepted invocation and reject a changed request using that key (ADR-20261022204036).
func TestInvokeIdempotencyKeyLiveAPI(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	slug, _ := tg.sharedProbe(t)
	key := "journey-invoke-" + tg.runID + "-" + randomHex(t, 8)
	args := []string{"--json", "invoke", slug, "--async", "--idempotency-key", key, "--method", "GET", "--path", "/healthz", "--payload"}
	first := tg.run(t, time.Minute, append(args, `{"operation":"health"}`)...)
	if first.exit != 0 {
		var problem struct{ Status int }
		if json.Unmarshal(first.stderr, &problem) == nil && problem.Status == http.StatusPaymentRequired {
			t.Skip("async invocation is unavailable for the journey account's plan")
		}
		t.Fatalf("submission: exit=%d %s", first.exit, tg.redact(first.problem()))
	}
	var accepted struct{ ID string }
	if json.Unmarshal(first.stdout, &accepted) != nil || accepted.ID == "" {
		t.Fatalf("missing receipt: %s", first.stdout)
	}
	retry := tg.ok(t, time.Minute, append(args, `{"operation":"health"}`)...)
	var replayed struct{ ID string }
	if json.Unmarshal(retry.stdout, &replayed) != nil || replayed != accepted {
		t.Fatalf("retry did not replay the accepted invocation: %s", retry.stdout)
	}
	changed := tg.run(t, time.Minute, append(args, `{"operation":"changed"}`)...)
	var problem struct {
		Status int
		Code   string
	}
	if changed.exit == 0 || len(changed.stdout) != 0 || json.Unmarshal(changed.stderr, &problem) != nil || problem.Status != http.StatusUnprocessableEntity || problem.Code != "idempotency_key_reused" {
		t.Fatalf("changed payload was not refused: exit=%d %s", changed.exit, tg.redact(changed.problem()))
	}
}
