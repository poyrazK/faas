// adr: 1151 — forwards resume a quiet-paused instance before dialing it, and
// Stats reports quiet pause so schedd can leave paused vCPUs out of admission.
package vmmdgrpc

import (
	"context"
	"errors"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type quietStubVMM struct {
	stubVMM
	resumeErr error
	resumed   []string
	paused    bool
	since     time.Time
}

func (q *quietStubVMM) QuietResume(_ context.Context, instance string) error {
	q.resumed = append(q.resumed, instance)
	return q.resumeErr
}

func (q *quietStubVMM) QuietPauseState(string) (bool, time.Time) {
	return q.paused, q.since
}

func TestResumeQuiet(t *testing.T) {
	for _, tc := range []struct {
		name      string
		resumeErr error
		wantCode  codes.Code
	}{
		{name: "resumed", wantCode: codes.OK},
		{name: "resume fails fast", resumeErr: errors.New("boom"), wantCode: codes.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &quietStubVMM{resumeErr: tc.resumeErr}
			s := New(q, nil, "1.17.0", nil)
			err := s.resumeQuiet(t.Context(), "vm-a")
			if status.Code(err) != tc.wantCode {
				t.Fatalf("resumeQuiet err = %v, want code %v", err, tc.wantCode)
			}
			if len(q.resumed) != 1 || q.resumed[0] != "vm-a" {
				t.Fatalf("QuietResume calls = %v", q.resumed)
			}
		})
	}
}

func TestResumeQuietWithoutCapability(t *testing.T) {
	s := New(stubVMM{}, nil, "1.17.0", nil)
	if err := s.resumeQuiet(t.Context(), "vm-a"); err != nil {
		t.Fatalf("resumeQuiet on a VMM without quiet pause: %v", err)
	}
}

func TestAddQuietPauseState(t *testing.T) {
	since := time.Unix(1_700_000_000, 0)
	for _, tc := range []struct {
		name      string
		vmm       VmmdAPI
		wantPause bool
		wantSince bool
	}{
		{name: "paused", vmm: &quietStubVMM{paused: true, since: since}, wantPause: true, wantSince: true},
		{name: "running", vmm: &quietStubVMM{}},
		{name: "no capability", vmm: stubVMM{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(tc.vmm, nil, "1.17.0", nil)
			row := &vmmdpb.InstanceStats{Instance: "vm-a"}
			s.addQuietPauseState(row)
			if row.GetQuietPaused() != tc.wantPause || (row.GetQuietPausedSince() != nil) != tc.wantSince {
				t.Fatalf("row = paused %v since %v", row.GetQuietPaused(), row.GetQuietPausedSince())
			}
			if tc.wantSince && !row.GetQuietPausedSince().AsTime().Equal(since) {
				t.Fatalf("quiet_paused_since = %v, want %v", row.GetQuietPausedSince().AsTime(), since)
			}
		})
	}
}

func TestQuietPauseAfterFromWire(t *testing.T) {
	for _, tc := range []struct {
		ms   int64
		want time.Duration
	}{
		{ms: 0, want: 0},
		{ms: -5, want: 0},
		{ms: 2000, want: 2 * time.Second},
		{ms: int64(maxQuietPauseAfter/time.Millisecond) + 1, want: 0},
	} {
		if got := quietPauseAfterFromWire(tc.ms); got != tc.want {
			t.Errorf("quietPauseAfterFromWire(%d) = %v, want %v", tc.ms, got, tc.want)
		}
	}
}
