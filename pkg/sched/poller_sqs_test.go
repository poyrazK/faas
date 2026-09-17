// adr: 100 — SQS-compatible trigger source and acknowledgement contract.
// poller_sqs_test.go — sqs_poller config validation tests.
//
// Scope (audit round 2 finding #2, PR #910): decodeSQSConfig used
// to call url.Parse and discard the result, only failing on truly
// malformed strings (missing brackets, invalid escape sequences).
// A schemeless URL like `faas-queue:9090/queues/ext-jobs` parses
// successfully with Scheme="" + Host="" — at Poll time the
// poller concatenates the path onto baseURL and
// http.NewRequestWithContext fails with a generic "no Host in
// request URL" transport error logged at runtime, NOT at config
// validation time.
//
// This file pins the new behaviour:
//
//  1. Schemeless URLs (Scheme="")         → rejected, message
//                                            contains "queue_url"
//  2. URLs that parse but lack a Host     → rejected, message
//                                            contains "queue_url"
//  3. http://host/path                    → accepted (clamped
//                                            long_poll_secs to 5)
//  4. https://host/path                   → accepted
//  5. Malformed strings (raw brackets)    → rejected by url.Parse
//  6. Missing queue_url key               → rejected with the
//                                            original "missing
//                                            queue_url" message
//  7. Empty trigger config blob           → rejected with "missing
//                                            config" message
//
// Tests build the sqlc.Trigger row by hand (the function takes
// the trigger struct verbatim; only Config + ConfigBytes are
// consumed by decodeSQSConfig). The config tests are pure CPU work;
// the bounded error-body regression uses only an in-process httptest server.

package sched

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestSQSPollerDeleteErrorBodyIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(strings.Repeat("x", api.TriggerBrokerErrorBodyMaxBytes+1024)))
	}))
	defer server.Close()

	p := &sqsPoller{
		client:   server.Client(),
		baseURL:  server.URL,
		inFlight: make(map[string]string),
	}
	err := p.deleteReceipts(context.Background(), []string{"receipt-1"})
	if err == nil {
		t.Fatal("deleteReceipts() returned nil")
	}
	if !strings.Contains(err.Error(), "[truncated]") {
		t.Fatalf("error = %q, want truncation marker", err)
	}
}

// sqsTestTrigger builds a sqlc.Trigger with the given config
// JSON. The other fields (Kind, Slug, etc.) are zero values; the
// poller doesn't read them on the validation path.
func sqsTestTrigger(t *testing.T, config map[string]any) sqlc.Trigger {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal test config: %v", err)
	}
	return sqlc.Trigger{Config: raw}
}

// TestDecodeSQSConfig_RejectsSchemelessURL pins audit round 2
// finding #2: a schemeless URL like `faas-queue:9090/queues/ext-jobs`
// parsed successfully but failed at Poll time with a generic
// "no Host in request URL" transport error. Now decodeSQSConfig
// rejects it up front with a stable message containing "queue_url".
func TestDecodeSQSConfig_RejectsSchemelessURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"host:port without scheme", "faas-queue:9090/queues/ext-jobs"},
		{"bare hostname", "faas-queue.example.com/queues/ext-jobs"},
		{"absolute path only", "/queues/ext-jobs"},
		{"empty path with no scheme", "localhost"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trig := sqsTestTrigger(t, map[string]any{
				"queue_url": tc.url,
			})
			cfg, err := decodeSQSConfig(trig)
			if err == nil {
				t.Fatalf("decodeSQSConfig(%q) = %+v, nil; want error (schemeless URL must be rejected at config time)", tc.url, cfg)
			}
			if !strings.Contains(err.Error(), "queue_url") {
				t.Errorf("error message = %q; want one containing \"queue_url\" (the operator error must name the field)", err.Error())
			}
		})
	}
}

// TestDecodeSQSConfig_RejectsMissingHost pins the
// `scheme://` (no host) corner case: url.Parse succeeds but
// Host is empty. The audit's second example was a path-less
// authority like `http://` itself.
func TestDecodeSQSConfig_RejectsMissingHost(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"scheme-only no host", "http://"},
		{"scheme + empty authority", "http:///queues/ext-jobs"},
		{"https only no host", "https://"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trig := sqsTestTrigger(t, map[string]any{
				"queue_url": tc.url,
			})
			_, err := decodeSQSConfig(trig)
			if err == nil {
				t.Fatalf("decodeSQSConfig(%q) returned no error; want missing-host rejection", tc.url)
			}
			if !strings.Contains(err.Error(), "queue_url") {
				t.Errorf("error message = %q; want one containing \"queue_url\"", err.Error())
			}
		})
	}
}

// TestDecodeSQSConfig_AcceptsValidURLs pins the happy path:
// http:// and https:// URLs parse and are accepted. long_poll_secs
// clamping (zero → 5, > 20 → 20, < 0 → 20) is exercised too.
func TestDecodeSQSConfig_AcceptsValidURLs(t *testing.T) {
	cases := []struct {
		name         string
		url          string
		longPollSec  int
		wantLongPoll int
	}{
		{"http localhost", "http://faas-queue:9090/queues/ext-jobs", 0, 5},
		{"https prod host", "https://sqs.us-east-1.amazonaws.com/123456789012/my-queue", 10, 10},
		{"long_poll_secs clamp to 20", "http://faas-queue:9090/q", 99, 20},
		{"long_poll_secs clamp negative", "http://faas-queue:9090/q", -5, 20},
		{"long_poll_secs explicit zero", "http://faas-queue:9090/q", 0, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trig := sqsTestTrigger(t, map[string]any{
				"queue_url":      tc.url,
				"long_poll_secs": tc.longPollSec,
			})
			cfg, err := decodeSQSConfig(trig)
			if err != nil {
				t.Fatalf("decodeSQSConfig(%q) = _, %v; want no error", tc.url, err)
			}
			if cfg.QueueURL != tc.url {
				t.Errorf("QueueURL = %q, want %q", cfg.QueueURL, tc.url)
			}
			if cfg.LongPollSec != tc.wantLongPoll {
				t.Errorf("LongPollSec = %d, want %d", cfg.LongPollSec, tc.wantLongPoll)
			}
		})
	}
}

// TestDecodeSQSConfig_RejectsTrulyMalformedURL pins the original
// url.Parse error path: a string with raw `[` / `]` brackets
// fails url.Parse and decodeSQSConfig surfaces the error
// unchanged. This was the only failure mode the OLD code caught.
func TestDecodeSQSConfig_RejectsTrulyMalformedURL(t *testing.T) {
	trig := sqsTestTrigger(t, map[string]any{
		"queue_url": "http://example.com/queue%ZZ", // invalid % escape
	})
	_, err := decodeSQSConfig(trig)
	if err == nil {
		t.Fatalf("decodeSQSConfig returned no error for malformed URL; want error")
	}
	if !strings.Contains(err.Error(), "queue_url") {
		t.Errorf("error message = %q; want one containing \"queue_url\"", err.Error())
	}
}

// TestDecodeSQSConfig_RejectsMissingQueueURL pins the
// pre-existing "missing queue_url" error message. The audit
// does not touch this branch; the test pins it as a regression
// guard so a future refactor that returns the wrong message on
// the missing-field path doesn't silently break the existing
// apid handler's error matching.
func TestDecodeSQSConfig_RejectsMissingQueueURL(t *testing.T) {
	trig := sqsTestTrigger(t, map[string]any{
		"long_poll_secs": 10,
	})
	_, err := decodeSQSConfig(trig)
	if err == nil {
		t.Fatalf("decodeSQSConfig returned no error; want missing queue_url")
	}
	if !strings.Contains(err.Error(), "missing queue_url") {
		t.Errorf("error message = %q; want one containing \"missing queue_url\"", err.Error())
	}
}

// TestDecodeSQSConfig_RejectsEmptyConfig pins the
// pre-existing "missing config" branch — empty t.Config blob
// means the trigger was created without the per-kind config
// payload (audit #2 doesn't touch this branch but the test
// documents the shape).
func TestDecodeSQSConfig_RejectsEmptyConfig(t *testing.T) {
	trig := sqlc.Trigger{Config: nil}
	_, err := decodeSQSConfig(trig)
	if err == nil {
		t.Fatalf("decodeSQSConfig returned no error; want missing config")
	}
	if !strings.Contains(err.Error(), "missing config") {
		t.Errorf("error message = %q; want one containing \"missing config\"", err.Error())
	}
}
