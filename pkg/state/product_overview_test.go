package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type productOverviewTestStore interface {
	state.ProductBillingTestStore
	state.ProductCustomerOverviewStore
}

func TestProductControlOverviewMemory(t *testing.T) {
	s := state.NewMemStore()
	insert := func(op state.Operation) {
		(*s.Mu()).Lock()
		defer (*s.Mu()).Unlock()
		s.InsertProductOperationLocked(op)
	}
	settle := func(op state.Operation) {
		(*s.Mu()).Lock()
		defer (*s.Mu()).Unlock()
		s.SettleProductOperationLocked(op)
	}
	productOverviewContract(t, s, state.SeedProductBilling(t, s, insert, settle))
}

func productOverviewContract(t *testing.T, s productOverviewTestStore, f state.ProductBillingFixture) {
	t.Helper()
	ctx, scope := t.Context(), (*f.Scope())
	check := func(unresolved, review int64, period string) {
		t.Helper()
		got, err := s.GetProductCustomerWorkSummary(ctx, scope.AccountID, scope.AppID, scope.TenantID)
		if err != nil || got.Unresolved != unresolved || got.NeedsReview != review || got.ReviewPeriod != period {
			t.Fatalf("work summary: %+v, %v; want %d/%d/%s", got, err, unresolved, review, period)
		}
	}
	// These historical receipts have no retained Operation rows. Pending proof
	// survives result expiry; terminal successes, failures and cancellations do not count.
	check(2, 2, scope.Period)
	other, err := s.GetProductCustomerWorkSummary(ctx, (*f.Other()).AccountID, (*f.Other()).AppID, (*f.Other()).TenantID)
	if err != nil || other != (api.ProductCustomerWorkSummary{}) {
		t.Fatalf("another customer's outcomes leaked: %+v %v", other, err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: scope.AppID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:overview"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := s.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: scope.AccountID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: scope.AppID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/export", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"exporting"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AdmitOperation(ctx, state.OperationAdmission{AccountID: scope.AccountID, PlatformTenantID: scope.TenantID, DefinitionID: def.ID, IdempotencyKey: "overview-live-work", Input: json.RawMessage(`{}`), ProductFeature: "export", ProductControlEnabled: true}); err != nil {
		t.Fatal(err)
	}
	// Current running work is pending, while the oldest historical review month
	// stays visible. A current-month-only billing read would miss these two cases.
	check(3, 2, scope.Period)
	if _, err = s.WaiveProductReceipt(ctx, scope, (*f.Ops())["expired"].ID, "Internal pilot write-off"); err != nil {
		t.Fatal(err)
	}
	check(2, 1, scope.Period)
	op := (*f.Ops())["unknown"]
	op.State, op.UpdatedAt = api.OperationSucceeded, time.Now().UTC()
	(*f.Settle())(op)
	check(1, 0, "")
	for _, bad := range []struct{ account, app, tenant string }{{uuid.NewString(), scope.AppID, scope.TenantID}, {scope.AccountID, uuid.NewString(), scope.TenantID}, {scope.AccountID, scope.AppID, uuid.NewString()}} {
		if _, err = s.GetProductCustomerWorkSummary(ctx, bad.account, bad.app, bad.tenant); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("unowned scope exposed: %v", err)
		}
	}
	if _, err = s.GetProductCustomerWorkSummary(ctx, scope.AccountID, scope.AppID, "invalid"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid tenant: %v", err)
	}
}
