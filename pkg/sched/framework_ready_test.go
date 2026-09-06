package sched

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFrameworkReadyOwnedPersistenceAndReplay(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.7.0")
	result, err := e.Wake(ctx, app.ID, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = e.ReportFrameworkReady(ctx, result.InstanceID); err != nil {
		t.Fatal(err)
	}
	first, _ := store.InstanceByID(ctx, result.InstanceID)
	if first.FrameworkReadyAt == nil {
		t.Fatal("missing readiness stamp")
	}
	if err = e.ReportFrameworkReady(ctx, result.InstanceID); err != nil {
		t.Fatal(err)
	}
	again, _ := store.InstanceByID(ctx, result.InstanceID)
	if !again.FrameworkReadyAt.Equal(*first.FrameworkReadyAt) {
		t.Fatal("replay moved age floor")
	}
	if err = store.UpdateInstanceState(ctx, result.InstanceID, string(state.StateStopped)); err != nil {
		t.Fatal(err)
	}
	if err = e.ReportFrameworkReady(ctx, result.InstanceID); err == nil {
		t.Fatal("stamped stopped instance")
	}
	if err = e.ReportFrameworkReady(ctx, "missing"); err == nil {
		t.Fatal("accepted missing instance")
	}
}
