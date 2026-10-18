// ADR-1715: shared customer help retains scoped observations and never repairs work.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type productHelpTestStore interface {
	productPlanTestStore
	ProductHelpStore
}

func helpFixture(ctx context.Context, t *testing.T, s productHelpTestStore) productPlanFixture {
	t.Helper()
	f := newProductPlanFixture(ctx, t, s)
	f.spec.Access.Roles["viewer"] = []api.ProductPermission{api.ProductPermissionViewWork}
	f.spec.Access.Roles["team"] = []api.ProductPermission{api.ProductPermissionManageTeam}
	if _, err := s.PutProductCatalog(ctx, f.scope.AccountID, f.scope.AppID, f.spec); err != nil {
		t.Fatal(err)
	}
	return f
}

func helpTestScope(f productPlanFixture, role string) ProductHelpScope {
	s := ProductHelpScope{ProductPlanScope: f.scope}
	s.Role = role
	return s
}

func helpCreate(ctx context.Context, t *testing.T, s ProductHelpStore, scope ProductHelpScope, kind, feature, op string) api.ProductHelpRequest {
	t.Helper()
	r, err := s.CreateProductHelp(ctx, scope, api.ProductHelpCreate{ID: uuid.NewString(), Kind: kind, Feature: feature, OperationID: op, Message: "Please investigate <script>this</script> request."})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestProductControlHelpMemory(t *testing.T) { productHelpContract(t, NewMemStore()) }

func productHelpContract(t *testing.T, s productHelpTestStore) {
	t.Helper()
	ctx := t.Context()
	t.Run("retained context, immutable retries and detached reads", func(t *testing.T) {
		f := helpFixture(ctx, t, s)
		scope := helpTestScope(f, "operator")
		period := productcontrol.Period(time.Now())
		before, err := s.GetProductUsage(ctx, scope.AccountID, scope.AppID, scope.TenantID, period)
		if err != nil {
			t.Fatal(err)
		}
		r := helpCreate(ctx, t, s, scope, "feature", "export", "")
		if r.Context.Usage == nil || r.Context.Usage.Remaining != 1 || r.Context.Reason != "available" || r.Status != "open" || r.Version != 1 || r.Response != "" || r.Context.ResetsAt == nil {
			t.Fatalf("new help: %+v", r)
		}
		r.Context.Usage.Remaining = 999
		fresh, err := s.GetProductHelp(ctx, scope, r.Request.ID)
		if err != nil || fresh.Context.Usage.Remaining != 1 {
			t.Fatal("aliased retained context", err)
		}
		u := api.ProductHelpUpdate{ExpectedVersion: 1, Status: "in_progress", Response: "Reviewing your request."}
		updated, err := s.UpdateProductHelp(ctx, scope.AccountID, scope.AppID, r.Request.ID, u)
		if err != nil || updated.Version != 2 {
			t.Fatal("update", err)
		}
		retry, err := s.CreateProductHelp(ctx, scope, r.Request)
		if err != nil || !reflect.DeepEqual(retry, updated) {
			t.Fatal("creation retry changed stored request", err)
		}
		changed := r.Request
		changed.Message += " changed"
		if _, err = s.CreateProductHelp(ctx, scope, changed); !errors.Is(err, ErrConflict) {
			t.Fatalf("changed request reused ID: %v", err)
		}
		page, err := s.ListProductHelp(ctx, scope, "all", "")
		if err != nil || len(page.Requests) != 1 {
			t.Fatal("retry duplicated request", err)
		}
		after, err := s.GetProductUsage(ctx, scope.AccountID, scope.AppID, scope.TenantID, period)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("help consumed quota/changed billing", err)
		}
		raw, _ := json.Marshal(fresh)
		for _, forbidden := range []string{"unit_price_millicents", "amount_millicents", "auth_organization_id", "auth_app_id", "input_schema", "definition_id", "actor_role"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("help leaked %s", forbidden)
			}
		}
	})
	t.Run("live role visibility, retained run scope and exhausted allowance", func(t *testing.T) {
		f := helpFixture(ctx, t, s)
		scope := helpTestScope(f, "operator")
		a := f.admission
		a.IdempotencyKey = uuid.NewString()
		op, _, err := s.AdmitOperation(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		general := helpCreate(ctx, t, s, scope, "workspace", "", "")
		feature := helpCreate(ctx, t, s, scope, "feature", "export", "")
		run := helpCreate(ctx, t, s, scope, "run", "", op.ID)
		if feature.Context.Reason != "allowance_exhausted" || feature.Context.Usage.Starts != 1 || run.Context.Feature != "export" || run.Context.Usage != nil || run.Context.Reason != "run_reported" {
			t.Fatal("wrong source context")
		}
		for _, tc := range []struct {
			role string
			ids  []string
		}{{"operator", []string{general.Request.ID, feature.Request.ID, run.Request.ID}}, {"viewer", []string{general.Request.ID, run.Request.ID}}, {"billing", []string{general.Request.ID, feature.Request.ID}}, {"manager", []string{general.Request.ID}}, {"team", []string{general.Request.ID}}} {
			visible := helpTestScope(f, tc.role)
			page, err := s.ListProductHelp(ctx, visible, "all", "")
			if err != nil || len(page.Requests) != len(tc.ids) {
				t.Fatalf("%s visibility: %+v %v", tc.role, page, err)
			}
			if tc.role == "manager" {
				if _, err = s.GetProductHelp(ctx, visible, feature.Request.ID); !errors.Is(err, ErrNotFound) {
					t.Fatalf("private feature context disclosed: %v", err)
				}
				if _, err = s.CreateProductHelp(ctx, visible, api.ProductHelpCreate{ID: uuid.NewString(), Kind: "feature", Feature: "export", Message: "Denied"}); !errors.Is(err, ErrProductAccess) {
					t.Fatalf("feature source without permission: %v", err)
				}
			}
		}
		foreign, _, err := s.CreatePlatformTenant(ctx, scope.AccountID, uuid.NewString(), "Foreign", 100)
		if err != nil {
			t.Fatal(err)
		}
		org := uuid.NewString()
		if _, err = s.SetProductCustomer(ctx, scope.AccountID, scope.AppID, api.ProductCustomer{TenantID: foreign.ID, Plan: "starter", AuthOrganizationID: org}); err != nil {
			t.Fatal(err)
		}
		other := scope
		other.TenantID, other.OrganizationID = foreign.ID, org
		if _, err = s.GetProductHelp(ctx, other, general.Request.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-workspace help: %v", err)
		}
		if _, err = s.CreateProductHelp(ctx, other, api.ProductHelpCreate{ID: uuid.NewString(), Kind: "run", OperationID: op.ID, Message: "Foreign run"}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-workspace run reference: %v", err)
		}
		f.spec.Access.Roles["operator"] = []api.ProductPermission{}
		if _, err = s.PutProductCatalog(ctx, scope.AccountID, scope.AppID, f.spec); err != nil {
			t.Fatal(err)
		}
		if _, err = s.GetProductHelp(ctx, scope, general.Request.ID); !errors.Is(err, ErrProductAccess) {
			t.Fatalf("revoked role recovered help: %v", err)
		}
	})
	t.Run("organization remapping does not expose a previous workspace history", func(t *testing.T) {
		f := helpFixture(ctx, t, s)
		scope := helpTestScope(f, "operator")
		r := helpCreate(ctx, t, s, scope, "workspace", "", "")
		c := f.customer
		c.AuthOrganizationID = uuid.NewString()
		if _, err := s.SetProductCustomer(ctx, scope.AccountID, scope.AppID, c); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetProductHelp(ctx, scope, r.Request.ID); !errors.Is(err, ErrProductAccess) {
			t.Fatalf("old organization remained authorized: %v", err)
		}
		scope.OrganizationID = c.AuthOrganizationID
		page, err := s.ListProductHelp(ctx, scope, "all", "")
		if err != nil || len(page.Requests) != 0 {
			t.Fatal("new organization inherited private history", err)
		}
		if _, err = s.GetProductOwnerHelp(ctx, scope.AccountID, scope.AppID, r.Request.ID); err != nil {
			t.Fatal("owner lost retained request", err)
		}
	})
	t.Run("competing status changes, acknowledgement loss and stale retries", func(t *testing.T) {
		f := helpFixture(ctx, t, s)
		scope := helpTestScope(f, "operator")
		r := helpCreate(ctx, t, s, scope, "workspace", "", "")
		changes := []api.ProductHelpUpdate{{ExpectedVersion: 1, Status: "in_progress", Response: "Investigating"}, {ExpectedVersion: 1, Status: "resolved", Response: "Answered"}}
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range changes {
			wg.Go(func() { _, errs[i] = s.UpdateProductHelp(ctx, scope.AccountID, scope.AppID, r.Request.ID, changes[i]) })
		}
		wg.Wait()
		winner := -1
		for i, err := range errs {
			if err == nil {
				if winner != -1 {
					t.Fatal("both concurrent updates applied")
				}
				winner = i
			} else if !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
		}
		if winner == -1 {
			t.Fatal("no status update applied")
		}
		if retry, err := s.UpdateProductHelp(ctx, scope.AccountID, scope.AppID, r.Request.ID, changes[winner]); err != nil || retry.Version != 2 {
			t.Fatal("response-loss retry reapplied", err)
		}
		if _, err := s.UpdateProductHelp(ctx, scope.AccountID, scope.AppID, r.Request.ID, api.ProductHelpUpdate{ExpectedVersion: 2, Status: "open", Response: "Reopened with a new question"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.UpdateProductHelp(ctx, scope.AccountID, scope.AppID, r.Request.ID, changes[winner]); !errors.Is(err, ErrConflict) {
			t.Fatalf("old retry restored a previous response: %v", err)
		}
	})
	t.Run("bounded pages and scope-bound continuation", func(t *testing.T) {
		f := helpFixture(ctx, t, s)
		scope := helpTestScope(f, "operator")
		for i := 0; i < api.ProductControlHelpPageSize+1; i++ {
			r := helpCreate(ctx, t, s, scope, "workspace", "", "")
			if _, err := s.UpdateProductHelp(ctx, scope.AccountID, scope.AppID, r.Request.ID, api.ProductHelpUpdate{ExpectedVersion: 1, Status: "resolved", Response: "Recorded"}); err != nil {
				t.Fatal(err)
			}
		}
		first, err := s.ListProductHelp(ctx, scope, "all", "")
		if err != nil || len(first.Requests) != api.ProductControlHelpPageSize || first.NextCursor == "" {
			t.Fatal("first page", err)
		}
		second, err := s.ListProductHelp(ctx, scope, "all", first.NextCursor)
		if err != nil || len(second.Requests) != 1 || second.NextCursor != "" {
			t.Fatal("second page", err)
		}
		for _, r := range first.Requests {
			if r.Request.ID == second.Requests[0].Request.ID {
				t.Fatal("duplicate across pages")
			}
		}
		if _, err = s.ListProductHelp(ctx, scope, "active", first.NextCursor); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("cursor changed filter: %v", err)
		}
		if _, err = s.ListProductOwnerHelp(ctx, scope.AccountID, scope.AppID, scope.TenantID, "all", first.NextCursor); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("customer cursor reused by owner: %v", err)
		}
	})
	t.Run("concurrent open limit and duplicate recovery at capacity", func(t *testing.T) {
		f := helpFixture(ctx, t, s)
		scope := helpTestScope(f, "operator")
		var first api.ProductHelpRequest
		for range api.ProductControlHelpMaxOpen - 1 {
			first = helpCreate(ctx, t, s, scope, "workspace", "", "")
		}
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			wg.Go(func() {
				_, errs[i] = s.CreateProductHelp(ctx, scope, api.ProductHelpCreate{ID: uuid.NewString(), Kind: "workspace", Message: "Limit race"})
			})
		}
		wg.Wait()
		passed := 0
		for _, err := range errs {
			if err == nil {
				passed++
			} else {
				var limit *OperationLimitError
				if !errors.As(err, &limit) {
					t.Fatal(err)
				}
			}
		}
		if passed != 1 {
			t.Fatal("quota race admitted wrong number", passed)
		}
		if _, err := s.CreateProductHelp(ctx, scope, first.Request); err != nil {
			t.Fatal("quota blocked recovery", err)
		}
	})
	t.Run("retained limit, invalid input and paused feature help", func(t *testing.T) {
		f := helpFixture(ctx, t, s)
		scope := helpTestScope(f, "operator")
		bad := api.ProductHelpCreate{ID: uuid.NewString(), Kind: "workspace", Message: strings.Repeat("x", api.ProductControlHelpTextMaxBytes+1)}
		if _, err := s.CreateProductHelp(ctx, scope, bad); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("oversized message accepted: %v", err)
		}
		c := f.customer
		c.Suspended = true
		if _, err := s.SetProductCustomer(ctx, scope.AccountID, scope.AppID, c); err != nil {
			t.Fatal(err)
		}
		r := helpCreate(ctx, t, s, scope, "feature", "export", "")
		if r.Context.Reason != "access_paused" {
			t.Fatal("cannot report a paused feature")
		}
		if _, err := s.UpdateProductHelp(ctx, scope.AccountID, scope.AppID, r.Request.ID, api.ProductHelpUpdate{ExpectedVersion: 1, Status: "resolved", Response: "Reviewed"}); err != nil {
			t.Fatal(err)
		}
		for i := 1; i < api.ProductControlHelpMaxRequests; i++ {
			next := helpCreate(ctx, t, s, scope, "workspace", "", "")
			if _, err := s.UpdateProductHelp(ctx, scope.AccountID, scope.AppID, next.Request.ID, api.ProductHelpUpdate{ExpectedVersion: 1, Status: "resolved", Response: "Reviewed"}); err != nil {
				t.Fatal(err)
			}
		}
		_, err := s.CreateProductHelp(ctx, scope, api.ProductHelpCreate{ID: uuid.NewString(), Kind: "workspace", Message: "Over the retained limit"})
		var limit *OperationLimitError
		if !errors.As(err, &limit) {
			t.Fatalf("unbounded retained requests: %v", err)
		}
		if _, err = s.CreateProductHelp(ctx, scope, r.Request); err != nil {
			t.Fatal("retained limit blocked exact retry", err)
		}
	})
}
