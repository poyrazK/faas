//go:build !no_pg

// Package e2e — paddle_e2e_test.go is the §14 M7 acceptance gate for
// the Paddle billing provider's webhook ingress (PR #3 / ADR-025).
// Boots a real apid subprocess against a real Postgres with
// FAAS_BILLING_PROVIDER=paddle, signs webhook bodies with the same
// HMAC-SHA256 the production paddle.Provider.VerifyWebhook checks,
// and asserts the dunning state machine flips on the right events.
//
// This file is the e2e complement to cmd/apid/handlers_change_plan_test.go
// (unit matrix) and pkg/billing/paddle/upgrade_test.go (provider unit
// tests). The unit tests pin the wire shape; this e2e proves the apid
// daemon actually wires the provider through end-to-end against a real
// Postgres-backed state.Store — the spec's load-bearing test surface.
//
// Cases:
//   - signed transaction.paid on a past_due account → status flips to
//     active within 5s.
//   - signed transaction.payment_failed on an active account → status
//     flips to past_due within 5s.
//   - POST with bad HMAC → 400 + RFC 7807 problem with code=validation_failed.
//
// To skip locally: export FAAS_SKIP_PG_TESTS=1.

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/paddle"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	paddleTestAPIKey    = "pdl_test_e2e_loader"
	paddleTestSecret    = "whk_test_e2e_loader_secret_value"
	paddleTestCtmID     = "ctm_test_e2e"
	paddleWebhookPath   = "/v1/webhooks/paddle"
	paddleTestTolerance = 5 * time.Second
)

// postSignedPaddle signs the body with paddle.SignForTestForTest
// (matches the production VerifyWebhook HMAC-SHA256 + ts scheme) and
// POSTs it to the apid harness. Returns the response status + body
// so the caller can assert on either.
//
// The body shape mirrors the Paddle webhook payload VerifyWebhook
// expects — a JSON object with `event_type` + `data` nested. apid
// never reads this directly; the paddle.Provider normalizes the
// payload into billing.Event via VerifyWebhook and apid's dunning
// state machine dispatches on the normalized event_type enum.
func postSignedPaddle(t *testing.T, apidURL, secret string, body map[string]any) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	ts := time.Now()
	sig := paddle.SignForTestForTest(raw, secret, ts)
	url := apidURL + paddleWebhookPath
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Paddle-Signature", sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, respBody
}

// startAPIDForPaddle boots apid with Paddle env wiring. Returns the
// harness so callers can read H.APIDURL. Keeps the env-var setup in
// one place — three tests would otherwise each carry the same 5-line
// StartWithEnv call.
func startAPIDForPaddle(t *testing.T, pool *pgxpool.Pool) *e2etest.Harness {
	t.Helper()
	return e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_BILLING_PROVIDER=paddle",
		"FAAS_PADDLE_API_KEY=" + paddleTestAPIKey,
		"FAAS_PADDLE_WEBHOOK_SECRET=" + paddleTestSecret,
		"FAAS_PADDLE_SANDBOX=1",
	})
}

// TestPaddle_TransactionPaid_RestoresPastDueToActive is the dunning
// recovery acceptance gate. Seeds a past_due account, posts a signed
// transaction.paid, polls the row for status=active within 5s.
//
// meterd isn't running here (apid-only boot is enough to prove the
// webhook dispatch) — the flip happens synchronously inside the
// webhook handler. The 5s bound is generous to absorb CI noise.
func TestPaddle_TransactionPaid_RestoresPastDueToActive(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}

	h := startAPIDForPaddle(t, pool)
	store := state.NewPgStore(pool)

	// Seed an account in past_due state with a customer id that
	// matches the webhook's data.customer_id. The pre-PR-#3
	// stripe_customer_id column is reused per ADR-025 — the same
	// column carries the Paddle ctm_… id.
	acct, err := store.CreateAccount(ctx, "paddle-paid@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := store.UpdateAccountStatus(ctx, acct.ID, state.AccountPastDue); err != nil {
		t.Fatalf("UpdateAccountStatus: %v", err)
	}
	if err := store.UpdateAccountPaddleCustomerID(ctx, acct.ID, paddleTestCtmID); err != nil {
		t.Fatalf("UpdateAccountPaddleCustomerID: %v", err)
	}

	body := map[string]any{
		"event_type": "transaction.paid",
		"data": map[string]any{
			"customer_id": paddleTestCtmID,
			"status":      "paid",
		},
	}
	status, respBody := postSignedPaddle(t, h.APIDURL, paddleTestSecret, body)
	if status != http.StatusOK {
		t.Fatalf("webhook status = %d, want 200\nbody = %s", status, respBody)
	}

	deadline := time.Now().Add(paddleTestTolerance)
	for {
		got, err := store.AccountByID(ctx, acct.ID)
		if err != nil {
			t.Fatalf("AccountByID: %v", err)
		}
		if got.Status == state.AccountActive {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("status stayed at %q after %s; want %q", got.Status, paddleTestTolerance, state.AccountActive)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestPaddle_TransactionPaymentFailed_FlipsActiveToPastDue is the
// dunning entry-point acceptance gate. Seeds an active account with a
// Paddle customer id, posts a signed transaction.payment_failed,
// asserts status flips to past_due.
//
// Like TestPaddle_TransactionPaid_RestoresPastDueToActive, this
// exercises the apid-side webhook → dunning state machine surface;
// meterd's 7-day timer is a separate acceptance gate (see
// meterd_dunning_e2e_test.go).
func TestPaddle_TransactionPaymentFailed_FlipsActiveToPastDue(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}

	h := startAPIDForPaddle(t, pool)
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(ctx, "paddle-failed@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := store.UpdateAccountPaddleCustomerID(ctx, acct.ID, paddleTestCtmID); err != nil {
		t.Fatalf("UpdateAccountPaddleCustomerID: %v", err)
	}

	body := map[string]any{
		"event_type": "transaction.payment_failed",
		"data": map[string]any{
			"customer_id": paddleTestCtmID,
		},
	}
	status, respBody := postSignedPaddle(t, h.APIDURL, paddleTestSecret, body)
	if status != http.StatusOK {
		t.Fatalf("webhook status = %d, want 200\nbody = %s", status, respBody)
	}

	deadline := time.Now().Add(paddleTestTolerance)
	for {
		got, err := store.AccountByID(ctx, acct.ID)
		if err != nil {
			t.Fatalf("AccountByID: %v", err)
		}
		if got.Status == state.AccountPastDue {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("status stayed at %q after %s; want %q", got.Status, paddleTestTolerance, state.AccountPastDue)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestPaddle_MalformedSignature_RejectsAt400 is the webhook signature
// surface. POSTs a body with a Paddle-Signature header that doesn't
// match the secret's HMAC; apid must 400 with the RFC 7807
// code=validation_failed envelope. Status: 200 is a bug (would mean
// apid is processing unauthenticated events — security incident).
func TestPaddle_MalformedSignature_RejectsAt400(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}

	h := startAPIDForPaddle(t, pool)

	body := map[string]any{
		"event_type": "transaction.paid",
		"data":       map[string]any{"customer_id": paddleTestCtmID},
	}
	raw, _ := json.Marshal(body)
	// Sign with the WRONG secret — should fail VerifyWebhook.
	badSig := paddle.SignForTestForTest(raw, "whk_wrong_secret", time.Now())

	url := h.APIDURL + paddleWebhookPath
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Paddle-Signature", badSig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\nbody = %s", resp.StatusCode, respBody)
	}
	var prob api.Problem
	if err := json.Unmarshal(respBody, &prob); err != nil {
		t.Fatalf("body not problem+json: %s", respBody)
	}
	if prob.Code != api.CodeValidation {
		t.Errorf("Code = %q, want %q", prob.Code, api.CodeValidation)
	}
}
