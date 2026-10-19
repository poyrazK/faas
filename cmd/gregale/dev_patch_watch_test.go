package main_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestWatchDevPatchReportsFirstTerminalState(t *testing.T) {
	applied := time.Now()
	responses := []api.DevPatchStatusResponse{
		{Generation: 4, State: api.DevPatchStatePending},
		{Generation: 4, State: api.DevPatchStatePending},
		{Generation: 4, State: api.DevPatchStateApplied, AppliedAt: &applied, ApplyMS: 90},
	}
	calls := 0
	status := func(_ context.Context, generation int64) (api.DevPatchStatusResponse, error) {
		if generation != 4 {
			t.Fatalf("polled generation %d", generation)
		}
		calls++
		if calls == 1 {
			return api.DevPatchStatusResponse{}, errors.New("transient")
		}
		return responses[min(calls-2, len(responses)-1)], nil
	}
	var got []api.DevPatchStatusResponse
	main.WatchDevPatch(context.Background(), 4, status, time.Millisecond, time.Second, func(s api.DevPatchStatusResponse, _ time.Time) {
		got = append(got, s)
	})
	if len(got) != 1 || got[0].State != api.DevPatchStateApplied {
		t.Fatalf("delivered = %+v, want one applied report", got)
	}
}

func TestWatchDevPatchStopsOnTimeoutAndCancel(t *testing.T) {
	pending := func(context.Context, int64) (api.DevPatchStatusResponse, error) {
		return api.DevPatchStatusResponse{State: api.DevPatchStatePending}, nil
	}
	report := func(api.DevPatchStatusResponse, time.Time) { t.Fatal("pending patch was reported") }
	main.WatchDevPatch(context.Background(), 1, pending, time.Millisecond, 20*time.Millisecond, report)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	main.WatchDevPatch(ctx, 1, pending, time.Millisecond, time.Hour, report)
}

func TestDevPatchPhaseInSummaryAndHistory(t *testing.T) {
	tracker := main.NewDevPhaseTracker()
	applied := time.Now()
	d := tracker.PatchDelivered(api.DevPatchStatusResponse{State: api.DevPatchStateApplied, AppliedAt: &applied}, (*tracker.StartedAt()).Add(800*time.Millisecond))
	if d != 800*time.Millisecond {
		t.Fatalf("edit-to-patch = %s, want 800ms on the CLI clock", d)
	}
	tracker.SourceSync(200*time.Millisecond, nil)
	var out bytes.Buffer
	tracker.Render(&out)
	if !strings.Contains(out.String(), "sync=200ms · patch=800ms") {
		t.Fatalf("summary = %q, want the patch phase after sync", out.String())
	}

	if note := main.DevHistoryPatchNote([]api.DevSyncPhase{{Phase: "build", Status: "completed", DurationMS: 4000}}); note != "" {
		t.Fatalf("history note without a patch = %q", note)
	}
	if note := main.DevHistoryPatchNote([]api.DevSyncPhase{{Phase: "patch", Status: "completed", DurationMS: 750}}); note != "  (live patch 750ms)" {
		t.Fatalf("history note = %q", note)
	}
	if note := main.DevHistoryPatchNote([]api.DevSyncPhase{{Phase: "patch", Status: "failed", Reason: "apply_failed"}}); note != "  (live patch failed: apply_failed)" {
		t.Fatalf("failed history note = %q", note)
	}
}
