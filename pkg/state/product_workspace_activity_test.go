package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type productWorkspaceActivityTestStore interface {
	productActivityTestStore
	productHelpTestStore
	ProductWorkspaceActivityStore
}

func workspaceActivityRead(t *testing.T, s ProductWorkspaceActivityStore, scope ProductWorkspaceActivityScope, filter, cursor string) api.ProductWorkspaceActivityPage {
	t.Helper()
	p, err := s.ListProductWorkspaceActivity(t.Context(), scope, filter, cursor)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProductControlWorkspaceActivityMemory(t *testing.T) {
	s := NewMemStore()
	insert := func(op Operation) { s.mu.Lock(); defer s.mu.Unlock(); s.insertProductOperationLocked(op) }
	settle := func(op Operation) { s.mu.Lock(); defer s.mu.Unlock(); s.settleProductOperationLocked(op) }
	productWorkspaceActivityContract(t, s, seedProductBilling(t, s, insert, settle), insert)
}

func productWorkspaceActivityContract(t *testing.T, s productWorkspaceActivityTestStore, f productBillingFixture, insert func(Operation)) {
	t.Helper()
	ctx := t.Context()
	auth, err := s.CreateApp(ctx, App{AccountID: f.scope.AccountID, Slug: "timeline-auth-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := s.GetProductCatalog(ctx, f.scope.AccountID, f.scope.AppID, "")
	if err != nil {
		t.Fatal(err)
	}
	spec := catalog.Spec
	spec.AuthApp = auth.Slug
	spec.Access = &api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{
		"all":    {api.ProductPermissionRunWork, api.ProductPermissionViewBilling, api.ProductPermissionManagePlan},
		"viewer": {api.ProductPermissionViewWork}, "billing": {api.ProductPermissionViewBilling},
		"manager": {api.ProductPermissionManagePlan}, "team": {api.ProductPermissionManageTeam},
	}}
	spec.SelfService = &api.ProductPlanPolicy{Transitions: map[string][]string{"pro": {"starter"}}}
	if _, err = s.PutProductCatalog(ctx, f.scope.AccountID, f.scope.AppID, spec); err != nil {
		t.Fatal(err)
	}
	org := uuid.NewString()
	if _, err = s.SetProductCustomer(ctx, f.scope.AccountID, f.scope.AppID, api.ProductCustomer{TenantID: f.scope.TenantID, Plan: "pro", AuthOrganizationID: org}); err != nil {
		t.Fatal(err)
	}
	scope := ProductWorkspaceActivityScope{ProductPlanScope{AccountID: f.scope.AccountID, AppID: f.scope.AppID, TenantID: f.scope.TenantID, AuthApp: auth.Slug, AuthAppID: auth.ID, OrganizationID: org, Role: "all"}}
	change, err := s.ApplyProductPlanChange(ctx, scope.ProductPlanScope, productPlanReview(ctx, t, s, scope.ProductPlanScope, "starter"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"unknown", "expired"} {
		if _, err = s.WaiveProductReceipt(ctx, f.scope, f.ops[name].ID, "PRIVATE_WAIVER_REASON"); err != nil {
			t.Fatal(err)
		}
	}
	review, err := s.ReviewProductBilling(ctx, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := s.FinalizeProductBilling(ctx, f.scope, review.ReviewHash)
	if err != nil {
		t.Fatal(err)
	}
	credit, err := s.CreditProductReceipt(ctx, f.scope, api.ProductCreditRequest{ID: uuid.NewString(), TenantID: scope.TenantID, OperationID: f.ops["success"].ID, AmountMillicents: 1000, Reason: "PRIVATE_CREDIT_REASON"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.HandoffProductBilling(ctx, f.scope, statement.ID, "PRIVATE_INVOICE_ID"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"workspace", "run", "feature"} {
		feature, op := "", ""
		if kind == "run" {
			op = f.ops["success"].ID
		}
		if kind == "feature" {
			feature = "export"
		}
		r := helpCreate(ctx, t, s, ProductHelpScope(scope), kind, feature, op)
		if _, err = s.UpdateProductHelp(ctx, scope.AccountID, scope.AppID, r.Request.ID, api.ProductHelpUpdate{ExpectedVersion: 1, Status: "resolved", Response: "PRIVATE_HELP_RESPONSE"}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.ReviewProductBilling(ctx, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	full := workspaceActivityRead(t, s, scope, "all", "")
	if len(full.Events) != 10 || full.NextCursor != "" {
		t.Fatalf("retained cross-layer events: %+v", full)
	}
	for _, e := range full.Events {
		switch e.Kind {
		case "plan_changed":
			if e.ReferenceID != change.ID || e.Plan.Before != "pro" || e.Plan.After != "starter" {
				t.Fatal(e)
			}
		case "statement_finalized":
			if e.ReferenceID != statement.ID || e.Billing.AmountMillicents != statement.AmountMillicents {
				t.Fatal(e)
			}
		case "credit_recorded":
			if e.ReferenceID != credit.ID || e.Billing.AmountMillicents != credit.AmountMillicents || e.Billing.AmountMillicents >= 0 {
				t.Fatal(e)
			}
		}
	}
	for _, tc := range []struct {
		role                              string
		total, work, plans, billing, help int
	}{
		{"all", 10, 4, 1, 2, 3}, {"viewer", 6, 4, 0, 0, 2}, {"billing", 4, 0, 0, 2, 2}, {"manager", 2, 0, 1, 0, 1}, {"team", 1, 0, 0, 0, 1},
	} {
		t.Run(tc.role+" current visibility", func(t *testing.T) {
			visible := scope
			visible.Role = tc.role
			for filter, want := range map[string]int{"all": tc.total, "work": tc.work, "plans": tc.plans, "billing": tc.billing, "help": tc.help} {
				p := workspaceActivityRead(t, s, visible, filter, "")
				if len(p.Events) != want {
					t.Fatalf("%s: got %d want %d", filter, len(p.Events), want)
				}
				permissions := productcontrol.EffectiveAccess(spec.Access, tc.role)
				key := productcontrol.WorkspaceActivityScope(visible.AccountID, visible.AppID, visible.TenantID, visible.AuthAppID, visible.OrganizationID, visible.Role, filter, permissions)
				if err := productcontrol.ValidateWorkspaceActivityPage(p, permissions, key, ""); err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(p)
				for _, forbidden := range []string{"PRIVATE_", "actor_role", "auth_organization_id", "unit_price_millicents", "review_url", "snapshot", "fingerprint", "preview", "reason", "expected_hash"} {
					if strings.Contains(string(raw), forbidden) {
						t.Fatal("private source data leaked", forbidden)
					}
				}
				if !permissions.ViewBilling && strings.Contains(string(raw), "amount_millicents") {
					t.Fatal("billing amount leaked")
				}
			}
		})
	}
	// Returned pointers must not alias retained receipts/documents.
	for _, e := range full.Events {
		if e.Plan != nil {
			e.Plan.After = "tampered"
		}
		if e.Billing != nil {
			e.Billing.AmountMillicents = 999999
		}
	}
	full = workspaceActivityRead(t, s, scope, "all", "")
	for _, e := range full.Events {
		if e.Plan != nil && e.Plan.After == "tampered" || e.Billing != nil && e.Billing.AmountMillicents == 999999 {
			t.Fatal("projection aliases records")
		}
	}
	after, err := s.ReviewProductBilling(ctx, f.scope)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("reading activity changed billing", err)
	}
	// Simultaneous and sub-millisecond outcomes exercise the entire ordering key.
	for i := range api.ProductControlWorkspaceActivityPageSize + 4 {
		op := f.ops["success"]
		op.ID = uuid.NewString()
		op.UpdatedAt = op.UpdatedAt.Add(time.Duration(i%3) * time.Microsecond)
		insert(op)
		f.settle(op)
	}
	first := workspaceActivityRead(t, s, scope, "all", "")
	if len(first.Events) != api.ProductControlWorkspaceActivityPageSize || first.NextCursor == "" {
		t.Fatal("missing bounded page")
	}
	seen, cursor := map[string]bool{}, ""
	for pages := 0; ; pages++ {
		if pages > 3 {
			t.Fatal("pagination did not terminate")
		}
		page := workspaceActivityRead(t, s, scope, "all", cursor)
		key := productcontrol.WorkspaceActivityScope(scope.AccountID, scope.AppID, scope.TenantID, scope.AuthAppID, scope.OrganizationID, scope.Role, "all", productcontrol.EffectiveAccess(spec.Access, scope.Role))
		if err := productcontrol.ValidateWorkspaceActivityPage(page, productcontrol.EffectiveAccess(spec.Access, scope.Role), key, cursor); err != nil {
			t.Fatal(err)
		}
		for _, e := range page.Events {
			if seen[e.ID] {
				t.Fatal("repeated event")
			}
			seen[e.ID] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 10+api.ProductControlWorkspaceActivityPageSize+4 {
		t.Fatal("pagination lost events", len(seen))
	}
	for _, filter := range []string{"billing", "work", "unsupported"} {
		if _, err = s.ListProductWorkspaceActivity(ctx, scope, filter, first.NextCursor); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal("cursor crossed filter", err)
		}
	}
	for _, field := range []string{"tenant", "account", "app", "auth app", "organization", "role"} {
		other := scope
		switch field {
		case "tenant":
			other.TenantID = f.other.TenantID
		case "account":
			other.AccountID = uuid.NewString()
		case "app":
			other.AppID = uuid.NewString()
		case "auth app":
			other.AuthAppID = uuid.NewString()
		case "organization":
			other.OrganizationID = uuid.NewString()
		case "role":
			other.Role = "viewer"
		}
		if _, err = s.ListProductWorkspaceActivity(ctx, other, "all", first.NextCursor); err == nil {
			t.Fatal("cursor crossed", field)
		}
	}
	// More than a page of newer, hidden records must not starve a viewer's page.
	for range api.ProductControlWorkspaceActivityPageSize + 1 {
		r := helpCreate(ctx, t, s, ProductHelpScope(scope), "feature", "export", "")
		if _, err = s.UpdateProductHelp(ctx, scope.AccountID, scope.AppID, r.Request.ID, api.ProductHelpUpdate{ExpectedVersion: 1, Status: "resolved", Response: "Hidden feature reply"}); err != nil {
			t.Fatal(err)
		}
	}
	viewer := scope
	viewer.Role = "viewer"
	visible := workspaceActivityRead(t, s, viewer, "all", "")
	if len(visible.Events) != api.ProductControlWorkspaceActivityPageSize || visible.NextCursor == "" {
		t.Fatal("hidden records starved a permitted page")
	}
	for _, e := range visible.Events {
		if e.HelpKind == "feature" || e.Billing != nil || e.Plan != nil {
			t.Fatal("hidden record filled viewer page")
		}
	}
	spec.Access.Roles["all"] = []api.ProductPermission{api.ProductPermissionManageTeam}
	if _, err = s.PutProductCatalog(ctx, scope.AccountID, scope.AppID, spec); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListProductWorkspaceActivity(ctx, scope, "all", first.NextCursor); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("cursor survived changed policy", err)
	}
	if p := workspaceActivityRead(t, s, scope, "all", ""); len(p.Events) != 1 || p.Events[0].HelpKind != "workspace" {
		t.Fatal("changed policy did not filter current page")
	}
	// An empty role policy revokes access.
	spec.Access.Roles["all"] = []api.ProductPermission{}
	if _, err = s.PutProductCatalog(ctx, scope.AccountID, scope.AppID, spec); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListProductWorkspaceActivity(ctx, scope, "all", ""); !errors.Is(err, ErrProductAccess) {
		t.Fatal("revoked policy exposed history", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.ListProductWorkspaceActivity(ctx, scope, "all", ""); err == nil {
		t.Fatal("cancelled query succeeded")
	}
}

func TestProductControlWorkspaceActivityRemappingMemory(t *testing.T) {
	productWorkspaceActivityRemapping(t, NewMemStore())
}

func productWorkspaceActivityRemapping(t *testing.T, s productWorkspaceActivityTestStore) {
	t.Helper()
	f := helpFixture(t.Context(), t, s)
	scope := ProductWorkspaceActivityScope{f.scope}
	change, err := s.ApplyProductPlanChange(t.Context(), f.scope, productPlanReview(t.Context(), t, s, f.scope, "pro"))
	if err != nil {
		t.Fatal(err)
	}
	r := helpCreate(t.Context(), t, s, ProductHelpScope(scope), "workspace", "", "")
	if _, err = s.UpdateProductHelp(t.Context(), scope.AccountID, scope.AppID, r.Request.ID, api.ProductHelpUpdate{ExpectedVersion: 1, Status: "resolved", Response: "Previous organization's reply"}); err != nil {
		t.Fatal(err)
	}
	if p := workspaceActivityRead(t, s, scope, "all", ""); len(p.Events) != 2 {
		t.Fatal("seed history missing")
	}
	c := change.Preview.AfterCustomer
	c.AuthOrganizationID = uuid.NewString()
	if _, err = s.SetProductCustomer(t.Context(), scope.AccountID, scope.AppID, c); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListProductWorkspaceActivity(t.Context(), scope, "all", ""); !errors.Is(err, ErrProductAccess) {
		t.Fatal("previous organization authorized", err)
	}
	scope.OrganizationID = c.AuthOrganizationID
	if p := workspaceActivityRead(t, s, scope, "all", ""); len(p.Events) != 0 {
		t.Fatal("new organization inherited old help or plan history", p)
	}
	if _, err = s.SetPlatformTenantStatus(t.Context(), scope.AccountID, scope.TenantID, PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListProductWorkspaceActivity(t.Context(), scope, "all", ""); !errors.Is(err, ErrProductAccess) {
		t.Fatal("suspended workspace authorized", err)
	}
}

func TestProductControlWorkspaceActivityAttentionMemory(t *testing.T) {
	s := NewMemStore()
	persist := func(op Operation, settled bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.operationMemoryLocked().operations[op.ID] = op
		if settled {
			s.settleProductOperationLocked(op)
		}
	}
	productWorkspaceActivityAttention(t, s, persist)
}

func productWorkspaceActivityAttention(t *testing.T, s productWorkspaceActivityTestStore, persist func(Operation, bool)) {
	t.Helper()
	f := helpFixture(t.Context(), t, s)
	f.admission.IdempotencyKey = uuid.NewString()
	op, _, err := s.AdmitOperation(t.Context(), f.admission)
	if err != nil {
		t.Fatal(err)
	}
	scope := ProductWorkspaceActivityScope{f.scope}
	scope.Role = "viewer"
	op.State, op.UpdatedAt = api.OperationRequiresReconciliation, time.Now().UTC().Truncate(time.Microsecond)
	persist(op, false)
	p := workspaceActivityRead(t, s, scope, "work", "")
	if len(p.Events) != 1 || p.Events[0].Kind != "work_attention" {
		t.Fatal("live attention missing", p)
	}
	op.ExpiresAt = time.Now().Add(-time.Second)
	persist(op, false)
	if p = workspaceActivityRead(t, s, scope, "work", ""); len(p.Events) != 0 {
		t.Fatal("expired attention inferred an outcome", p)
	}
	op.State = api.OperationSucceeded
	persist(op, true)
	if p = workspaceActivityRead(t, s, scope, "work", ""); len(p.Events) != 1 || p.Events[0].Kind != "work_succeeded" {
		t.Fatal("retained outcome disappeared with run details", p)
	}
}
