// adr: 1155 — a finished capture keeps the profiles the store can hold,
// retries transient store errors, and otherwise finishes failed with a reason
// instead of expiring as interrupted.
package sched_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProfileCaptureOutcomeDropsProfilesOutsideStoreBounds(t *testing.T) {
	out := sched.ProfileCaptureOutcome{InstanceID: "i", Result: profileproto.CaptureResult{Processes: 3, Dropped: 1, Profiles: []profileproto.CapturedProfile{
		{Kind: api.ProfileKindCPU, ProcessID: "7", Profile: []byte("ok")},
		{Kind: api.ProfileKindCPU, ProcessID: strings.Repeat("9", 17), Profile: []byte("long process id")},
		{Kind: api.ProfileKindHeap, ProcessID: "8", Profile: make([]byte, api.ProfileMaxCompressedBytes+1)},
	}}}
	result, blobs := sched.ProfileCaptureOutcomeOf(out, nil)
	if result.Status != api.ProfileCaptureReady || len(blobs) != 1 || len(result.Profiles) != 1 || result.Dropped != 3 || blobs[0].ProcessID != "7" {
		t.Fatalf("outcome = %+v, blobs %d", result, len(blobs))
	}
}

type recordingFinisher struct {
	errs  []error // returned in order; nil once exhausted
	calls []api.ProfileCapture
	blobs []int
}

func (f *recordingFinisher) FinishProfileCapture(_ context.Context, _ string, result api.ProfileCapture, blobs []state.ProfileCaptureBlob, _ time.Time) error {
	f.calls = append(f.calls, result)
	f.blobs = append(f.blobs, len(blobs))
	if len(f.errs) == 0 {
		return nil
	}
	err := f.errs[0]
	f.errs = f.errs[1:]
	return err
}

func TestFinishProfileCapture(t *testing.T) {
	transient := errors.New("connection reset")
	ready := api.ProfileCapture{Status: api.ProfileCaptureReady, InstanceID: "i", DeploymentID: "d", Profiles: []api.ProfileCaptureProfile{{Kind: api.ProfileKindCPU}}}
	blobs := []state.ProfileCaptureBlob{{Kind: api.ProfileKindCPU, ProcessID: "7", Profile: []byte("p")}}
	for _, tc := range []struct {
		name      string
		result    api.ProfileCapture
		errs      []error
		wantErr   bool
		wantCalls int
		wantWaits []time.Duration
		// lastFailed: the final call stored a failed capture with no profiles.
		lastFailed bool
	}{
		{name: "stored first time", result: ready, wantCalls: 1},
		{name: "transient error retried", result: ready, errs: []error{transient, transient}, wantCalls: 3, wantWaits: []time.Duration{time.Second, 2 * time.Second}},
		{name: "row gone is final", result: ready, errs: []error{state.ErrNotFound}, wantErr: true, wantCalls: 1},
		{name: "profiles never stored", result: ready, errs: []error{transient, transient, transient}, wantErr: true, wantCalls: 4,
			wantWaits: []time.Duration{time.Second, 2 * time.Second}, lastFailed: true},
		{name: "failed capture not retried as failed again", result: api.ProfileCapture{Status: api.ProfileCaptureFailed, Reason: "r"},
			errs: []error{transient, transient, transient}, wantErr: true, wantCalls: 3, wantWaits: []time.Duration{time.Second, 2 * time.Second}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &recordingFinisher{errs: tc.errs}
			var waits []time.Duration
			err := sched.FinishProfileCapture(t.Context(), f, "c", tc.result, blobs, func(d time.Duration) { waits = append(waits, d) })
			if (err != nil) != tc.wantErr || len(f.calls) != tc.wantCalls || len(waits) != len(tc.wantWaits) {
				t.Fatalf("err %v, calls %d, waits %v", err, len(f.calls), waits)
			}
			for i, w := range tc.wantWaits {
				if waits[i] != w {
					t.Fatalf("waits = %v, want %v", waits, tc.wantWaits)
				}
			}
			last := f.calls[len(f.calls)-1]
			if tc.lastFailed && (last.Status != api.ProfileCaptureFailed || f.blobs[len(f.blobs)-1] != 0 || !strings.Contains(last.Reason, "could not be stored") || last.InstanceID != "i") {
				t.Fatalf("fallback finish = %+v with %d profiles", last, f.blobs[len(f.blobs)-1])
			}
		})
	}
}
