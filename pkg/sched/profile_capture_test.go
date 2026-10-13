package sched

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestProfileCaptureOutcomeMapping(t *testing.T) {
	out := ProfileCaptureOutcome{InstanceID: "i", DeploymentID: "d", Result: profileproto.CaptureResult{Processes: 2, Profiles: []profileproto.CapturedProfile{{Kind: "cpu", ProcessID: "7", Profile: []byte("abc")}}}}
	result, blobs := profileCaptureOutcome(out, nil)
	if result.Status != api.ProfileCaptureReady || result.Processes != 2 || len(blobs) != 1 || result.Profiles[0].Bytes != 3 || result.InstanceID != "i" {
		t.Fatalf("ready outcome: %+v %+v", result, blobs)
	}
	for _, tt := range []struct {
		err  error
		want string
	}{
		{ErrNoRunningInstance, "no running instance"},
		{status.Error(codes.FailedPrecondition, "guest has no profile collectors for on-demand capture"), "no profile collectors"},
		{status.Error(codes.NotFound, "gone"), "stopped before"},
		{status.Error(codes.DeadlineExceeded, "slow"), "timed out"},
		{status.Error(codes.Unavailable, "internal detail"), "could not reach"},
	} {
		result, blobs := profileCaptureOutcome(ProfileCaptureOutcome{}, tt.err)
		if result.Status != api.ProfileCaptureFailed || blobs != nil || !strings.Contains(result.Reason, tt.want) {
			t.Fatalf("%v -> %+v", tt.err, result)
		}
	}
}

func TestPickProfileInstance(t *testing.T) {
	t0 := time.Unix(1000, 0)
	rows := []state.Instance{
		{ID: "parked", State: string(state.StateParked), NodeID: "n1", StartedAt: t0},
		{ID: "late", State: string(state.StateRunning), NodeID: "n1", StartedAt: t0.Add(time.Minute)},
		{ID: "early", State: string(state.StateRunning), NodeID: "n2", StartedAt: t0},
		{ID: "unplaced", State: string(state.StateRunning), StartedAt: t0.Add(-time.Minute)},
	}
	tests := []struct {
		name, requested, want string
		ok                    bool
	}{
		{name: "earliest running", want: "early", ok: true},
		{name: "requested running", requested: "late", want: "late", ok: true},
		{name: "requested parked", requested: "parked"},
		{name: "requested unplaced", requested: "unplaced"},
		{name: "requested unknown", requested: "missing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := pickProfileInstance(rows, tt.requested)
			if ok != tt.ok || got.ID != tt.want {
				t.Fatalf("pick(%q) = %q, %v; want %q, %v", tt.requested, got.ID, ok, tt.want, tt.ok)
			}
		})
	}
	if _, ok := pickProfileInstance(nil, ""); ok {
		t.Fatal("empty app yielded an instance")
	}
}
