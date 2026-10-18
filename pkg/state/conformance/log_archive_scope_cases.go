package conformance

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// testInstanceLogsOwnedByApp pins archived-log scoping: an instance's logs
// belong to an app through its live instance row, and after that row is
// deleted through the app's billing rows near the requested day. Any other
// account, app, window or malformed ID is refused.
func testInstanceLogsOwnedByApp(t *testing.T, fx *Fixture) {
	scope, ok := fx.Store.(state.LogArchiveScopeStore)
	if !ok {
		t.Fatalf("%T does not implement state.LogArchiveScopeStore", fx.Store)
	}
	ins, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID, string(state.StateRunning), 512, fx.Node.ID, uuid.NewString())
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	day := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	from, to := day.AddDate(0, 0, -1), day.AddDate(0, 0, 2)
	owned := func(account, app, instance string, from, to time.Time) bool {
		t.Helper()
		got, err := scope.InstanceLogsOwnedByApp(fx.Ctx, account, app, instance, from, to)
		if err != nil {
			t.Fatalf("InstanceLogsOwnedByApp: %v", err)
		}
		return got
	}
	if !owned(fx.Account.ID, fx.App.ID, ins.ID, from, to) {
		t.Fatal("live instance row does not prove ownership")
	}
	for name, args := range map[string][3]string{
		"other app":         {fx.Account.ID, uuid.NewString(), ins.ID},
		"other account":     {uuid.NewString(), fx.App.ID, ins.ID},
		"unknown instance":  {fx.Account.ID, fx.App.ID, uuid.NewString()},
		"malformed account": {"acct-1", fx.App.ID, ins.ID},
		"malformed app":     {fx.Account.ID, "app-1", ins.ID},
		"malformed id":      {fx.Account.ID, fx.App.ID, "../" + ins.ID},
	} {
		if owned(args[0], args[1], args[2], from, to) {
			t.Errorf("%s: ownership granted", name)
		}
	}

	// After retention deletes the instance row, billing rows prove it.
	minute := day.Add(12*time.Hour + 34*time.Minute)
	perSecond := int64(fx.App.RAMMB + api.PerVMOverheadMB)
	if err := fx.Store.AppendUsage(fx.Ctx, fx.Account.ID, fx.App.ID, ins.ID, minute, perSecond, 1, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("AppendUsage: %v", err)
	}
	if err := fx.Store.DeleteInstance(fx.Ctx, ins.ID); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}
	if !owned(fx.Account.ID, fx.App.ID, ins.ID, from, to) {
		t.Fatal("billing rows do not prove ownership after the instance row is gone")
	}
	if owned(fx.Account.ID, fx.App.ID, ins.ID, to, to.AddDate(0, 0, 3)) {
		t.Fatal("billing rows outside the window granted ownership")
	}
	if owned(fx.Account.ID, uuid.NewString(), ins.ID, from, to) {
		t.Fatal("billing rows granted ownership to another app")
	}
}
