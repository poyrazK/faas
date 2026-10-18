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
)

type productWorkHistoryTestStore interface {
	productRunDetailsTestStore
	ProductWorkHistoryStore
}

func readWorkHistory(t *testing.T, s ProductWorkHistoryStore, scope ProductWorkHistoryScope, f api.ProductWorkHistoryFilter, cursor string) api.ProductWorkHistoryPage {
	t.Helper()
	p, err := s.ListProductWorkHistory(t.Context(), scope, f, cursor)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProductControlWorkHistoryMemory(t *testing.T) {
	s := NewMemStore()
	insert := func(op Operation) { s.mu.Lock(); defer s.mu.Unlock(); s.insertProductOperationLocked(op) }
	settle := func(op Operation) { s.mu.Lock(); defer s.mu.Unlock(); s.settleProductOperationLocked(op) }
	productWorkHistoryContract(t, s, seedProductBilling(t, s, insert, settle), insert, settle)
}

func productWorkHistoryContract(t *testing.T, s productWorkHistoryTestStore, f productBillingFixture, insert, settle func(Operation)) {
	t.Helper()
	ctx := t.Context()
	auth, err := s.CreateApp(ctx, App{AccountID: f.scope.AccountID, Slug: "history-auth-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := s.GetProductCatalog(ctx, f.scope.AccountID, f.scope.AppID, "")
	if err != nil {
		t.Fatal(err)
	}
	spec := catalog.Spec
	spec.AuthApp = auth.Slug
	spec.Access = &api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{"viewer": {api.ProductPermissionViewWork}, "billing": {api.ProductPermissionViewBilling}, "manager": {api.ProductPermissionManagePlan}}}
	if _, err = s.PutProductCatalog(ctx, f.scope.AccountID, f.scope.AppID, spec); err != nil {
		t.Fatal(err)
	}
	org := uuid.NewString()
	customer := api.ProductCustomer{TenantID: f.scope.TenantID, Plan: "pro", AuthOrganizationID: org}
	if _, err = s.SetProductCustomer(ctx, f.scope.AccountID, f.scope.AppID, customer); err != nil {
		t.Fatal(err)
	}
	scope := ProductWorkHistoryScope{ProductPlanScope{AccountID: f.scope.AccountID, AppID: f.scope.AppID, TenantID: f.scope.TenantID, AuthApp: auth.Slug, AuthAppID: auth.ID, OrganizationID: org, Role: "viewer"}}
	at := f.ops["success"].ProductControl.AdmittedAt
	oldIDs := map[string]bool{}
	for i := 0; i < 66; i++ {
		op := cloneOperation(f.ops["success"])
		op.ID = uuid.NewString()
		op.ProductControl.Feature = "retired-feature"
		op.ProductControl.Plan = "retired-plan"
		// Equal timestamps exercise UUID tie breaks over several pages. The
		// final entry is the last microsecond of the following UTC day.
		if i == 65 {
			op.ProductControl.AdmittedAt = at.Add(48*time.Hour - time.Microsecond)
			op.UpdatedAt = at.Add(48 * time.Hour)
		}
		insert(op)
		settle(op)
		oldIDs[op.ID] = true
	}
	before, err := s.ReviewProductBilling(ctx, f.scope)
	if err != nil {
		t.Fatal(err)
	}
	all := api.ProductWorkHistoryFilter{}
	first := readWorkHistory(t, s, scope, all, "")
	if len(first.Entries) != api.ProductControlWorkHistoryPageSize || first.NextCursor == "" || first.Filter.State != "all" {
		t.Fatal(first)
	}
	seen := map[string]bool{}
	page := first
	for {
		for _, e := range page.Entries {
			if seen[e.OperationID] {
				t.Fatal("duplicate", e.OperationID)
			}
			seen[e.OperationID] = true
			if e.OperationID == f.ops["bob"].ID || e.OperationID == f.ops["carol"].ID {
				t.Fatal("foreign receipt leaked")
			}
		}
		if page.NextCursor == "" {
			break
		}
		page = readWorkHistory(t, s, scope, all, page.NextCursor)
	}
	if len(seen) != 72 {
		t.Fatalf("history lost retained receipts: %d", len(seen))
	}
	for id := range oldIDs {
		if !seen[id] {
			t.Fatal("historical feature/plan lost")
		}
	}
	filtered := api.ProductWorkHistoryFilter{Feature: "retired-feature", State: "succeeded", From: at.Add(24 * time.Hour).Format(time.DateOnly), Through: at.Add(24 * time.Hour).Format(time.DateOnly)}
	last := readWorkHistory(t, s, scope, filtered, "")
	if len(last.Entries) != 1 || last.Entries[0].Plan != "retired-plan" || last.Entries[0].Outcome.ResultAvailable || last.Entries[0].Outcome.DetailsAvailable {
		t.Fatal("inclusive dates or retained outcomes lost", last)
	}
	id := f.ops["success"].ID
	byID := readWorkHistory(t, s, scope, api.ProductWorkHistoryFilter{OperationID: id}, "")
	if len(byID.Entries) != 1 || byID.Entries[0].Plan != "starter" || byID.Entries[0].Outcome.State != "succeeded" {
		t.Fatal(byID)
	}
	*byID.Entries[0].Outcome.ResolvedAt = time.Time{}
	if readWorkHistory(t, s, scope, api.ProductWorkHistoryFilter{OperationID: id}, "").Entries[0].Outcome.ResolvedAt.IsZero() {
		t.Fatal("aliased outcome")
	}
	for _, ref := range []string{uuid.NewString(), f.ops["bob"].ID} {
		if p := readWorkHistory(t, s, scope, api.ProductWorkHistoryFilter{OperationID: ref}, ""); len(p.Entries) != 0 || p.NextCursor != "" {
			t.Fatal("foreign/missing reference leaked", p)
		}
	}
	for _, state := range []string{"failed", "cancelled", "unavailable"} {
		p := readWorkHistory(t, s, scope, api.ProductWorkHistoryFilter{State: state}, "")
		if len(p.Entries) == 0 {
			t.Fatal("missing outcome filter", state)
		}
		for _, e := range p.Entries {
			if e.Outcome.State != state {
				t.Fatal(e)
			}
		}
	}
	raw, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"millicents", "charge", "currency", "admission_evidence", "definition_id", "deployment_id", "input\"", "result\"", "waiver", "invoice", "execution"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("private field leaked", forbidden)
		}
	}
	for _, bad := range []api.ProductWorkHistoryFilter{{State: "expired"}, {Feature: "../x"}, {OperationID: "bad"}, {From: "2026-02-30"}, {From: "2026-10-02", Through: "2026-10-01"}} {
		if _, err = s.ListProductWorkHistory(ctx, scope, bad, ""); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	if _, err = s.ListProductWorkHistory(ctx, scope, api.ProductWorkHistoryFilter{State: "failed"}, first.NextCursor); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("cursor changed filter accepted", err)
	}
	for _, role := range []string{"billing", "manager", "unknown"} {
		sc := scope
		sc.Role = role
		if _, err = s.ListProductWorkHistory(ctx, sc, all, ""); !errors.Is(err, ErrProductAccess) {
			t.Fatal("role read history", role, err)
		}
	}
	for _, what := range []string{"account", "app", "tenant", "auth", "organization"} {
		sc := scope
		switch what {
		case "account":
			sc.AccountID = uuid.NewString()
		case "app":
			sc.AppID = uuid.NewString()
		case "tenant":
			sc.TenantID = f.other.TenantID
		case "auth":
			sc.AuthAppID = uuid.NewString()
		case "organization":
			sc.OrganizationID = uuid.NewString()
		}
		if _, err = s.ListProductWorkHistory(ctx, sc, all, ""); err == nil {
			t.Fatal("foreign scope accepted", what)
		}
	}
	customer.Suspended = true
	if _, err = s.SetProductCustomer(ctx, scope.AccountID, scope.AppID, customer); err != nil {
		t.Fatal(err)
	}
	readWorkHistory(t, s, scope, all, "")
	// A policy change that retains the role's work access invalidates its cursor.
	spec.Access.Roles["viewer"] = []api.ProductPermission{api.ProductPermissionViewWork, api.ProductPermissionViewBilling}
	if _, err = s.PutProductCatalog(ctx, scope.AccountID, scope.AppID, spec); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListProductWorkHistory(ctx, scope, all, first.NextCursor); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("stale policy cursor accepted", err)
	}
	after, err := s.ReviewProductBilling(ctx, f.scope)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("read mutated billing", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.ListProductWorkHistory(cancelled, scope, all, ""); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel ignored", err)
	}
	spec.Access.Roles["viewer"] = []api.ProductPermission{api.ProductPermissionViewBilling}
	if _, err = s.PutProductCatalog(ctx, scope.AccountID, scope.AppID, spec); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListProductWorkHistory(ctx, scope, all, ""); !errors.Is(err, ErrProductAccess) {
		t.Fatal("stale role authority accepted", err)
	}
}

func TestProductControlWorkHistoryLiveMemory(t *testing.T) {
	s := NewMemStore()
	persist := func(op Operation, settled bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.operationMemoryLocked().operations[op.ID] = cloneOperation(op)
		if settled {
			s.settleProductOperationLocked(op)
		}
	}
	productWorkHistoryLiveContract(t, s, persist)
}
func productWorkHistoryLiveContract(t *testing.T, s productWorkHistoryTestStore, persist func(Operation, bool)) {
	t.Helper()
	f := helpFixture(t.Context(), t, s)
	f.admission.IdempotencyKey = uuid.NewString()
	op, _, err := s.AdmitOperation(t.Context(), f.admission)
	if err != nil {
		t.Fatal(err)
	}
	scope := ProductWorkHistoryScope{f.scope}
	scope.Role = "viewer"
	check := func(state string, details, result bool) {
		t.Helper()
		page := readWorkHistory(t, s, scope, api.ProductWorkHistoryFilter{OperationID: op.ID, State: state}, "")
		if len(page.Entries) != 1 || page.Entries[0].Outcome.State != state || page.Entries[0].Outcome.DetailsAvailable != details || page.Entries[0].Outcome.ResultAvailable != result {
			t.Fatal(page)
		}
	}
	op.ExpiresAt = time.Now().Add(-time.Hour)
	persist(op, false)
	check("accepted", true, false)
	op.State = api.OperationRunning
	persist(op, false)
	check("running", true, false)
	op.State = api.OperationRequiresReconciliation
	op.ExpiresAt = time.Now().Add(time.Hour)
	persist(op, false)
	check("requires_reconciliation", true, false)
	op.ExpiresAt = time.Now().Add(-time.Second)
	persist(op, false)
	check("unavailable", false, false)
	op.State = api.OperationSucceeded
	op.UpdatedAt = time.Now().UTC()
	op.ExpiresAt = time.Now().Add(time.Hour)
	op.Result = json.RawMessage(`{"PRIVATE_RESULT":true}`)
	persist(op, true)
	check("succeeded", true, true)
	op.ExpiresAt = time.Now().Add(-time.Second)
	persist(op, false)
	check("succeeded", false, false)
	page := readWorkHistory(t, s, scope, api.ProductWorkHistoryFilter{State: "failed"}, "")
	if len(page.Entries) != 0 {
		t.Fatal("expired results invented failure", page)
	}
}
