// invoice_e2e_test.go — M7 acceptance for issue #259 PR A (BILLING:
// plan comparison + invoice history). The webhook ingestion surface
// lands in PR B alongside billing.Provider.NormalizeInvoice; until
// then, the e2e test plants invoice rows directly against the live
// Postgres so the read-path contract is locked:
//
//   - GET /v1/invoices is account-scoped: alice's rows must not
//     appear in bob's response.
//   - month=YYYY-MM is a UTC half-open filter.
//   - Validation: bad month → 400 with RFC 7807 CodeValidation.
//
// Build tag: (none). CI-safe. Requires Postgres (skip via
// FAAS_SKIP_PG_TESTS).
package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestE2E_Invoices_CrossAccountIsolation(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, nil)

	keyAlice := h.SeedAccount(context.Background(), api.PlanHobby, "alice")
	keyBob := h.SeedAccount(context.Background(), api.PlanHobby, "bob")

	store := state.NewPgStore(pool)
	aliceAcct, err := store.AccountByEmail(context.Background(),
		"e2e+hobby+alice@test.example")
	if err != nil {
		t.Fatalf("lookup alice: %v", err)
	}
	bobAcct, err := store.AccountByEmail(context.Background(),
		"e2e+hobby+bob@test.example")
	if err != nil {
		t.Fatalf("lookup bob: %v", err)
	}

	july := time.Date(2026, 7, 31, 23, 59, 59, 0, time.UTC)
	aug := time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)
	seedInvoiceE2E(t, pool, aliceAcct.ID, "stripe", "in_alice_jul", july, 900)
	seedInvoiceE2E(t, pool, aliceAcct.ID, "stripe", "in_alice_aug", aug, 900)
	seedInvoiceE2E(t, pool, bobAcct.ID, "stripe", "in_bob_jul", july, 100)

	// Alice: should see her two rows, ordered aug → jul.
	raw, code := doReq(t, h, keyAlice, http.MethodGet, "/v1/invoices", nil)
	if code != http.StatusOK {
		t.Fatalf("alice list: %d (body=%s)", code, raw)
	}
	var aliceResp api.InvoiceListResponse
	if err := json.Unmarshal(raw, &aliceResp); err != nil {
		t.Fatalf("decode alice: %v", err)
	}
	if len(aliceResp.Items) != 2 {
		t.Fatalf("alice: want 2 invoices, got %d (body=%s)", len(aliceResp.Items), raw)
	}
	if aliceResp.Items[0].ProviderInvoiceID != "in_alice_aug" {
		t.Fatalf("alice ordering broken: first=%q want in_alice_aug",
			aliceResp.Items[0].ProviderInvoiceID)
	}
	for _, it := range aliceResp.Items {
		if it.ProviderInvoiceID == "in_bob_jul" {
			t.Fatalf("alice leaked bob's row: %+v", it)
		}
	}

	// Bob: should see only his row.
	raw, code = doReq(t, h, keyBob, http.MethodGet, "/v1/invoices", nil)
	if code != http.StatusOK {
		t.Fatalf("bob list: %d (body=%s)", code, raw)
	}
	var bobResp api.InvoiceListResponse
	if err := json.Unmarshal(raw, &bobResp); err != nil {
		t.Fatalf("decode bob: %v", err)
	}
	if len(bobResp.Items) != 1 {
		t.Fatalf("bob: want 1 invoice, got %d (body=%s)", len(bobResp.Items), raw)
	}
	if bobResp.Items[0].ProviderInvoiceID != "in_bob_jul" {
		t.Fatalf("bob got wrong row: %q", bobResp.Items[0].ProviderInvoiceID)
	}

	// month=2026-07 on alice narrows to her July row only.
	raw, code = doReq(t, h, keyAlice, http.MethodGet, "/v1/invoices?month=2026-07", nil)
	if code != http.StatusOK {
		t.Fatalf("alice month: %d (body=%s)", code, raw)
	}
	var julyResp api.InvoiceListResponse
	if err := json.Unmarshal(raw, &julyResp); err != nil {
		t.Fatalf("decode july: %v", err)
	}
	if len(julyResp.Items) != 1 || julyResp.Items[0].ProviderInvoiceID != "in_alice_jul" {
		t.Fatalf("alice month=2026-07: got %+v want [in_alice_jul]", julyResp.Items)
	}

	// Validation: bad month → 400 with CodeValidation. Locks the
	// "RFC 7807 stable code" invariant from the dashboard surface.
	raw, code = doReq(t, h, keyAlice, http.MethodGet, "/v1/invoices?month=garbage", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("alice bad month: %d, want 400 (body=%s)", code, raw)
	}
	var prob api.Problem
	if err := json.Unmarshal(raw, &prob); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if prob.Code != api.CodeValidation {
		t.Fatalf("alice bad month code: %q, want %q", prob.Code, api.CodeValidation)
	}
}

// seedInvoiceE2E plants a row directly via the live pool. PR B will
// replace this with the webhook ingestion path; the unique constraint
// on (account_id, provider, provider_invoice_id) is what makes the
// first-write-wins idempotency safe — same fixture pattern as the
// pgstore unit test in pkg/state/pgstore_invoices_test.go.
func seedInvoiceE2E(t *testing.T, pool *pgxpool.Pool, accountID, provider,
	providerInvoiceID string, periodEnd time.Time, totalCents int64) {
	t.Helper()
	id := uuid.NewString()
	periodStart := periodEnd.AddDate(0, 0, -30)
	_, err := pool.Exec(context.Background(),
		`insert into invoices (id, account_id, provider, provider_invoice_id,
		                       number, status, period_start, period_end,
		                       subtotal_cents, tax_cents, total_cents,
		                       amount_paid_cents, currency, pdf_available,
		                       hosted_url, raw)
		 values ($1, $2, $3, $4, '', 'paid', $5, $6,
		         $7, 0, $7, $7, 'eur', true, '', '{}'::jsonb)`,
		id, accountID, provider, providerInvoiceID,
		periodStart, periodEnd, totalCents)
	if err != nil {
		t.Fatalf("seed invoice %s: %v", providerInvoiceID, err)
	}
}
