package conformance

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// testDashboards pins ADR-20261022173833's stored dashboards: per-account
// unique names, the cap, replace, tenant-scoped reads and delete.
func testDashboards(t *testing.T, fx *Fixture) {
	t.Helper()
	widgets := json.RawMessage(`[{"title":"p95","kind":"app_metric","app":"shop","metric":"latency_p95_ms","window":"1h"}]`)
	create := func(name string, limit int) (state.Dashboard, error) {
		return fx.Store.CreateDashboard(fx.Ctx, state.Dashboard{AccountID: fx.Account.ID, Name: name, Widgets: widgets}, limit)
	}
	first, err := create("checkout", 2)
	if err != nil || first.ID == "" || first.Name != "checkout" || first.CreatedAt.IsZero() {
		t.Fatalf("create = %+v, %v", first, err)
	}
	var stored []map[string]any
	if err := json.Unmarshal(first.Widgets, &stored); err != nil || len(stored) != 1 || stored[0]["metric"] != "latency_p95_ms" {
		t.Fatalf("widgets = %s, %v", first.Widgets, err)
	}
	if _, err := create("checkout", 2); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate name = %v, want ErrConflict", err)
	}
	second, err := create("payments", 2)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	var quota *state.DashboardQuotaError
	if _, err := create("third", 2); !errors.As(err, &quota) || quota.Limit != 2 || quota.Observed != 2 {
		t.Fatalf("past the cap = %v, want a 2/2 quota error", err)
	}

	second.Name, second.Widgets = "payments-v2", json.RawMessage(`[]`)
	replaced, err := fx.Store.ReplaceDashboard(fx.Ctx, second)
	if err != nil || replaced.Name != "payments-v2" || string(replaced.Widgets) != "[]" {
		t.Fatalf("replace = %+v, %v", replaced, err)
	}
	second.Name = "checkout"
	if _, err := fx.Store.ReplaceDashboard(fx.Ctx, second); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("rename onto a taken name = %v, want ErrConflict", err)
	}
	other := second
	other.AccountID, other.Name = uuid.NewString(), "stolen"
	if _, err := fx.Store.ReplaceDashboard(fx.Ctx, other); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another account's replace = %v, want ErrNotFound", err)
	}

	list, err := fx.Store.ListDashboards(fx.Ctx, fx.Account.ID)
	if err != nil || len(list) != 2 || list[0].Name != "checkout" || list[1].Name != "payments-v2" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if _, err := fx.Store.DashboardByID(fx.Ctx, uuid.NewString(), first.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another account's read = %v, want ErrNotFound", err)
	}
	if got, err := fx.Store.DashboardByID(fx.Ctx, fx.Account.ID, first.ID); err != nil || got.Name != "checkout" {
		t.Fatalf("read = %+v, %v", got, err)
	}
	if err := fx.Store.DeleteDashboard(fx.Ctx, fx.Account.ID, first.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := fx.Store.DeleteDashboard(fx.Ctx, fx.Account.ID, first.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}
