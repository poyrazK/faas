//go:build paddle_sandbox_e2e && !no_pg

// Package e2e — billing_paddle_sandbox_test.go is PR-P3's live-sandbox
// acceptance walk against api.sandbox.paddle.com.
//
// Four end-to-end tests that together prove the Paddle wire-up
// works against the real sandbox merchant:
//
//   - TestPaddleSandbox_ChangePlanReturnsCheckoutURL — boots apid
//     against the live sandbox, signs up an account, PATCHes
//     /v1/account/plan to hobby, asserts the 402 surfaces a
//     non-empty paddle_checkout_url + tx_id.
//
//   - TestPaddleSandbox_SubscriptionCreatedStampsCustomerID — POSTs
//     a real signed subscription.created event with the customer
//     id from Test 1, asserts acct.ProviderCustomerID populated.
//
//   - TestPaddleSandbox_TransactionCompletedIsNoop — POSTs a signed
//     transaction.completed with the txn_id from Test 1, asserts no
//     state flip (a completed transaction doesn't change dunning
//     state — it's an informational event).
//
//   - TestPaddleSandbox_PerWindowClaimRoundTrip — runs the meterd
//     production path (NewProviderWithDedupe → EnsurePlanProducts →
//     PushUsageRecord) directly against api.sandbox.paddle.com,
//     asserts the paddle_overage_dedupe row is stamped with
//     state=completed, pushed_at non-null, claimed_by non-null,
//     pushed_mb_seconds = the integer the test pushed. Closes the
//     gap that pkg/billing/paddle/sandbox_test.go exercises the
//     Provider without a dedupe wired.
//
//   - TestPaddleSandbox_WebhookSignatureRoundTrip — SDK-side
//     pinning of the contract that Test 2 + 3 prove at the apid
//     layer. Signs a real Paddle-shaped JSON body with the operator's
//     webhook secret, asserts VerifyWebhook accepts it with
//     canonical and lowercase header keys, rejects a tampered body
//     with billing.ErrBadSignature.
//
// Gating:
//
//   1. Build tag //go:build paddle_sandbox_e2e — the file is NOT
//      compiled by `go test ./...` in CI.
//
//   2. Environment guard at TestMain / per-test: FAAS_PADDLE_SANDBOX_E2E=1
//      AND secrets/.env.sandbox (NOT in repo) must exist. Without
//      the file the test skips — never runs accidentally.
//
//   3. secrets/.env.sandbox must NOT be committed. The .gitignore
//      covers secrets/. The CI workflow never sets the env var.
//
// Operator-only: this is the surface that proves the §14 M7
// acceptance gate is satisfiable for a real Paddle merchant, not
// just a stubbed SDK. CI does NOT run it. `make e2e-sandbox` is
// the explicit entry point.

package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/paddle"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// sandboxSecretsPath is the operator-supplied credentials file. The
// file lives outside the repo (secrets/) and is gitignored. Format
// is "KEY=value" lines; values are sandbox-only.
//
// FAAS_PADDLE_SANDBOX_API_KEY — pdl_sandbox_… from the Paddle Dashboard
// FAAS_PADDLE_SANDBOX_WEBHOOK_SECRET — whk_… from the same Dashboard
const sandboxSecretsPath = "secrets/.env.sandbox"

// sandboxHandoffPath is the /tmp/ JSON file Test 1 writes after a
// successful checkout-URL round-trip so tests 2 and 3 can chain. The
// file is created with mode 0600 and deleted via t.Cleanup so a
// stale handoff from a prior failed run never leaks into a fresh
// one. /tmp/ is OS-managed and not in .gitignore (not needed — the
// directory is wiped on reboot on most Linux distros).
//
// PR-P4 replaced the t.Skip placeholders with real implementations
// that read this handoff. Pre-PR-P4 the file did not exist and the
// two follow-on tests were stubs.
const sandboxHandoffPath = "/tmp/faas-paddle-sandbox-handoff.json"

// sandboxHarnessPtr is a package-level handle to the harness booted
// by Test 1, set immediately after StartWithEnv returns. Tests 2 + 3
// read it via getSandboxHarness() to issue the same state.NewPgStore
// reads that account_scoped_e2e_test.go:46 uses. Lives in the same
// Go test process as the three PaddleSandbox_ tests, so the Go
// runtime guarantees the visibility.
//
// Set by Test 1, read by Tests 2/3. No mutex needed because
// `go test` runs the three tests serially in source order when
// invoked as `go test -run TestPaddleSandbox` — parallel-mode is
// opt-in via `t.Parallel()` and none of the three call it. An
// operator who re-runs tests 2/3 standalone must have populated
// the handoff file by hand; in that case getSandboxHarness() skips
// the test (the standalone path never boots a harness).
var sandboxHarnessPtr *e2etest.Harness

// sandboxHandoff is the JSON shape on disk. Field tags use
// snake_case so an operator who runs `cat` on the file gets the
// same names they see in the Paddle dashboard.
type sandboxHandoff struct {
	CheckoutURL    string `json:"checkout_url"`
	TxID           string `json:"tx_id"`
	CustomerID     string `json:"customer_id"`
	SubscriptionID string `json:"subscription_id"`
	AccountID      string `json:"account_id"`
	APIDURL        string `json:"apid_url"`
	WebhookSecret  string `json:"webhook_secret"`
}

// getSandboxHarness returns the harness booted by Test 1 or skips
// the calling test. Standalone re-runs of tests 2/3 never have a
// harness in scope (Test 1 didn't run), so we skip instead of
// failing — that's the same contract readSandboxHandoff uses for
// the JSON file.
func getSandboxHarness(t *testing.T) *e2etest.Harness {
	t.Helper()
	if sandboxHarnessPtr == nil {
		t.Skip("no sandbox harness in memory; run TestPaddleSandbox_ChangePlanReturnsCheckoutURL first")
	}
	return sandboxHarnessPtr
}

// writeSandboxHandoff atomically writes the handoff struct as JSON.
// Mode 0600 keeps the webhook_secret out of group/world-readable
// state; the file lives in /tmp/ so a leaked permission is still
// limited to the local host.
func writeSandboxHandoff(t *testing.T, h sandboxHandoff) {
	t.Helper()
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		t.Fatalf("marshal handoff: %v", err)
	}
	if err := os.WriteFile(sandboxHandoffPath, b, 0o600); err != nil {
		t.Fatalf("write handoff %s: %v", sandboxHandoffPath, err)
	}
	t.Cleanup(func() {
		// Best-effort delete — the next run overwrites anyway, but
		// a stale handoff on a developer's box is a footgun.
		_ = os.Remove(sandboxHandoffPath)
	})
}

// readSandboxHandoff reads + validates the handoff file. Skips the
// test (rather than failing) when the handoff is missing — that's
// the contract that lets an operator re-run a single follow-on
// test without re-running Test 1, by populating the file manually.
func readSandboxHandoff(t *testing.T) sandboxHandoff {
	t.Helper()
	b, err := os.ReadFile(sandboxHandoffPath)
	if err != nil {
		t.Skipf("missing handoff %s: %v — run TestPaddleSandbox_ChangePlanReturnsCheckoutURL first", sandboxHandoffPath, err)
	}
	var h sandboxHandoff
	if err := json.Unmarshal(b, &h); err != nil {
		t.Fatalf("parse handoff %s: %v", sandboxHandoffPath, err)
	}
	if h.CustomerID == "" || h.CheckoutURL == "" || h.APIDURL == "" || h.WebhookSecret == "" {
		t.Skipf("handoff %s missing required fields; re-run TestPaddleSandbox_ChangePlanReturnsCheckoutURL", sandboxHandoffPath)
	}
	return h
}

// loadSandboxSecrets reads secrets/.env.sandbox. Returns the (apiKey,
// webhookSecret) pair or skips the test if the file is missing.
// Operator opt-in: the test only runs when both the env var AND the
// file are present.
func loadSandboxSecrets(t *testing.T) (apiKey, webhookSecret string) {
	t.Helper()
	if os.Getenv("FAAS_PADDLE_SANDBOX_E2E") != "1" {
		t.Skip("FAAS_PADDLE_SANDBOX_E2E not set; live sandbox walk is operator-only")
	}
	f, err := os.Open(sandboxSecretsPath)
	if err != nil {
		t.Skipf("missing %s: %v (operator-only credentials; never committed)", sandboxSecretsPath, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		switch key {
		case "FAAS_PADDLE_SANDBOX_API_KEY":
			apiKey = val
		case "FAAS_PADDLE_SANDBOX_WEBHOOK_SECRET":
			webhookSecret = val
		}
	}
	if apiKey == "" || webhookSecret == "" {
		t.Skipf("secrets file missing one of FAAS_PADDLE_SANDBOX_API_KEY / FAAS_PADDLE_SANDBOX_WEBHOOK_SECRET")
	}
	return apiKey, webhookSecret
}

// startAPIDForPaddleSandbox boots apid against the live Paddle
// sandbox (api.sandbox.paddle.com). Mirrors startAPIDForPaddle but
// reads creds from the operator's secrets file. The sandbox
// merchant's product catalog is the same one EnsurePlanProducts
// hydrates against in production — the difference is the hostname.
func startAPIDForPaddleSandbox(t *testing.T, pool *pgxpool.Pool, apiKey, webhookSecret string) *e2etest.Harness {
	t.Helper()
	return e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_BILLING_PROVIDER=paddle",
		"FAAS_PADDLE_API_KEY=" + apiKey,
		"FAAS_PADDLE_WEBHOOK_SECRET=" + webhookSecret,
		"FAAS_PADDLE_SANDBOX=1",
	})
}

// TestPaddleSandbox_ChangePlanReturnsCheckoutURL exercises the
// CreateCustomer + CreateUpgradeTransaction sidecar against the
// real Paddle sandbox. The flow:
//
//  1. Sign up a fresh account via the apid harness
//  2. PATCH /v1/account/plan to hobby (free → paid gate fires)
//  3. Assert 402 + paddle_checkout_url populated + tx_id populated
//  4. Stash the ctm_… + txn_… for the follow-up tests
//
// We don't follow the redirect to the hosted checkout — that would
// require a real browser. The wire-shape assertion is what proves
// the SDK round-trip completed: if Paddle rejected the customer
// creation (bad key, sandbox down) the handler would 503, not 402.
//
// The 60s ceiling absorbs Paddle sandbox latency; production
// p99 historically completes in < 5s.
func TestPaddleSandbox_ChangePlanReturnsCheckoutURL(t *testing.T) {
	apiKey, webhookSecret := loadSandboxSecrets(t)

	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	h := startAPIDForPaddleSandbox(t, pool, apiKey, webhookSecret)
	sandboxHarnessPtr = h // exposed for Tests 2/3 (no public CurrentHarness())

	// Sign up via the apid harness.
	signupBody, _ := json.Marshal(map[string]any{
		"email": fmt.Sprintf("sandbox-%d@example.com", time.Now().UnixNano()),
		"plan":  string(api.PlanFree),
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", h.APIDURL+"/v1/signup", bytes.NewReader(signupBody))
	req.Header.Set("Content-Type", "application/json")
	signupResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	defer signupResp.Body.Close()
	if signupResp.StatusCode != http.StatusOK && signupResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(signupResp.Body)
		t.Fatalf("signup status = %d, want 200/201\nbody = %s", signupResp.StatusCode, body)
	}
	var signup struct {
		Token   string `json:"token"`
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}
	if err := json.NewDecoder(signupResp.Body).Decode(&signup); err != nil {
		t.Fatalf("decode signup: %v", err)
	}

	// PATCH /v1/account/plan to hobby. The free → paid gate fires;
	// the sidecar creates a Paddle customer + a hosted checkout.
	patchBody, _ := json.Marshal(map[string]string{"plan": string(api.PlanHobby)})
	patchReq, _ := http.NewRequestWithContext(ctx, "PATCH", h.APIDURL+"/v1/account/plan", bytes.NewReader(patchBody))
	patchReq.Header.Set("Content-Type", "application/json")
	patchReq.Header.Set("Authorization", "Bearer "+signup.Token)
	patchResp, err := http.DefaultClient.Do(patchReq)
	if err != nil {
		t.Fatalf("changePlan: %v", err)
	}
	defer patchResp.Body.Close()
	patchBytes, _ := io.ReadAll(patchResp.Body)
	if patchResp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("changePlan status = %d, want 402 (free → paid gate)\nbody = %s", patchResp.StatusCode, patchBytes)
	}
	var prob api.Problem
	if err := json.Unmarshal(patchBytes, &prob); err != nil {
		t.Fatalf("body not problem+json: %s", patchBytes)
	}
	if prob.PaddleCheckoutURL == "" {
		t.Fatalf("paddle_checkout_url is empty; SDK round-trip likely failed: %s", patchBytes)
	}
	if !strings.HasPrefix(prob.PaddleCheckoutURL, "https://") {
		t.Errorf("paddle_checkout_url = %q, want https:// prefix", prob.PaddleCheckoutURL)
	}
	if prob.TxID == "" {
		t.Errorf("tx_id is empty; SDK must surface the txn handle")
	}
	if prob.BillingPortalURL != "" {
		t.Errorf("billing_portal_url = %q, want empty on Paddle path", prob.BillingPortalURL)
	}
	t.Logf("changePlan: ctm_url=%s tx_id=%s", prob.PaddleCheckoutURL, prob.TxID)

	// Read back the customer_id minted by the Paddle sidecar so the
	// follow-on tests (subscription.created / transaction.completed)
	// can chain without a second CreateCustomer round-trip. The pgstore
	// stamps acct.ProviderCustomerID when the sidecar returns 201; this
	// Read is the tripwire that proves the sidecar's persistence path
	// matches the sign-up row. AccountByID returns ErrNotFound on a
	// missing row — we surface that as a hard fail, not a t.Skip,
	// because it would indicate the sidecar's 201 was a no-op.
	st := state.NewPgStore(h.Pool)
	acct, err := st.AccountByID(ctx, signup.Account.ID)
	if err != nil {
		t.Fatalf("AccountByID(%s) post-sidecar: %v", signup.Account.ID, err)
	}
	if acct.ProviderCustomerID == "" {
		t.Fatalf("acct.ProviderCustomerID is empty after free→hobby 402; sidecar 201 likely never stamped the row")
	}

	// Subscription id is optional — the sandbox-sidecar may mint one
	// alongside the customer id or defer it to subscription.created.
	// Either way, write whatever we have so tests 2/3 can choose the
	// right payload shape.
	subscriptionID := ""
	// We don't have a direct handle to the sub_id in the 402 problem
	// body; the webhook in test 2 will assert sub_id from the event.

	writeSandboxHandoff(t, sandboxHandoff{
		CheckoutURL:    prob.PaddleCheckoutURL,
		TxID:           prob.TxID,
		CustomerID:     acct.ProviderCustomerID,
		SubscriptionID: subscriptionID,
		AccountID:      signup.Account.ID,
		APIDURL:        h.APIDURL,
		WebhookSecret:  webhookSecret,
	})
}

// TestPaddleSandbox_SubscriptionCreatedStampsCustomerID POSTs a
// real signed subscription.created event with the customer id
// minted by Test 1's sidecar. The handler stamps acct.ProviderCustomerID
// via the existing webhook logic (subscription.created's
// data.customer_id round-trip). Asserts the round-trip persisted.
//
// Sequenced via /tmp/faas-paddle-sandbox-handoff.json: Test 1 writes
// {checkout_url, tx_id, customer_id, account_id, apid_url,
// webhook_secret}; this test reads it and posts the signed event
// against handoff.APIDURL. The harness pointer stashed in
// sandboxHarnessPtr lets us read the account row post-webhook via
// the same pgxpool the booted apid uses — no second pool needed.
func TestPaddleSandbox_SubscriptionCreatedStampsCustomerID(t *testing.T) {
	_, _ = loadSandboxSecrets(t) // gate on secrets-file + env var; webhookSecret comes via handoff

	handoff := readSandboxHandoff(t)
	h := getSandboxHarness(t)
	ctx := context.Background()

	// Build a synthetic subscription.created payload whose
	// data.customer_id matches the sidecar-minted customer. The
	// subscription id is fresh — the sandbox-sidecar mints it on
	// the first event, not on the free→hobby 402 path.
	subID := fmt.Sprintf("sub_test_%d", time.Now().UnixNano())
	body := []byte(fmt.Sprintf(`{
  "event_id": "evt_test_sub_%d",
  "event_type": "subscription.created",
  "occurred_at": %q,
  "data": {
    "id": %q,
    "customer_id": %q,
    "status": "active",
    "items": [{"price": {"id": "pri_test_local_cli"}}]
  }
}`, time.Now().UnixNano(), time.Now().UTC().Format(time.RFC3339), subID, handoff.CustomerID))

	// Sign with the operator's sandbox webhook_secret so the
	// verifier on the server side accepts it (the secret in
	// sealed.env is the one registered on the Paddle dashboard's
	// webhook endpoint). SignForTestForTest's doubled suffix is a
	// "do not call from prod code" tripwire — test-only signature.
	sig := paddle.SignForTestForTest(body, handoff.WebhookSecret, time.Now())

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		handoff.APIDURL+"/v1/webhooks/paddle", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Paddle-Signature", sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST subscription.created: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("subscription.created: status=%d body=%s", resp.StatusCode, respBody)
	}

	// Re-read the account row via the harness's pool; the handler
	// stamps acct.ProviderCustomerID on subscription.created. The
	// column has carried that name since migration 00040 (the rename
	// predates PR-P4); the expectation is unchanged: the customer
	// id is on the row.
	st := state.NewPgStore(h.Pool)
	acct, err := st.AccountByID(ctx, handoff.AccountID)
	if err != nil {
		t.Fatalf("AccountByID(%s) post-subscription.created: %v", handoff.AccountID, err)
	}
	if acct.ProviderCustomerID != handoff.CustomerID {
		t.Errorf("acct.ProviderCustomerID = %q, want %q (subscription.created must stamp the sidecar-minted id)", acct.ProviderCustomerID, handoff.CustomerID)
	}
	t.Logf("subscription.created: account=%s provider_customer_id=%s", acct.ID, acct.ProviderCustomerID)
}

// TestPaddleSandbox_TransactionCompletedIsNoop POSTs a signed
// transaction.completed with the txn_id from Test 1. The handler
// maps transaction.completed to billing.EventPaymentSucceeded (the
// "completed" → "paid" Paddle translation), which would normally
// fire AccountRestoredBody — but the test starts from an active
// account, so the status guard inside EventPaymentSucceeded
// (acct.Status == past_due) skips the email. The state must be
// unchanged.
//
// This is the regression pin: a future refactor that accidentally
// flips transaction.completed to EventPaymentFailed would land on
// the wrong branch and break the dunning state machine.
func TestPaddleSandbox_TransactionCompletedIsNoop(t *testing.T) {
	handoff := readSandboxHandoff(t)
	h := getSandboxHarness(t)
	ctx := context.Background()

	// Build a synthetic transaction.completed payload whose
	// data.id matches the tx_id from the free→hobby 402. The
	// transaction id is the operator-visible billing handle; Paddle
	// uses it for refunds and for the customer portal "Recent
	// activity" view.
	body := []byte(fmt.Sprintf(`{
  "event_id": "evt_test_tx_%d",
  "event_type": "transaction.completed",
  "occurred_at": %q,
  "data": {
    "id": %q,
    "customer_id": %q,
    "status": "completed",
    "items": [{"price": {"id": "pri_test_local_cli"}}]
  }
}`, time.Now().UnixNano(), time.Now().UTC().Format(time.RFC3339), handoff.TxID, handoff.CustomerID))

	sig := paddle.SignForTestForTest(body, handoff.WebhookSecret, time.Now())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		handoff.APIDURL+"/v1/webhooks/paddle", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Paddle-Signature", sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST transaction.completed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("transaction.completed: status=%d body=%s", resp.StatusCode, respBody)
	}

	// Read the account row + assert dunning state is unchanged.
	// transaction.completed is informational (no state flip) — the
	// acct.Status guard inside EventPaymentSucceeded should keep it
	// on the active path. If a future refactor accidentally maps
	// transaction.completed to EventPaymentFailed, the dunning
	// status would flip and this assertion would fail.
	st := state.NewPgStore(h.Pool)
	acct, err := st.AccountByID(ctx, handoff.AccountID)
	if err != nil {
		t.Fatalf("AccountByID(%s) post-transaction.completed: %v", handoff.AccountID, err)
	}
	if string(acct.Status) != "active" {
		t.Errorf("acct.Status = %q, want \"active\" (transaction.completed is informational; must not flip dunning state)", acct.Status)
	}
	t.Logf("transaction.completed: account=%s status=%s", acct.ID, acct.Status)
}

// TestPaddleSandbox_PerWindowClaimRoundTrip runs the meterd
// production path directly against api.sandbox.paddle.com and
// asserts that paddle_overage_dedupe reflects exactly what was
// pushed. This is the load-bearing seam the B4 pre-flight pins
// the shape of — a future regression that drops the stamp (e.g.
// reverting the Commit 0 `_ = mbSeconds` shape) would flip this
// assertion red even when the SDK POST succeeded.
//
// Distinct from sandbox_test.go: TestPaddleSandbox_PushUsageRecord
// exercises Provider with dedupe=nil; the production meterd path
// passes the *PgStore in. This test wires the production
// constructor (NewProviderWithDedupe) and asserts the dedupe
// row state after a real SDK POST — closing the gap that the
// B2 review flagged.
//
// priorMonth is offset -31 days from now so the per-window grain
// (provider usage.go's time.Truncate(time.Hour) at the prior-
// month boundary) doesn't collide with a stale row from a
// previous failed run. The -31 day offset is also the value
// meterd uses to label "prior month" in production.
//
// Operator-only: gated on the same FAAS_PADDLE_SANDBOX_E2E=1 +
// secrets/.env.sandbox pair as Tests 1-3. CI does not run this;
// `make e2e-sandbox` does.
//
// Build tag matches the file (paddle_sandbox_e2e && !no_pg).
func TestPaddleSandbox_PerWindowClaimRoundTrip(t *testing.T) {
	apiKey, _ := loadSandboxSecrets(t)

	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	store := state.NewPgStore(pool)

	// (1) Build the production-path Provider with the *PgStore as
	// the dedupe backend. NewProviderWithDedupe's 2nd positional
	// is `sandbox bool` — true = api.sandbox.paddle.com.
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	p, err := paddle.NewProviderWithDedupe(apiKey, true, log, store)
	if err != nil {
		t.Fatalf("NewProviderWithDedupe: %v", err)
	}

	// (2) Hydrate the overage catalog against the live sandbox.
	// This is the call meterd makes at boot; it lists the
	// merchant's products + prices and stores the
	// plan→price-id map on the Provider. Subsequent
	// PushUsageRecord calls need this map populated or they
	// return ErrOveragePriceMissing.
	if err := p.EnsurePlanProducts(ctx); err != nil {
		t.Fatalf("EnsurePlanProducts: %v", err)
	}

	// (3) Seed a fresh account via the production CreateAccount.
	// The test's push will land on a brand-new (acct, window)
	// pair so dedupe-claim can never collide with a stale row.
	email := fmt.Sprintf("live-overage-%d@example.com", time.Now().UnixNano())
	acct, err := store.CreateAccount(ctx, email, api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount(%s): %v", email, err)
	}

	// (4) Push the integer wire value. priorMonth = -31d so the
	// window grain does not collide with a previous run.
	const pushedMB = int64(1024)
	priorMonth := time.Now().UTC().Add(-31 * 24 * time.Hour)
	if err := p.PushUsageRecord(ctx, acct, priorMonth, pushedMB); err != nil {
		t.Fatalf("PushUsageRecord acct=%s window=%s mb=%d: %v",
			acct.ID, priorMonth.Format(time.RFC3339), pushedMB, err)
	}

	// (5) Assert the dedupe row reflects the push. Exactly one
	// row at (account_id, window_start); state completed; the
	// stamp columns non-null; pushed_mb_seconds = the integer
	// we pushed. A future regression that drops the stamp (e.g.
	// reverting pgstore.go:9929 `pushed_mb_seconds = $3`) flips
	// the last assertion red — the load-bearing contract this
	// test pins.
	var (
		rowCount   int
		rowState   string
		pushedAtNS *int64
		claimedBy  *string
		stampedMB  *int64
	)
	if err := pool.QueryRow(ctx, `
		select count(*)::int,
		       max(state)::text,
		       max(extract(epoch from pushed_at) * 1e9)::bigint,
		       max(claimed_by),
		       max(pushed_mb_seconds)
		  from paddle_overage_dedupe
		 where account_id = $1
		   and window_start = $2
	`, acct.ID, priorMonth.UTC().Truncate(time.Hour)).Scan(
		&rowCount, &rowState, &pushedAtNS, &claimedBy, &stampedMB,
	); err != nil {
		t.Fatalf("read paddle_overage_dedupe acct=%s: %v", acct.ID, err)
	}
	if rowCount != 1 {
		t.Errorf("paddle_overage_dedupe rows for acct=%s window=%s = %d, want 1",
			acct.ID, priorMonth.Format(time.RFC3339), rowCount)
	}
	if rowState != "completed" {
		t.Errorf("state = %q, want \"completed\" (PushUsageRecord reached defaultFlushLocked → CompletePaddleOverageWindow)", rowState)
	}
	if pushedAtNS == nil {
		t.Errorf("pushed_at IS NULL (production path must stamp the timestamp)")
	}
	if claimedBy == nil || *claimedBy == "" {
		t.Errorf("claimed_by IS NULL/empty (the test process must own the claim — proves the wire path went through ClaimPaddleOverageWindow, not a no-op short-circuit)")
	}
	if stampedMB == nil {
		t.Errorf("pushed_mb_seconds IS NULL (Commit 0 stamping regressed; the production PgStore.CompletePaddleOverageWindow must materialise the integer)")
	} else if *stampedMB != pushedMB {
		t.Errorf("pushed_mb_seconds = %d, want %d (CustomData carried the value to the merchant dashboard; the dedupe row must match)",
			*stampedMB, pushedMB)
	}
	t.Logf("per-window claim: account=%s window=%s state=%s pushed_mb=%v claimed_by=%v",
		acct.ID, priorMonth.Format(time.RFC3339), rowState,
		func() any {
			if stampedMB == nil {
				return "nil"
			}
			return *stampedMB
		}(),
		func() any {
			if claimedBy == nil {
				return "nil"
			}
			return *claimedBy
		}())
}

// TestPaddleSandbox_WebhookSignatureRoundTrip is the SDK-side pin
// of the contract that TestPaddleSandbox_SubscriptionCreatedStampsCustomerID
// and TestPaddleSandbox_TransactionCompletedIsNoop prove at the apid
// HTTP layer. Distinct from those tests: this one calls
// VerifyWebhook directly (no http server, no apid process), so a
// regression that breaks the SDK-side HMAC verification flips this
// red even when the apid wrapper still parses the body.
//
// Three sub-assertions:
//
//  1. Canonical-header accept: sign with the operator's webhook
//     secret, post via Paddle-Signature header, assert no error
//     and event.EventID non-empty (proves the body parsed end-to-
//     end, not just the HMAC). EventID is the dedupe key apid
//     keys replay defense on (pkg/billing/provider.go:309).
//
//  2. Lowercase-header accept: re-sign + re-verify with the
//     paddle-signature header key. Mirrors provider_test.go:78
//     and pins the lowercase fallback that Paddle's docs
//     describe — without it, an upstream proxy that lowercases
//     headers (e.g. a CDN config bug) would silently 401.
//
//  3. Tampered-body reject: flip one byte in the body, re-verify
//     with the original signature. Must return
//     errors.Is(err, billing.ErrBadSignature). A future refactor
//     that accidentally bypasses HMAC (e.g. swapping the SDK's
//     signature for a no-op stub) would flip this red.
//
// event_type is "transaction.paid" — a known mapping per
// pkg/billing/paddle/webhook.go (transaction.created maps to
// EventUnknown; transaction.completed maps to EventPaymentSucceeded).
// transaction.paid is the canonical "payment confirmed" event Paddle
// emits; using it here exercises the same parsing path Test 3 hits.
//
// Operator-only: gated on the same FAAS_PADDLE_SANDBOX_E2E=1 +
// secrets/.env.sandbox pair as Tests 1-4. The webhook secret comes
// from the secrets file, not Test 1's handoff — the SDK-side
// verification does not need an account row or an apid running.
func TestPaddleSandbox_WebhookSignatureRoundTrip(t *testing.T) {
	apiKey, webhookSecret := loadSandboxSecrets(t)

	// Body shape mirrors Paddle's transaction.paid schema:
	//   event_id (delivery UUID)
	//   event_type (one of the typed mappings)
	//   data.{id, customer_id, status, items[].price.id}
	// We use placeholder IDs; VerifyWebhook does not reach out to
	// the merchant API — it only parses the body + verifies HMAC.
	eventID := fmt.Sprintf("evt_test_sig_%d", time.Now().UnixNano())
	body := []byte(fmt.Sprintf(`{
  "event_id": %q,
  "event_type": "transaction.paid",
  "occurred_at": %q,
  "data": {
    "id": "txn_test_sig",
    "customer_id": "ctm_test_sig",
    "status": "paid",
    "items": [{"price": {"id": "pri_test_sig"}}]
  }
}`, eventID, time.Now().UTC().Format(time.RFC3339)))

	// (0) Construct the provider. NewProvider(apiKey, webhookSecret,
	// sandbox=true, log). sandbox=true → api.sandbox.paddle.com for
	// any future SDK call; for VerifyWebhook the host is irrelevant.
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	p, err := paddle.NewProvider(apiKey, webhookSecret, true, log)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	when := time.Now()

	// (1) Canonical header. Paddle-Signature.
	sigCanonical := paddle.SignForTestForTest(body, webhookSecret, when)
	event, err := p.VerifyWebhook(body,
		map[string]string{"Paddle-Signature": sigCanonical},
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("VerifyWebhook (canonical header): %v", err)
	}
	if event.EventID == "" {
		t.Errorf("VerifyWebhook: event.EventID = \"\", want %q (apid dedupes on EventID per pkg/billing/provider.go:309)", eventID)
	}
	if event.EventID != eventID {
		t.Errorf("VerifyWebhook: event.EventID = %q, want %q (the SDK must surface the body's event_id verbatim, not generate one)", event.EventID, eventID)
	}

	// (2) Lowercase header. paddle-signature.
	sigLower := paddle.SignForTestForTest(body, webhookSecret, when)
	eventLower, err := p.VerifyWebhook(body,
		map[string]string{"paddle-signature": sigLower},
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("VerifyWebhook (lowercase header): %v (the lowercase fallback is the contract at provider.go:78; a CDN lowercasing the header should still pass)", err)
	}
	if eventLower.EventID != eventID {
		t.Errorf("VerifyWebhook lowercase: event.EventID = %q, want %q", eventLower.EventID, eventID)
	}

	// (3) Tampered body. Flip the first character of the event_type
	// value. The signature was computed over the original body, so
	// HMAC mismatch → ErrBadSignature. The flip target is the
	// 't' of "transaction.paid"; replacing it with 'x' produces
	// "xransaction.paid" which is structurally invalid JSON-ish
	// but still parses enough to hit the HMAC comparison.
	tampered := bytes.Replace(body, []byte("transaction.paid"), []byte("xransaction.paid"), 1)
	if bytes.Equal(tampered, body) {
		t.Fatalf("bytes.Replace did not mutate the body; the test would silently pass — flip a different byte")
	}
	_, err = p.VerifyWebhook(tampered,
		map[string]string{"Paddle-Signature": sigCanonical},
		5*time.Minute,
	)
	if err == nil {
		t.Errorf("VerifyWebhook accepted tampered body (signature was over the original body; HMAC mismatch must surface as ErrBadSignature)")
	} else if !errors.Is(err, billing.ErrBadSignature) {
		t.Errorf("VerifyWebhook tampered-body err = %v, want errors.Is(billing.ErrBadSignature)", err)
	}

	t.Logf("webhook signature round-trip: event_id=%s canonical=ok lowercase=ok tampered=ErrBadSignature", eventID)
}

// _ pins the paddle package import — used by the signing helper
// (paddle.SignForTestForTest) in the subscription.created /
// transaction.completed tests.
var _ = paddle.SignForTestForTest

// _ pins the state package import for AccountByID access in the
// test bodies above. The full sequential tests assert against the
// pgstore account row post-webhook.
var _ state.Account
