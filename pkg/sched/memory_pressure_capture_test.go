package sched

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQueueMemoryPressureCapture(t *testing.T) {
	ctx := t.Context()
	mem := state.NewMemStore()
	seed := func(email string, plan api.Plan, instanceState state.State) (state.App, state.Instance) {
		t.Helper()
		acct, err := mem.CreateAccount(ctx, email, plan)
		if err != nil {
			t.Fatal(err)
		}
		app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: strings.Split(email, "@")[0], RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		ins, err := mem.CreateInstance(ctx, app.ID, "", string(instanceState), 256, "node-1", "")
		if err != nil {
			t.Fatal(err)
		}
		return app, ins
	}
	notif := &fakeNotifier{}
	e := newEngine(t, mem, &fakeVMM{}, notif, "1.10.0")
	app, ins := seed("pressure@example.com", api.PlanHobby, state.StateRunning)

	queued, reason, err := e.QueueMemoryPressureCapture(ctx, ins.ID, 30, 246, 190)
	if err != nil || !queued || reason != "" {
		t.Fatalf("first report: queued=%v reason=%q err=%v", queued, reason, err)
	}
	list, _ := mem.ListProfileCaptures(ctx, app.AccountID, app.ID)
	if len(list) != 1 || list[0].Trigger != api.ProfileCaptureTriggerMemoryPressure || list[0].RequestedInstanceID != ins.ID ||
		len(list[0].Kinds) != 1 || list[0].Kinds[0] != api.ProfileKindHeap || !strings.Contains(list[0].TriggerDetail, "30 MiB of 246 MiB") {
		t.Fatalf("queued capture = %+v", list)
	}
	notif.mu.Lock()
	notified := len(notif.events) == 1 && notif.events[0].channel == "profile_capture"
	notif.mu.Unlock()
	if !notified {
		t.Fatal("schedd drain was not notified")
	}
	if queued, reason, _ := e.QueueMemoryPressureCapture(ctx, ins.ID, 20, 246, 200); queued || !strings.Contains(reason, "already queued") {
		t.Fatalf("concurrent report: queued=%v reason=%q", queued, reason)
	}
	claimed, err := mem.ClaimProfileCapture(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.FinishProfileCapture(ctx, claimed.ID, api.ProfileCapture{Status: api.ProfileCaptureReady}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if queued, reason, _ := e.QueueMemoryPressureCapture(ctx, ins.ID, 20, 246, 200); queued || !strings.Contains(reason, "minimum interval") {
		t.Fatalf("report inside the interval: queued=%v reason=%q", queued, reason)
	}
	_, free := seed("free-pressure@example.com", api.PlanFree, state.StateRunning)
	if queued, reason, _ := e.QueueMemoryPressureCapture(ctx, free.ID, 10, 120, 100); queued || !strings.Contains(reason, "plan") {
		t.Fatalf("free plan: queued=%v reason=%q", queued, reason)
	}
	_, parked := seed("parked-pressure@example.com", api.PlanHobby, state.StateParked)
	if queued, reason, _ := e.QueueMemoryPressureCapture(ctx, parked.ID, 10, 246, 200); queued || !strings.Contains(reason, "not running") {
		t.Fatalf("parked instance: queued=%v reason=%q", queued, reason)
	}
	if _, _, err := e.QueueMemoryPressureCapture(ctx, "00000000-0000-4000-8000-000000000000", 1, 2, 3); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unknown instance err = %v", err)
	}
}
