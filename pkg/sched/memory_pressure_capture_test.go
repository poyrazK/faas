package sched

// adr: 1155 pins automatic heap captures on guest memory pressure.

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

func TestQueueSlowRequestCapture(t *testing.T) {
	ctx := t.Context()
	mem := state.NewMemStore()
	seed := func(email string, plan api.Plan) (state.App, state.Instance) {
		t.Helper()
		acct, err := mem.CreateAccount(ctx, email, plan)
		if err != nil {
			t.Fatal(err)
		}
		app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: strings.Split(email, "@")[0], RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		ins, err := mem.CreateInstance(ctx, app.ID, "", string(state.StateRunning), 256, "node-1", "")
		if err != nil {
			t.Fatal(err)
		}
		return app, ins
	}
	e := newEngine(t, mem, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	app, ins := seed("slow@example.com", api.PlanHobby)
	queued, reason, err := e.QueueSlowRequestCapture(ctx, ins.ID, "req-1", "trace-1", "GET /orders/{id}", 12*time.Second)
	if err != nil || !queued || reason != "" {
		t.Fatalf("first report: queued=%v reason=%q err=%v", queued, reason, err)
	}
	list, _ := mem.ListProfileCaptures(ctx, app.AccountID, app.ID)
	if len(list) != 1 || list[0].Trigger != api.ProfileCaptureTriggerSlowRequest || list[0].RequestID != "req-1" || list[0].RequestTraceID != "trace-1" ||
		len(list[0].Kinds) != 2 || list[0].Kinds[0] != api.ProfileKindStacks || list[0].Kinds[1] != api.ProfileKindCPU ||
		list[0].DurationSeconds != int(api.ProfileCaptureSlowRequestDuration/time.Second) || list[0].TriggerDetail != "GET /orders/{id} in flight for 12s" {
		t.Fatalf("queued capture = %+v", list)
	}
	if queued, reason, _ := e.QueueSlowRequestCapture(ctx, ins.ID, "req-2", "trace-2", "", 11*time.Second); queued || !strings.Contains(reason, "already queued") {
		t.Fatalf("concurrent report: queued=%v reason=%q", queued, reason)
	}
	_, free := seed("free-slow@example.com", api.PlanFree)
	if queued, reason, _ := e.QueueSlowRequestCapture(ctx, free.ID, "req-3", "trace-3", "GET /", 10*time.Second); queued || !strings.Contains(reason, "plan") {
		t.Fatalf("free plan: queued=%v reason=%q", queued, reason)
	}
}

func TestLinkOOMProfileCapture(t *testing.T) {
	ctx := t.Context()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "oom-link@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "oom-link", RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	e := newEngine(t, mem, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	now := time.Now().UTC()
	capture := api.ProfileCapture{ID: "11111111-1111-4111-8111-111111111111", Kinds: []string{api.ProfileKindHeap}, DurationSeconds: 10,
		Trigger: api.ProfileCaptureTriggerMemoryPressure, CreatedAt: now.Add(-5 * time.Second), ExpiresAt: now.Add(api.ProfileCaptureRetention)}
	if err := mem.QueueAutomaticProfileCapture(ctx, acct.ID, app.ID, capture, api.ProfileCaptureAutomaticMinInterval, api.MustLimitsFor(api.PlanPro).Profiling.CaptureStorageBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.ClaimProfileCapture(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := mem.StartProfileCapture(ctx, capture.ID, "instance-1", "deployment-1"); err != nil {
		t.Fatal(err)
	}
	e.linkOOMProfileCapture(ctx, app.ID, "instance-2", now, 263, 256) // another instance: no link
	e.linkOOMProfileCapture(ctx, app.ID, "instance-1", now, 263, 256)
	got, err := mem.GetProfileCapture(ctx, acct.ID, app.ID, capture.ID)
	if err != nil || got.OOMKilledAt == nil || !got.OOMKilledAt.Equal(now) || got.OOMPeakMB != 263 || got.OOMPlanMB != 256 {
		t.Fatalf("linked capture = %+v %v", got, err)
	}
}

func TestProfileCaptureRequestCheckpoints(t *testing.T) {
	claimed := func(trigger string, seconds int, kinds ...string) state.ClaimedProfileCapture {
		return state.ClaimedProfileCapture{ID: "11111111-1111-4111-8111-111111111111", Capture: api.ProfileCapture{Trigger: trigger, DurationSeconds: seconds, Kinds: kinds}}
	}
	for _, tc := range []struct {
		name string
		c    state.ClaimedProfileCapture
		want int64
	}{
		{"memory pressure heap", claimed(api.ProfileCaptureTriggerMemoryPressure, 10, api.ProfileKindHeap), api.ProfileCaptureCheckpointInterval.Milliseconds()},
		{"manual heap", claimed(api.ProfileCaptureTriggerManual, 10, api.ProfileKindHeap), 0},
		{"memory pressure cpu", claimed(api.ProfileCaptureTriggerMemoryPressure, 10, api.ProfileKindCPU), 0},
		{"window shorter than the interval", claimed(api.ProfileCaptureTriggerMemoryPressure, 2, api.ProfileKindHeap), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := profileCaptureRequest(tc.c)
			if req.CheckpointMillis != tc.want || req.CaptureID != "11111111111141118111111111111111" {
				t.Fatalf("request = %+v", req)
			}
			if err := req.Validate(); err != nil {
				t.Fatalf("request invalid: %v", err)
			}
		})
	}
}
