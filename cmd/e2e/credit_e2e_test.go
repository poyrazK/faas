// Package e2e — cmd/e2e acceptance tests. See cmd/e2e/quota_e2e_test.go
// for the build-tag policy. credit_e2e_test.go is the §14 BILLING gate
// for issue #279: an admin-issued credit lands in account_credits +
// credit_ledger with an audit row, and a per-account overage cap is
// honoured by the meterd quota tick.
//
// These tests boot real daemon subprocesses (apid + meterd) so the
// HTTP path, the SQL writes, and the meterd loop run in the production
// wire — not in-process fakes. The migration race is gated by
// pgtest.WaitForMigration (the harness boot runs the wait before any
// daemon starts; memory: cmd-e2e-schedd-migration-race).
//
// To skip locally: export FAAS_SKIP_PG_TESTS=1.

//go:build !no_pg

package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

func newCreditRefundPolar(t *testing.T) (*httptest.Server, func() int) {
	t.Helper()
	var (
		mu        sync.Mutex
		refundKey string
		posts     int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organizations/":
			_, _ = w.Write([]byte(`{"items":[{"id":"org-1","subscription_settings":{"allow_multiple_subscriptions":false,"proration_behavior":"invoice"},"customer_portal_settings":{"subscription":{"update_plan":false}}}]}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/products/"):
			id := strings.TrimPrefix(r.URL.Path, "/v1/products/")
			plan, ok := map[string]api.Plan{"hobby-product": api.PlanHobby, "pro-product": api.PlanPro, "scale-product": api.PlanScale}[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			fixedCents := api.MustLimitsFor(plan).PriceMillicents / 1000
			_, _ = fmt.Fprintf(w, `{"id":%q,"recurring_interval":"month","recurring_interval_count":1,"is_recurring":true,"is_archived":false,"prices":[{"amount_type":"fixed","price_currency":%q,"price_amount":%d,"is_archived":false},{"amount_type":"metered_unit","price_currency":%q,"unit_amount":"1","meter_id":"meter-1","cap_amount":null,"is_archived":false}],"benefits":[]}`, id, api.BillingCurrencyLower, fixedCents, api.BillingCurrencyLower)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/meters/meter-1":
			_, _ = fmt.Fprint(w, `{"id":"meter-1","unit":"scalar","archived_at":null,"filter":{"conjunction":"and","clauses":[{"property":"name","operator":"eq","value":"ram_usage"}]},"aggregation":{"func":"sum","property":"gb_ram_hours"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/refunds":
			mu.Lock()
			key := refundKey
			mu.Unlock()
			if key == "" {
				_, _ = fmt.Fprint(w, `{"items":[],"pagination":{"max_page":1}}`)
				return
			}
			_, _ = fmt.Fprintf(w, `{"items":[{"id":"refund-e2e","amount":250,"currency":"eur","status":"succeeded","metadata":{"faas_idempotency_key":%q}}],"pagination":{"max_page":1}}`, key)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/refunds":
			var body struct {
				OrderID  string            `json:"order_id"`
				Amount   int64             `json:"amount"`
				Metadata map[string]string `json:"metadata"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if body.OrderID == "" || body.Amount != 250 || body.Metadata["faas_idempotency_key"] == "" {
				t.Errorf("polar refund request = %+v", body)
			}
			mu.Lock()
			refundKey = body.Metadata["faas_idempotency_key"]
			posts++
			mu.Unlock()
			_, _ = fmt.Fprint(w, `{"id":"refund-e2e","amount":250,"currency":"eur","status":"succeeded"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, func() int {
		mu.Lock()
		defer mu.Unlock()
		return posts
	}
}

// TestE2E_CreditIssue_AdminKey — POST /v1/admin/accounts/{id}/credits
// from a verified operator session with the email in FAAS_ADMIN_EMAILS lands a
// row in account_credits + a row in credit_ledger + an audit event of
// kind "credit.issued". Mirrors TestIssueCredit_HappyPath at the
// handler unit-test layer (cmd/apid/handlers_admin_credits_test.go);
// this is the e2e form to prove the wire-up.
func TestE2E_CreditIssue_AdminKey(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	pgtest.WaitForMigration(t, pool, 54, 10*time.Second) // issue #279 PR-A landed at slot 54

	// The admin allowlist is read from FAAS_ADMIN_EMAILS by apid at
	// boot (cmd/apid/main.go:349). The harness seeds accounts whose
	// email is `e2e+<plan>+<label>@test.example`; the admin email below
	// must match what SeedAccount produces for the operator.
	const adminEmail = "e2e+hobby+admin@test.example"
	const targetEmail = "e2e+hobby+credit-target@test.example"
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatalf("session key: %v", err)
	}
	// NewManager deliberately zeroes the caller-owned key slice after
	// copying it into the manager. Preserve the encoded value before
	// construction so the apid subprocess receives the same key used to
	// mint the test cookie.
	keyHex := hex.EncodeToString(keyBytes)
	sessionMgr, err := session.NewManager(keyBytes, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool,
		e2etest.APID,
		[]string{
			"FAAS_ADMIN_EMAILS=" + adminEmail,
			"FAAS_SESSION_KEY=" + keyHex,
		})

	store := state.NewPgStore(pool)

	// Seed the target account (the credit recipient).
	targetAcct, err := store.AccountByEmail(ctx, targetEmail)
	if err != nil {
		// not yet created — create it directly with a Hobby plan.
		targetAcct, err = store.CreateAccount(ctx, targetEmail, api.PlanHobby)
		if err != nil {
			t.Fatalf("seed target account: %v", err)
		}
	}

	// Seed the operator account, then mint the same verified session cookie
	// that the operations console uses. Provider-admin mutations deliberately
	// reject bearer/API-key principals.
	h.SeedAccount(ctx, api.PlanHobby, "admin")
	adminAcct, err := store.AccountByEmail(ctx, adminEmail)
	if err != nil {
		t.Fatalf("load admin account: %v", err)
	}
	sid := uuid.NewString()
	if _, err := store.CreateSession(ctx, sid, adminAcct.ID, "192.0.2.10", "credit-e2e-ua"); err != nil {
		t.Fatalf("create admin session: %v", err)
	}
	sessionToken, err := sessionMgr.IssueWithSessionAndBindingHashAndStepUp(sid, adminAcct.ID, "", time.Now(), false)
	if err != nil {
		t.Fatalf("issue admin session: %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"cents":  500,
		"reason": "goodwill for outage",
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.APIDURL+"/v1/admin/accounts/"+targetAcct.ID+"/credits",
		bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "faas_sid", Value: sessionToken})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "e2e-credit-"+uuid.NewString())

	rec, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer rec.Body.Close()
	if rec.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.StatusCode)
	}

	var resp api.AccountCreditResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AccountID != targetAcct.ID {
		t.Errorf("AccountID = %q, want %q", resp.AccountID, targetAcct.ID)
	}
	if resp.CentsRemaining != 500 {
		t.Errorf("CentsRemaining = %d, want 500", resp.CentsRemaining)
	}

	// Exactly one row in account_credits + one row in credit_ledger.
	credits, err := store.ListAccountCredits(ctx, targetAcct.ID, false)
	if err != nil {
		t.Fatalf("ListAccountCredits: %v", err)
	}
	if len(credits) != 1 {
		t.Fatalf("account_credits rows = %d, want 1", len(credits))
	}
	if credits[0].Reason != "goodwill for outage" {
		t.Errorf("credit reason = %q, want %q", credits[0].Reason, "goodwill for outage")
	}

	// Audit row — the auditor's Emit is best-effort, so this
	// assertion pins that the row actually landed (matches the
	// handler-test addition).
	events, err := store.ListEvents(ctx, targetAcct.ID, 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var sawCreditIssued bool
	for _, e := range events {
		if e.Kind == "credit.issued" {
			sawCreditIssued = true
			break
		}
	}
	if !sawCreditIssued {
		t.Fatalf("credit.issued audit row missing for account %s", targetAcct.ID)
	}
}

// TestE2E_CreditIssue_NonAdminForbidden — POST without admin scope
// returns 403. Pins the two-layer auth at the wire boundary (not just
// at the unit-test boundary).
func TestE2E_CreditIssue_NonAdminForbidden(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	pgtest.WaitForMigration(t, pool, 54, 10*time.Second)

	h := e2etest.StartWithEnv(t, pool, e2etest.APID,
		[]string{"FAAS_ADMIN_EMAILS=e2e+hobby+admin@test.example"})

	store := state.NewPgStore(pool)
	targetEmail := "e2e+hobby+credit-403-target@test.example"
	targetAcct, err := store.CreateAccount(ctx, targetEmail, api.PlanHobby)
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}

	// Mint a non-admin scoped key directly via the store (the harness's
	// SeedAccount always returns admin scope, so we hand-build here).
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	nonAdminAcct, err := store.CreateAccount(ctx, "e2e+hobby+credit-403-caller@test.example", api.PlanFree)
	if err != nil {
		t.Fatalf("seed caller: %v", err)
	}
	if _, err := store.CreateAPIKey(ctx, nonAdminAcct.ID, hash, "e2e", []string{api.ScopeDeployWrite}); err != nil {
		t.Fatalf("create non-admin key: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"cents":  500,
		"reason": "goodwill for outage",
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		h.APIDURL+"/v1/admin/accounts/"+targetAcct.ID+"/credits",
		bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+plain)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "e2e-credit-403-"+uuid.NewString())

	rec, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer rec.Body.Close()
	if rec.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.StatusCode)
	}
}

// spec: §14 billing credits must move provider money exactly once.
// TestE2E_CreditConsume_PolarAppliesCreditAsDiscount — with Polar, account
// credit reaches the next order as a discount (ADR-20261104212028), so
// POST /v1/invoices/{id}/consume-credits answers 409 against the real apid
// and Postgres: no credit is drawn against the paid invoice and no refund
// is sent to the provider, under a fresh Idempotency-Key as well.
func TestE2E_CreditConsume_PolarAppliesCreditAsDiscount(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	pgtest.WaitForMigration(t, pool, 58, 10*time.Second)

	const adminEmail = "e2e+hobby+admin@test.example"
	const targetEmail = "e2e+hobby+consume-target@test.example"
	polarServer, refundPosts := newCreditRefundPolar(t)

	h := e2etest.StartWithEnv(t, pool,
		e2etest.APID,
		[]string{
			"FAAS_ADMIN_EMAILS=" + adminEmail,
			"FAAS_BILLING_PROVIDER=polar",
			"FAAS_POLAR_ACCESS_TOKEN=polar_e2e_token",
			"FAAS_POLAR_WEBHOOK_SECRET=dGVzdA==",
			"FAAS_POLAR_HOBBY_PRODUCT_ID=hobby-product",
			"FAAS_POLAR_PRO_PRODUCT_ID=pro-product",
			"FAAS_POLAR_SCALE_PRODUCT_ID=scale-product",
			"FAAS_POLAR_USAGE_EVENT_NAME=ram_usage",
			"FAAS_POLAR_METER_ID=meter-1",
			"FAAS_POLAR_BASE_URL=" + polarServer.URL,
		})

	store := state.NewPgStore(pool)
	targetAcct, err := store.AccountByEmail(ctx, targetEmail)
	if err != nil {
		targetAcct, err = store.CreateAccount(ctx, targetEmail, api.PlanHobby)
		if err != nil {
			t.Fatalf("seed target: %v", err)
		}
	}
	adminToken := h.SeedAccount(ctx, api.PlanHobby, "admin")
	if _, err := store.CreateAccountCredit(ctx, state.AccountCredit{
		AccountID: targetAcct.ID, CentsRemaining: 250, Reason: "e2e goodwill",
	}); err != nil {
		t.Fatalf("seed credit: %v", err)
	}
	invoiceID := uuid.NewString()
	providerInvoiceID := "order_e2e_consume_" + uuid.NewString()
	if _, err := pool.Exec(ctx,
		`insert into invoices
		   (id, account_id, provider, provider_invoice_id, provider_charge_id, status,
		    period_start, period_end, subtotal_cents, tax_cents,
		    total_cents, amount_paid_cents, plan, currency, pdf_available,
		    created_at, updated_at)
		 values ($1, $2, 'polar', $3, $3, 'paid',
		         $4, $5, 250, 0, 250, 250, 'hobby', 'usd', false,
		         now(), now())`,
		invoiceID, targetAcct.ID, providerInvoiceID, time.Now().UTC().Add(-24*time.Hour), time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			h.APIDURL+"/v1/invoices/"+invoiceID+"/consume-credits", bytes.NewBufferString(`{}`))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", fmt.Sprintf("e2e-consume-%d-%s", attempt, uuid.NewString()))
		rec, err := h.HTTPClient().Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		status, body := rec.StatusCode, readBody(t, rec)
		_ = rec.Body.Close()
		if status != http.StatusConflict || !strings.Contains(body, "discount") {
			t.Fatalf("attempt %d: status = %d body = %s, want 409 naming the discount", attempt, status, body)
		}
	}

	credits, err := store.ListAccountCredits(ctx, targetAcct.ID, false)
	if err != nil {
		t.Fatalf("list credits: %v", err)
	}
	if len(credits) != 1 || credits[0].CentsRemaining != 250 {
		t.Fatalf("credits = %+v, want the 250 cents left for the next order", credits)
	}
	var drawn int
	if err := pool.QueryRow(ctx,
		`select count(*) from credit_ledger where provider_invoice_id = $1 and delta_cents < 0`,
		providerInvoiceID).Scan(&drawn); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if drawn != 0 {
		t.Fatalf("credit drawn against the paid invoice %d times, want 0", drawn)
	}
	if got := refundPosts(); got != 0 {
		t.Fatalf("provider refund POSTs = %d, want 0", got)
	}
}

// readBody drains the response body for diagnostic messages on test
// failures. Local helper — kept at the bottom of the file so it
// shadows no other function.
func readBody(t *testing.T, rec *http.Response) string {
	t.Helper()
	b := make([]byte, 4096)
	n, _ := rec.Body.Read(b)
	return string(b[:n])
}
