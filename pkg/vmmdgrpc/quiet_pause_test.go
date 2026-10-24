// adr: 1151 — forwards resume a quiet-paused instance before dialing it, and
// Stats reports quiet pause so schedd can leave paused vCPUs out of admission
// and record completed pauses in the billing ledger (ADR-20261023171905).
package vmmdgrpc

import (
	"context"
	"errors"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type quietStubVMM struct {
	stubVMM
	resumeErr error
	resumed   []string
	report    fcvm.QuietPauseReport
}

func (q *quietStubVMM) QuietResume(_ context.Context, instance string) error {
	q.resumed = append(q.resumed, instance)
	return q.resumeErr
}

func (q *quietStubVMM) QuietPauseReport(string) fcvm.QuietPauseReport {
	return q.report
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
	lastStarted, lastEnded := since.Add(-time.Minute), since.Add(-50*time.Second)
	completed := fcvm.QuietPauseReport{LastStarted: lastStarted, LastEnded: lastEnded, PausedTotal: 10 * time.Second}
	open := completed
	open.Paused, open.Since = true, since
	for _, tc := range []struct {
		name      string
		vmm       VmmdAPI
		wantPause bool
		wantSince bool
		wantLast  bool
	}{
		{name: "paused, never resumed", vmm: &quietStubVMM{report: fcvm.QuietPauseReport{Paused: true, Since: since}}, wantPause: true, wantSince: true},
		{name: "running after a pause", vmm: &quietStubVMM{report: completed}, wantLast: true},
		{name: "paused again", vmm: &quietStubVMM{report: open}, wantPause: true, wantSince: true, wantLast: true},
		{name: "running, never paused", vmm: &quietStubVMM{}},
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
			gotLast := row.GetQuietLastPauseStartedAt() != nil
			if gotLast != tc.wantLast || (row.GetQuietLastPauseEndedAt() != nil) != tc.wantLast || (row.GetQuietPausedNsTotal() != nil) != tc.wantLast {
				t.Fatalf("last pause = [%v, %v] total %v, want present=%v", row.GetQuietLastPauseStartedAt(), row.GetQuietLastPauseEndedAt(), row.GetQuietPausedNsTotal(), tc.wantLast)
			}
			if tc.wantLast {
				if !row.GetQuietLastPauseStartedAt().AsTime().Equal(lastStarted) || !row.GetQuietLastPauseEndedAt().AsTime().Equal(lastEnded) {
					t.Fatalf("last pause = [%v, %v], want [%v, %v]", row.GetQuietLastPauseStartedAt().AsTime(), row.GetQuietLastPauseEndedAt().AsTime(), lastStarted, lastEnded)
				}
				if got := row.GetQuietPausedNsTotal().GetValue(); got != (10 * time.Second).Nanoseconds() {
					t.Fatalf("quiet_paused_ns_total = %d, want %d", got, (10 * time.Second).Nanoseconds())
				}
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
