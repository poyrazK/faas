// adr: 022 — the resume-hook ack carries an optional guest-side timing frame
// after the ADR-687 capability byte.

package fcvm

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/resumetimingproto"
)

// Production-us measured resume_hook ~190 ms for Node and Python apps and
// ~58 ms for Go, but vmmd could only see connect (~10 ms) and "waiting for
// the ack". The guest's own stages must reach the stage histogram, and a
// guest that sends no frame, or a broken one, must still restore.
func TestTriggerResumeHookReadsGuestTimings(t *testing.T) {
	timings := resumetimingproto.Timings{
		ReadUs: 50, EntropyUs: 2000, HWRNGUs: 1500, ClockUs: 100, UUIDMarkerUs: 5000,
		UserspaceReseedUs: 120000, ReseedClients: 1, ReseedSlowestUs: 119000, ReseedSlowestRuntime: "node",
		TotalUs: 130000,
	}
	frame, err := resumetimingproto.AppendFrame(nil, timings)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name         string
		caps         byte
		tail         []byte
		wantObserved bool
	}{
		{name: "guest sends a timing frame", caps: resumeCapUserspaceReseed | resumetimingproto.CapTimings, tail: frame, wantObserved: true},
		{name: "timing frame is malformed", caps: resumeCapUserspaceReseed | resumetimingproto.CapTimings, tail: []byte{0, 0, 0, 3, 'x'}},
		{name: "guest predates the frame", caps: resumeCapUserspaceReseed},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chrootBase := shortChrootBase(t, "gt")
			instance := "i" + string(rune('A'+i))
			root := filepath.Join(chrootBase, "f", instance, "root")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			l, err := net.Listen("unix", filepath.Join(root, VsockUDSSocketName))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = l.Close() })
			go func() {
				c, aErr := l.Accept()
				if aErr != nil {
					return
				}
				handleFakeVsockHook(t, &guestAckRewriteConn{Conn: c, caps: tc.caps, tail: tc.tail}, ackOK, nil)
			}()

			wpm := NewWakePhaseMetrics()
			v := &JailerVMM{chrootBase: chrootBase, fcName: "f", wakePhaseMetrics: wpm}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := v.TriggerResumeHook(ctx, Lease{Instance: instance}, 1); err != nil {
				t.Fatalf("TriggerResumeHook: %v", err)
			}
			counts, sums := resumeGuestStageSamples(t, wpm)
			wantCount := uint64(0)
			if tc.wantObserved {
				wantCount = 1
			}
			for _, stage := range resumeGuestStageNames {
				if counts[stage] != wantCount {
					t.Errorf("stage %s observed %d times, want %d", stage, counts[stage], wantCount)
				}
			}
			if tc.wantObserved {
				if got := sums["userspace_reseed"]; got < 0.119 || got > 0.121 {
					t.Errorf("userspace_reseed sum = %v s, want 0.12", got)
				}
			}
		})
	}
}

func TestResumeGuestOutside(t *testing.T) {
	cases := []struct {
		name    string
		totalUs int64
		ackWait time.Duration
		want    time.Duration
	}{
		{name: "host waited longer than the guest worked", totalUs: 130000, ackWait: 180 * time.Millisecond, want: 50 * time.Millisecond},
		{name: "clock skew between host and guest clamps to zero", totalUs: 130000, ackWait: 129 * time.Millisecond, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resumeGuestOutside(resumetimingproto.Timings{TotalUs: tc.totalUs}, tc.ackWait); got != tc.want {
				t.Fatalf("resumeGuestOutside = %v, want %v", got, tc.want)
			}
		})
	}
	var nilMetrics *WakePhaseMetrics
	nilMetrics.ObserveResumeGuestStages(resumetimingproto.Timings{TotalUs: 1}, time.Millisecond) // nil-safe
}

// guestAckRewriteConn turns the fake guest's [OK, reseed capability] ack into
// [OK, caps] followed by tail, the shape a timing-capable guest-init writes.
type guestAckRewriteConn struct {
	net.Conn
	caps byte
	tail []byte
}

func (c *guestAckRewriteConn) Write(p []byte) (int, error) {
	if len(p) == 2 && p[0] == ackOK && p[1] == resumeCapUserspaceReseed {
		_, err := c.Conn.Write(append([]byte{ackOK, c.caps}, c.tail...))
		return len(p), err
	}
	return c.Conn.Write(p)
}

func resumeGuestStageSamples(t *testing.T, wpm *WakePhaseMetrics) (map[string]uint64, map[string]float64) {
	t.Helper()
	families, err := wpm.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	counts, sums := map[string]uint64{}, map[string]float64{}
	for _, f := range families {
		if f.GetName() != "vmmd_resume_guest_stage_duration_seconds" {
			continue
		}
		for _, m := range f.GetMetric() {
			stage := m.GetLabel()[0].GetValue()
			counts[stage] = m.GetHistogram().GetSampleCount()
			sums[stage] = m.GetHistogram().GetSampleSum()
		}
	}
	if len(counts) != len(resumeGuestStageNames) {
		t.Fatalf("stages = %d, want the closed set of %d", len(counts), len(resumeGuestStageNames))
	}
	return counts, sums
}
