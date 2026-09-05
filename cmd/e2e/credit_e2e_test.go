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
	"net/http"
	"os"
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
	pool := pgtest.Open(t)
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
	pool := pgtest.Open(t)
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

// TestE2E_CreditConsume_HappyPathAndIdempotent — POST
// /v1/invoices/{id}/consume-credits end-to-end. Plants an account
// + invoice + credit + usage row directly in Postgres (no
// SeedInvoiceForTest on PgStore yet — that lands with PR-B's
// webhook writer; this PR's reducer is invoked via the operator
// endpoint). Pins FIFO drain + the partial-unique-index idempotency
// against real SQL, not the in-process MemStore.
//
// §14 BILLING gate for issue #279 PR-C: the reducer must drain
// credits FIFO, return the per-credit breakdown, and be idempotent
// under a fresh Idempotency-Key (the partial unique index on
// credit_ledger (provider_invoice_id, credit_id) is the seam).
func TestE2E_CreditConsume_HappyPathAndIdempotent(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	// Slot 56 lands the partial unique index on credit_ledger
	// (provider_invoice_id, credit_id). WaitForMigration gates
	// against the cmd/e2e schedd migration race — daemons must
	// boot AFTER the migration is in goose_db_version.
	pgtest.WaitForMigration(t, pool, 58, 10*time.Second)

	const adminEmail = "e2e+hobby+admin@test.example"
	const targetEmail = "e2e+hobby+consume-target@test.example"

	h := e2etest.StartWithEnv(t, pool,
		e2etest.APID,
		[]string{"FAAS_ADMIN_EMAILS=" + adminEmail})

	store := state.NewPgStore(pool)

	targetAcct, err := store.AccountByEmail(ctx, targetEmail)
	if err != nil {
		targetAcct, err = store.CreateAccount(ctx, targetEmail, api.PlanHobby)
		if err != nil {
			t.Fatalf("seed target: %v", err)
		}
	}
	adminToken := h.SeedAccount(ctx, api.PlanHobby, "admin")

	// Plant a 250-cent credit.
	credit, err := store.CreateAccountCredit(ctx, state.AccountCredit{
		AccountID:      targetAcct.ID,
		CentsRemaining: 250,
		Reason:         "e2e goodwill",
	})
	if err != nil {
		t.Fatalf("seed credit: %v", err)
	}

	// Plant an invoice. TODO(PR-B): the PR-B webhook writer adds
	// UpsertInvoice to the Store interface; this raw-SQL insert goes
	// away when webhook ingestion lands.
	invoiceID := uuid.NewString()
	periodStart := time.Now().UTC().Add(-24 * time.Hour)
	periodEnd := time.Now().UTC().Add(time.Hour)
	providerInvoiceID := "in_e2e_consume_" + uuid.NewString()
	if _, err := pool.Exec(ctx,
		`insert into invoices
		   (id, account_id, provider, provider_invoice_id, status,
		    period_start, period_end, subtotal_cents, tax_cents,
		    total_cents, amount_paid_cents, currency, pdf_available,
		    created_at, updated_at)
		 values ($1, $2, 'stripe', $3, 'paid',
		         $4, $5, 0, 0, 0, 0, 'eur', false,
		         now(), now())`,
		invoiceID, targetAcct.ID, providerInvoiceID, periodStart, periodEnd); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}

	// Seed a real app + deployment + parked instance so the usage
	// row's app_id / instance_id columns (uuid not null) accept the
	// values AppendUsage writes. The instance is Parked so the
	// meterd sampler doesn't append additional mb_seconds on top of
	// our seed (spec invariant §6.2-4: parked instance consumes zero
	// resident RAM and zero billable usage).
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("resolve default-local compute_node: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID:      targetAcct.ID,
		Slug:           "consume-e2e",
		Type:           state.AppTypeApp,
		RAMMB:          256,
		MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Status:      state.DeployLive,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
	})
	if err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateParked), 256, node.ID, "")
	if err != nil {
		t.Fatalf("seed instance: %v", err)
	}

	// Plant 50 included GB-hours plus 250 billable GB-hours.
	if err := store.AppendUsage(ctx, targetAcct.ID, app.ID, ins.ID,
		time.Now().UTC(), int64(targetAcct.Plan.PlanIncludedGBHours()+250)*api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("seed usage: %v", err)
	}

	// First call — drain 250 cents.
	body := bytes.NewBufferString(`{}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.APIDURL+"/v1/invoices/"+invoiceID+"/consume-credits", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "e2e-consume-1-"+uuid.NewString())

	rec, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer rec.Body.Close()
	if rec.StatusCode != http.StatusOK {
		t.Fatalf("first call: status = %d, want 200; body=%s", rec.StatusCode, readBody(t, rec))
	}

	var resp api.ConsumeInvoiceResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.InvoiceID != invoiceID {
		t.Errorf("InvoiceID = %q, want %q", resp.InvoiceID, invoiceID)
	}
	if resp.ConsumedCents != 250 {
		t.Errorf("ConsumedCents = %d, want 250", resp.ConsumedCents)
	}
	if resp.AlreadyConsumedForInvoice {
		t.Errorf("first call: AlreadyConsumedForInvoice = true, want false")
	}
	if len(resp.PerCredit) != 1 || resp.PerCredit[0].CreditID != credit.ID {
		t.Fatalf("PerCredit = %+v, want 1 row for credit %s", resp.PerCredit, credit.ID)
	}
	if resp.PerCredit[0].DeltaCents != -250 || resp.PerCredit[0].NewBalance != 0 {
		t.Errorf("PerCredit[0] = %+v, want {delta=-250, balance=0}", resp.PerCredit[0])
	}

	// Verify the persisted account_credits reflect the drain.
	credits, err := store.ListAccountCredits(ctx, targetAcct.ID, false)
	if err != nil {
		t.Fatalf("list credits: %v", err)
	}
	if len(credits) != 1 || credits[0].CentsRemaining != 0 {
		t.Fatalf("credits after drain = %+v, want one row with cents_remaining=0", credits)
	}

	// Verify the credit.consumed audit row landed.
	events, err := store.ListEvents(ctx, targetAcct.ID, 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var sawConsumed bool
	for _, e := range events {
		if e.Kind == "credit.consumed" {
			sawConsumed = true
			break
		}
	}
	if !sawConsumed {
		t.Fatalf("credit.consumed audit row missing for account %s", targetAcct.ID)
	}

	// Second call with a FRESH Idempotency-Key — the idempotent
	// middleware must not short-circuit. The reducer-level dedupe
	// (partial unique index on credit_ledger) must kick in: same
	// ConsumedCents, AlreadyConsumedForInvoice=true, no new ledger
	// rows.
	body2 := bytes.NewBufferString(`{}`)
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.APIDURL+"/v1/invoices/"+invoiceID+"/consume-credits", body2)
	if err != nil {
		t.Fatalf("new request 2: %v", err)
	}
	req2.Header.Set("Authorization", "Bearer "+adminToken)
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", "e2e-consume-2-"+uuid.NewString())

	rec2, err := h.HTTPClient().Do(req2)
	if err != nil {
		t.Fatalf("do 2: %v", err)
	}
	defer rec2.Body.Close()
	if rec2.StatusCode != http.StatusOK {
		t.Fatalf("second call: status = %d, want 200; body=%s", rec2.StatusCode, readBody(t, rec2))
	}
	var resp2 api.ConsumeInvoiceResponse
	if err := json.NewDecoder(rec2.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode 2: %v", err)
	}
	if !resp2.AlreadyConsumedForInvoice {
		t.Errorf("replay: AlreadyConsumedForInvoice = false, want true (partial unique index)")
	}
	if resp2.ConsumedCents != 250 {
		t.Errorf("replay: ConsumedCents = %d, want 250 (stable across replays)", resp2.ConsumedCents)
	}
	if len(resp2.PerCredit) != 0 {
		t.Errorf("replay: PerCredit = %+v, want 0", resp2.PerCredit)
	}

	// Exactly one consumption ledger row in Postgres for this
	// invoice (the partial unique index made the replay's INSERT a
	// no-op).
	var consumptionRowCount int
	if err := pool.QueryRow(ctx,
		`select count(*) from credit_ledger
		   where provider_invoice_id = $1 and delta_cents < 0`,
		providerInvoiceID).Scan(&consumptionRowCount); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if consumptionRowCount != 1 {
		t.Fatalf("consumption ledger rows = %d, want 1 (partial unique index)", consumptionRowCount)
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
