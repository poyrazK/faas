package state

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type productOverdueTestStore interface {
	productContractStore
	ProductOverdueStore
	StatementInvoiceExportStore
	StatementInvoicePaymentStore
}

func TestProductOverdueHoldsMemory(t *testing.T) {
	s := NewMemStore()
	productOverdueContract(t, s, func(account, app string, doc api.ProductBillingDocument) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.productLocked().documents[doc.ID] = productMemoryDocument{account: account, app: app, document: doc}
	})
}

// adr: 20261018212918 — a customer whose exported Product Control statement
// is unpaid the policy's days after its due date cannot start new work. The
// hold names the oldest such statement, admission refuses with the invoice's
// due date and hosted page, suspension is reported separately, and paying the
// invoice or clearing the policy lifts the hold.
func productOverdueContract(t *testing.T, s productOverdueTestStore, insertDoc func(account, app string, doc api.ProductBillingDocument)) {
	t.Helper()
	ctx := t.Context()
	account, err := s.CreateAccount(ctx, "overdue-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: account.ID, Slug: "overdue-export", Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:overdue"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := s.PutOperationDefinition(ctx, OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", DefinitionID: def.ID}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}
	if _, err := s.PutProductCatalog(ctx, account.ID, app.ID, spec); err != nil {
		t.Fatal(err)
	}
	var alice, bob PlatformTenant
	for _, tenant := range []*PlatformTenant{&alice, &bob} {
		if *tenant, _, err = s.CreatePlatformTenant(ctx, account.ID, uuid.NewString()[:8], "Customer", 100); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter"}); err != nil {
			t.Fatal(err)
		}
	}

	for _, days := range []int{-1, api.ProductOverdueBlockMaxDays + 1} {
		if _, err := s.PutProductOverduePolicy(ctx, ProductOverduePolicy{AccountID: account.ID, AppID: app.ID, BlockOverdueAfterDays: days}); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("%d days: %v", days, err)
		}
	}
	if _, err := s.PutProductOverduePolicy(ctx, ProductOverduePolicy{AccountID: uuid.NewString(), AppID: app.ID, BlockOverdueAfterDays: 14}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign account policy: %v", err)
	}
	if _, err := s.GetProductOverduePolicy(ctx, account.ID, app.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing policy: %v", err)
	}
	if _, err := s.DeleteProductOverduePolicy(ctx, account.ID, app.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing policy: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	later := now.AddDate(0, 0, 1)
	export := func(tenant, period string, due time.Time) StatementInvoiceExport {
		t.Helper()
		doc := api.ProductBillingDocument{ID: uuid.NewString(), Kind: "statement", TenantID: tenant, Period: period, Currency: "EUR", AmountMillicents: 25000, CreatedAt: now}
		insertDoc(account.ID, app.ID, doc)
		e, _, err := s.BeginStatementInvoiceExport(ctx, StatementInvoiceExport{AccountID: account.ID, SourceKind: StatementSourceProductControl,
			StatementID: doc.ID, Provider: BillingProviderStripe, Customer: "cus_overdue", Currency: "EUR", AmountMinor: 25})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetStatementInvoiceExportInvoice(ctx, account.ID, e.ID, "in_"+doc.ID[:8]); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CompleteStatementInvoiceExport(ctx, account.ID, e.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.RecordStatementInvoicePayment(ctx, e.ID, StatementInvoicePayment{Status: InvoicePaymentOpen, AmountRemainingMinor: 25, DueAt: &due,
			HostedInvoiceURL: "https://invoice.stripe.com/i/" + doc.ID[:8]}, now, &later); err != nil {
			t.Fatal(err)
		}
		return e
	}
	oldest := export(alice.ID, "2026-07", now.AddDate(0, 0, -20))
	newer := export(alice.ID, "2026-08", now.AddDate(0, 0, -16))
	recent := export(bob.ID, "2026-08", now.AddDate(0, 0, -3))

	policy, err := s.PutProductOverduePolicy(ctx, ProductOverduePolicy{AccountID: account.ID, AppID: app.ID, BlockOverdueAfterDays: 14})
	if err != nil || policy.BlockOverdueAfterDays != 14 || policy.AppID != app.ID {
		t.Fatalf("policy = %+v, %v", policy, err)
	}
	placed, lifted, err := s.SyncProductOverdueHolds(ctx, now)
	if err != nil || len(placed) != 1 || len(lifted) != 0 || placed[0].TenantID != alice.ID || placed[0].ExportID != oldest.ID ||
		placed[0].DocumentID != oldest.StatementID || !placed[0].DueAt.Equal(now.AddDate(0, 0, -20)) || placed[0].AppID != app.ID {
		t.Fatalf("sync = %+v, %+v, %v", placed, lifted, err)
	}
	since := placed[0].Since
	if again, gone, err := s.SyncProductOverdueHolds(ctx, now); err != nil || len(again) != 0 || len(gone) != 0 {
		t.Fatalf("second sync = %+v, %+v, %v", again, gone, err)
	}
	if e, err := s.GetStatementInvoiceExport(ctx, account.ID, StatementSourceProductControl, oldest.StatementID); err != nil ||
		e.NextStatusSyncAt == nil || e.NextStatusSyncAt.After(now.Add(api.ProductOverdueHeldSyncInterval)) {
		t.Fatalf("held invoice resync = %+v, %v", e.NextStatusSyncAt, err)
	}
	if e, err := s.GetStatementInvoiceExport(ctx, account.ID, StatementSourceProductControl, recent.StatementID); err != nil ||
		e.NextStatusSyncAt == nil || !e.NextStatusSyncAt.Equal(later) {
		t.Fatalf("unheld invoice resync = %+v, %v", e.NextStatusSyncAt, err)
	}
	holds, err := s.ListProductOverdueHolds(ctx, account.ID, app.ID)
	if err != nil || len(holds) != 1 || holds[0].TenantID != alice.ID || holds[0].HostedInvoiceURL != "https://invoice.stripe.com/i/"+oldest.StatementID[:8] {
		t.Fatalf("holds = %+v, %v", holds, err)
	}
	if _, err := s.GetProductOverdueHold(ctx, account.ID, app.ID, bob.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob held: %v", err)
	}

	base := OperationAdmission{AccountID: account.ID, PlatformTenantID: alice.ID, DefinitionID: def.ID, ProductFeature: "export", ProductControlEnabled: true, Input: json.RawMessage(`{}`)}
	admit := func(tenant, key string) error {
		a := base
		a.PlatformTenantID, a.IdempotencyKey = tenant, key
		_, _, err := s.AdmitOperation(ctx, a)
		return err
	}
	var refusal *ProductInvoiceOverdueError
	if err := admit(alice.ID, "held"); !errors.As(err, &refusal) || !errors.Is(err, ErrProductInvoiceOverdue) ||
		!refusal.DueAt.Equal(now.AddDate(0, 0, -20)) || refusal.HostedInvoiceURL != holds[0].HostedInvoiceURL {
		t.Fatalf("held customer admitted: %v", err)
	}
	if usage, err := s.GetProductUsage(ctx, account.ID, app.ID, alice.ID, now.Format("2006-01")); err != nil || len(usage.Lines) != 0 {
		t.Fatalf("refused start consumed: %+v, %v", usage, err)
	}
	if err := admit(bob.ID, "unheld"); err != nil {
		t.Fatalf("unheld customer refused: %v", err)
	}
	if _, err := s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: alice.ID, Plan: "starter", Suspended: true}); err != nil {
		t.Fatal(err)
	}
	if err := admit(alice.ID, "held"); !errors.Is(err, ErrProductAccess) || errors.Is(err, ErrProductInvoiceOverdue) {
		t.Fatalf("suspended and held customer: %v", err)
	}
	if _, err := s.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: alice.ID, Plan: "starter"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetProductOverdueHold(ctx, account.ID, app.ID, alice.ID); err != nil {
		t.Fatalf("lifting suspension lifted the hold: %v", err)
	}

	paid := func(e StatementInvoiceExport) {
		t.Helper()
		if _, err := s.RecordStatementInvoicePayment(ctx, e.ID, StatementInvoicePayment{Status: InvoicePaymentPaid, AmountPaidMinor: 25, PaidAt: &now}, now, nil); err != nil {
			t.Fatal(err)
		}
	}
	paid(oldest)
	if placed, lifted, err := s.SyncProductOverdueHolds(ctx, now); err != nil || len(placed) != 0 || len(lifted) != 0 {
		t.Fatalf("moving the hold to the next overdue statement = %+v, %+v, %v", placed, lifted, err)
	}
	if hold, err := s.GetProductOverdueHold(ctx, account.ID, app.ID, alice.ID); err != nil || hold.ExportID != newer.ID || !hold.Since.Equal(since) {
		t.Fatalf("moved hold = %+v, %v", hold, err)
	}
	paid(newer)
	if _, err := s.GetProductOverdueHold(ctx, account.ID, app.ID, alice.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("paid invoice still holds before the next sync: %v", err)
	}
	if err := admit(alice.ID, "paid"); err != nil {
		t.Fatalf("paid customer refused: %v", err)
	}
	if placed, lifted, err := s.SyncProductOverdueHolds(ctx, now); err != nil || len(placed) != 0 || len(lifted) != 1 || lifted[0].TenantID != alice.ID {
		t.Fatalf("lift = %+v, %+v, %v", placed, lifted, err)
	}

	updated, err := s.PutProductOverduePolicy(ctx, ProductOverduePolicy{AccountID: account.ID, AppID: app.ID, BlockOverdueAfterDays: 0})
	if err != nil || updated.BlockOverdueAfterDays != 0 || !updated.CreatedAt.Equal(policy.CreatedAt) {
		t.Fatalf("updated policy = %+v, %v", updated, err)
	}
	if placed, _, err := s.SyncProductOverdueHolds(ctx, now); err != nil || len(placed) != 1 || placed[0].TenantID != bob.ID {
		t.Fatalf("zero-day policy = %+v, %v", placed, err)
	}
	lifted, err = s.DeleteProductOverduePolicy(ctx, account.ID, app.ID)
	if err != nil || len(lifted) != 1 || lifted[0].TenantID != bob.ID {
		t.Fatalf("clearing the policy lifted %+v, %v", lifted, err)
	}
	if err := admit(bob.ID, "cleared"); err != nil {
		t.Fatalf("customer refused after the policy was cleared: %v", err)
	}
	if placed, lifted, err := s.SyncProductOverdueHolds(ctx, now); err != nil || len(placed) != 0 || len(lifted) != 0 {
		t.Fatalf("sync after clearing = %+v, %+v, %v", placed, lifted, err)
	}
}
