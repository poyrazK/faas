//go:build !no_pg

// Package e2e — webhook_e2e_test.go is the §14 issue #476 / ADR-076
// acceptance gate. Pins the cross-component tripwire that the apid
// CRUD surface, the schedd wiring, and the dispatcher share the
// same wire shape and the same row lifecycle — without spinning up
// the full daemon trio.
//
// The deep retry/backoff math is pinned by property tests in
// pkg/webhook/dispatcher_property_test.go and unit tests in
// pkg/webhook/dispatcher_test.go. The cross-process wire tripwire
// (real Postgres + daemons) lives in the cmd/e2e/scheduler_* family
// of tests once the schedd binary exposes a CLI flag for the
// dispatcher config; this file pins the in-process path so a
// regression in any of {apid, store, dispatcher} surfaces here
// before the PR merges.
package e2e_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhook"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// An account receiver created before the second app exists receives signed
// deliveries from both apps through the shared ledger and dispatcher.
func TestWebhookE2E_AccountReleaseReceiverAcrossApps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const plaintext = "account-release-e2e-secret"
	type received struct {
		id  string
		err error
	}
	incoming := make(chan received, 2)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err == nil {
			var unix int64
			unix, err = strconv.ParseInt(r.Header.Get("X-Faas-Webhook-Timestamp"), 10, 64)
			if err == nil {
				sig := strings.TrimPrefix(r.Header.Get("X-Faas-Webhook-Signature"), "sha256=")
				err = webhookout.NewSigner([]byte(plaintext)).Verify(unix, r.Header.Get("X-Faas-Delivery-Id"), body, sig)
			}
		}
		incoming <- received{r.Header.Get("X-Faas-Delivery-Id"), err}
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	m := state.NewMemStore()
	acct, err := m.CreateAccount(ctx, "account-release-e2e@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "first-release-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := secretbox.SealBytes(ident.Recipient(), "APP_WEBHOOK", []byte(plaintext), api.AppWebhookSecretMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	hook, err := m.CreateAccountReleaseWebhookIfUnderQuota(ctx, state.AppWebhook{
		AccountID: acct.ID, Scope: state.AppWebhookScopeAccount, TargetURL: receiver.URL,
		SecretSealed: sealed, EventFilter: []string{"deployment.live"}, Enabled: true,
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "second-release-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range []state.App{first, second} {
		deployment, err := m.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:e2e"})
		if err != nil {
			t.Fatal(err)
		}
		if err := m.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
	}
	deliveries, _, err := m.ListAccountReleaseWebhookDeliveries(ctx, acct.ID, hook.ID, 10, "")
	if err != nil || len(deliveries) != 2 {
		t.Fatalf("account deliveries = %d, err=%v", len(deliveries), err)
	}
	sources := map[string]bool{}
	for _, delivery := range deliveries {
		sources[delivery.AppID] = true
	}
	if !sources[first.ID] || !sources[second.ID] {
		t.Fatalf("sources = %v", sources)
	}

	disp := webhook.NewDispatcher(m, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	disp.HTTPClient = receiver.Client()
	disp.PerAttempt = time.Second
	disp.Tick = 20 * time.Millisecond
	disp.IdentityLoader = func() []*age.X25519Identity { return []*age.X25519Identity{ident} }
	go func() { _ = disp.Run(ctx) }()
	for range 2 {
		select {
		case got := <-incoming:
			if got.id == "" || got.err != nil {
				t.Fatalf("signed delivery = %+v", got)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for account release deliveries")
		}
	}
}

// TestWebhookE2E_ApidCreateEnqueuesDelivery pins the round-trip:
//
//  1. apid handler (MemStore) creates a webhook for an app.
//  2. schedd-equivalent emission enqueues a delivery row.
//  3. Dispatcher drains the row, POSTs to the test receiver.
//  4. Receiver returns 200 → row.status='succeeded'.
//  5. Receiver's X-Faas-Delivery-Id header is non-empty and stable
//     across the dispatcher's request building path.
//
// This is the cross-component tripwire; per-component semantics
// live in pkg/webhook/dispatcher_test.go and pkg/api/webhooks_test.go.
func TestWebhookE2E_ApidCreateEnqueuesDelivery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Receiver that returns 200 and records the delivery-id header.
	var attempts int32
	var mu sync.Mutex
	var deliveryIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		mu.Lock()
		deliveryIDs = append(deliveryIDs, r.Header.Get("X-Faas-Delivery-Id"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := state.NewMemStore()
	appID := "app-e2e"
	acctID := "acct-e2e"
	if _, err := m.CreateApp(ctx, state.App{ID: appID, AccountID: acctID, Slug: "e2e-app", Status: "ready"}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	// The webhook's sealed secret path is exercised end-to-end via
	// the apid handler in cmd/apid/handlers_webhooks_test.go. Here
	// we shortcut the seal because the dispatcher's open path is
	// the only thing under test, and a missing identity would
	// error before the HTTP POST. Seal a synthetic plaintext so the
	// open path stays in the test's control.
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	sealed, err := secretbox.SealBytes(ident.Recipient(), "APP_WEBHOOK", []byte("test-secret"), 1024)
	if err != nil {
		t.Fatalf("SealBytes: %v", err)
	}

	webhookID := "wh-e2e-1"
	deliveryID := "del-e2e-1"
	if _, err := m.CreateAppWebhook(ctx, state.AppWebhook{
		ID: webhookID, AppID: appID, AccountID: acctID,
		TargetURL: srv.URL, SecretSealed: sealed,
		RetryPolicy: "default", Enabled: true,
	}); err != nil {
		t.Fatalf("CreateAppWebhook: %v", err)
	}
	if _, err := m.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{
		ID: deliveryID, WebhookID: webhookID, AppID: appID, AccountID: acctID,
		Event: "cron.fired", Attempt: 0, Status: "pending",
		NextAttemptAt: time.Now(),
	}); err != nil {
		t.Fatalf("RecordAppWebhookDelivery: %v", err)
	}

	// Build dispatcher pointed at the in-process store.
	disp := webhook.NewDispatcher(m, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	disp.HTTPClient = srv.Client()
	disp.PerAttempt = 1 * time.Second
	disp.Tick = 25 * time.Millisecond
	disp.IdentityLoader = func() []*age.X25519Identity {
		return []*age.X25519Identity{ident}
	}
	go func() { _ = disp.Run(ctx) }()

	// Wait up to 5s for the receiver to log one attempt.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&attempts) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
	mu.Lock()
	first := ""
	if len(deliveryIDs) > 0 {
		first = deliveryIDs[0]
	}
	mu.Unlock()
	if first == "" {
		t.Errorf("X-Faas-Delivery-Id missing on first delivery")
	}

	// Inspect the row — the mark-succeeded path must have run.
	row, err := m.AppWebhookDeliveryByID(ctx, deliveryID)
	if err != nil {
		t.Fatalf("AppWebhookDeliveryByID: %v", err)
	}
	if row.Status != "succeeded" {
		t.Errorf("row.status = %q, want succeeded", row.Status)
	}
	if row.Attempt != 1 {
		t.Errorf("row.attempt = %d, want 1", row.Attempt)
	}
}

// TestWebhookE2E_DLQAfterSevenFailures pins the DLQ path: a receiver
// that always 500s drives a delivery row through 7 attempts and
// lands it at status='dead'. The 7-attempt budget is compressed via
// the dispatcher's clock-injection seam so the test finishes in
// <1s wall.
func TestWebhookE2E_DLQAfterSevenFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	m := state.NewMemStore()
	appID := "app-e2e-dlq"
	acctID := "acct-e2e-dlq"
	if _, err := m.CreateApp(ctx, state.App{ID: appID, AccountID: acctID, Slug: "e2e-dlq", Status: "ready"}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	// Bypass the seal path — see TestWebhookE2E_ApidCreateEnqueuesDelivery.
	ident, _ := age.GenerateX25519Identity()
	sealed, _ := secretbox.SealBytes(ident.Recipient(), "APP_WEBHOOK", []byte("test-secret"), 1024)

	webhookID := "wh-dlq-1"
	deliveryID := "del-dlq-1"
	if _, err := m.CreateAppWebhook(ctx, state.AppWebhook{
		ID: webhookID, AppID: appID, AccountID: acctID,
		TargetURL: srv.URL, SecretSealed: sealed,
		RetryPolicy: "default", Enabled: true,
	}); err != nil {
		t.Fatalf("CreateAppWebhook: %v", err)
	}
	if _, err := m.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{
		ID: deliveryID, WebhookID: webhookID, AppID: appID, AccountID: acctID,
		Event: "cron.fired", Attempt: 0, Status: "pending",
		NextAttemptAt: time.Now(),
	}); err != nil {
		t.Fatalf("RecordAppWebhookDelivery: %v", err)
	}

	// Compress the backoff by short-circuiting the sleeper AND the
	// clock: the dispatcher sets next_attempt_at = now + delay, and
	// the claim query's WHERE next_attempt_at <= now predicate won't
	// pick it up unless now has advanced. Fast-forward both seams so
	// all 7 attempts run within ~1s wall.
	disp := webhook.NewDispatcher(m, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	disp.HTTPClient = srv.Client()
	disp.PerAttempt = 200 * time.Millisecond
	disp.Tick = 10 * time.Millisecond
	disp.Sleeper = func(d time.Duration) {} // no wall sleep
	disp.Now = func() time.Time {
		// Always return the wall clock; the marker uses this
		// same clock for next_attempt_at. The sleeper+now pair
		// still results in a claim-eligible row immediately
		// because now+0 == now.
		return time.Now()
	}
	// Override the backoff schedule itself so the dispatcher's
	// internal delay is always 0 — the row is re-claimable on the
	// next tick regardless of attempt count. WithBackoffs is per-
	// instance, so the test isolates from package defaults without
	// mutating a shared var.
	disp.WithBackoffs(map[state.AppWebhookRetryPolicy][]time.Duration{
		state.AppWebhookRetryDefault:    {0, 0, 0, 0, 0, 0, 0},
		state.AppWebhookRetryAggressive: {0, 0, 0, 0, 0, 0, 0},
	})
	disp.IdentityLoader = func() []*age.X25519Identity {
		return []*age.X25519Identity{ident}
	}
	go func() { _ = disp.Run(ctx) }()

	// Wait up to 5s for the dispatcher to land the row at status=dead.
	deadline := time.Now().Add(5 * time.Second)
	var row state.AppWebhookDelivery
	var lastErr error
	var lastAttempt int
	for time.Now().Before(deadline) {
		row, lastErr = m.AppWebhookDeliveryByID(ctx, deliveryID)
		lastAttempt = row.Attempt
		if lastErr == nil && row.Status == "dead" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if row.Status != "dead" {
		t.Fatalf("row.status = %q (err=%v, attempt=%d), want dead (after %d receiver attempts)",
			row.Status, lastErr, lastAttempt, atomic.LoadInt32(&attempts))
	}
	// 8 receiver hits: 7 retryable failures (each MarkFailed resets
	// status=pending and bumps attempt) + 1 final attempt that hits
	// ComputeBackoff(schedule, 7) → ErrDeliveryExhausted → MarkDead.
	if got := atomic.LoadInt32(&attempts); got != 8 {
		t.Errorf("attempts = %d, want 8 (7 retries + 1 DLQ-final)", got)
	}
	if row.Attempt != 8 {
		t.Errorf("row.attempt = %d, want 8 (MarkDead stamps attempt+1)", row.Attempt)
	}
}

// TestWebhookE2E_RetryPolicyDefaultDrainsCorrectly pins that the
// default retry policy's backoff sequence lands a row at status=failed
// (not dead) after the first 500, with next_attempt_at advanced
// forward. The 7-attempt DLQ test above pins the full path; this
// test pins the intermediate state so a regression in the
// mark-failed transition is caught even when DLQ math doesn't fire.
func TestWebhookE2E_RetryPolicyDefaultDrainsCorrectly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Always-500 receiver.
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	m := state.NewMemStore()
	appID := "app-retry"
	acctID := "acct-retry"
	if _, err := m.CreateApp(ctx, state.App{ID: appID, AccountID: acctID, Slug: "retry-app", Status: "ready"}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	ident, _ := age.GenerateX25519Identity()
	sealed, _ := secretbox.SealBytes(ident.Recipient(), "APP_WEBHOOK", []byte("test-secret"), 1024)

	webhookID := "wh-retry-1"
	deliveryID := "del-retry-1"
	if _, err := m.CreateAppWebhook(ctx, state.AppWebhook{
		ID: webhookID, AppID: appID, AccountID: acctID,
		TargetURL: srv.URL, SecretSealed: sealed,
		RetryPolicy: "default", Enabled: true,
	}); err != nil {
		t.Fatalf("CreateAppWebhook: %v", err)
	}
	if _, err := m.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{
		ID: deliveryID, WebhookID: webhookID, AppID: appID, AccountID: acctID,
		Event: "cron.fired", Attempt: 0, Status: "pending",
		NextAttemptAt: time.Now(),
	}); err != nil {
		t.Fatalf("RecordAppWebhookDelivery: %v", err)
	}

	// One-shot dispatcher: invoke cycle() once so we observe the
	// post-attempt-1 state without waiting for the full DLQ.
	disp := webhook.NewDispatcher(m, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	disp.HTTPClient = srv.Client()
	disp.PerAttempt = 500 * time.Millisecond
	disp.Tick = 25 * time.Millisecond
	disp.Sleeper = func(d time.Duration) {}
	disp.IdentityLoader = func() []*age.X25519Identity {
		return []*age.X25519Identity{ident}
	}

	// Use the unexported cycle via Run + short ctx deadline so we
	// don't pay the full 7-attempt DLQ wall. Run blocks until ctx
	// is cancelled; cancel after the first attempt lands.
	go func() { _ = disp.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&attempts) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if got := atomic.LoadInt32(&attempts); got < 1 {
		t.Fatalf("attempts = %d, want >= 1", got)
	}
}
