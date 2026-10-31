package sched_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// One instance that cannot be read (its ring not registered yet while it
// cold-boots, or its node unreachable) used to fail the whole one-shot
// fetch. The other instances' lines now come back, with a gap frame naming
// the instance that could not be read.
func TestEngineStreamAppLogs_OneShotSurvivesOneUnreadableInstance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	store := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, store, api.PlanHobby, 256, 5)
	ok, err := store.CreateInstance(ctx, app.ID, "", string(state.StateRunning), 256, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}
	waking, err := store.CreateInstance(ctx, app.ID, "", string(state.StateWaking), 256, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}
	stream := newProgrammableLogStream(sched.LogLine{Seq: 1, Stream: "stdout", Line: "served"})
	stream.close()
	vmm := &deploymentFilterFakeVMM{
		perInstanceStream: map[string]sched.LogStream{state.DefaultLocalNodeName + "/" + ok.ID: stream},
		perInstanceErr:    map[string]error{state.DefaultLocalNodeName + "/" + waking.ID: errors.New("instance not live on this vmmd")},
	}
	eng := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	sink := &frameSink{}
	if err := eng.StreamAppLogs(ctx, app.ID, 0, time.Time{}, false, "", sink.sink()); err != nil {
		t.Fatalf("StreamAppLogs: %v", err)
	}
	var line, gap bool
	for _, f := range sink.snapshot() {
		line = line || (f.InstanceID == ok.ID && f.Line == "served")
		gap = gap || (f.InstanceID == waking.ID && f.IsGap && f.GapReason == "instance_unavailable")
	}
	if !line || !gap {
		t.Fatalf("frames = %+v, want the readable line and a gap for the waking instance", sink.snapshot())
	}
}

// A follow stream attaches an instance that wakes after it started with its
// whole ring (since 1). It used to reuse the request's since 0, "only new
// lines", and lose the lines written before the 250 ms discovery poll.
func TestEngineStreamAppLogs_LateInstanceReplaysItsRing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, store, api.PlanHobby, 256, 5)
	first, err := store.CreateInstance(ctx, app.ID, "", string(state.StateRunning), 256, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}
	firstStream := newProgrammableLogStream()
	vmm := &deploymentFilterFakeVMM{perInstanceStream: map[string]sched.LogStream{state.DefaultLocalNodeName + "/" + first.ID: firstStream}}
	eng := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	done := make(chan error, 1)
	go func() {
		done <- eng.StreamAppLogs(ctx, app.ID, 0, time.Time{}, true, "", (&frameSink{}).sink())
	}()
	waitDials := func(n int) []recordedDial {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if dials := vmm.dialed(); len(dials) >= n {
				return dials
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("dials = %+v, want %d", vmm.dialed(), n)
		return nil
	}
	if dials := waitDials(1); dials[0].SinceSeq != 0 {
		t.Fatalf("an instance live at attach keeps the request's cursor: %+v", dials[0])
	}

	vmm.mu.Lock()
	late, err := store.CreateInstance(ctx, app.ID, "", string(state.StateRunning), 256, state.DefaultLocalNodeName, "")
	if err != nil {
		vmm.mu.Unlock()
		t.Fatal(err)
	}
	lateStream := newProgrammableLogStream()
	vmm.perInstanceStream[state.DefaultLocalNodeName+"/"+late.ID] = lateStream
	vmm.mu.Unlock()
	dials := waitDials(2)
	if dials[1].InstanceID != late.ID || dials[1].SinceSeq != 1 {
		t.Fatalf("late instance dial = %+v, want its ring from seq 1", dials[1])
	}
	firstStream.close()
	lateStream.close()
	cancel()
	<-done
}
