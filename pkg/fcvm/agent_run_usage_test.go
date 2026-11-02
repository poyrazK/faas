// adr: 20261027090011 — vmmd measures an agent run's private memory each
// second, splits its time by the quiet pause ledger, and reports one usage
// record when the run ends.
package fcvm

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const mib = int64(1) << 20

// usageStep is one sample: the interval that ends after `after`, the private
// memory read at its end (negative: the read fails), and how much of the
// interval the VM spent paused.
type usageStep struct {
	after  time.Duration
	mb     int64
	paused time.Duration
}

func meterUsage(t *testing.T, steps []usageStep) AgentRunUsage {
	t.Helper()
	now := time.Unix(1_800_000_000, 0)
	var paused time.Duration
	var next int64
	read := func(Lease) (int64, error) {
		if next < 0 {
			return 0, errors.New("memory.stat gone")
		}
		return next * mib, nil
	}
	meter := newAgentUsageMeter(read, func(time.Time) time.Duration { return paused }, Lease{}, true, now)
	for _, s := range steps {
		now = now.Add(s.after)
		paused += s.paused
		next = s.mb
		meter.sample(now)
	}
	return meter.usage
}

func TestAgentRunUsageMeter(t *testing.T) {
	floor, overhead := int64(api.AgentRunUsageFloorMB), int64(api.PerVMOverheadMB)
	for _, tc := range []struct {
		name  string
		steps []usageStep
		want  AgentRunUsage
	}{
		{
			name:  "above the floor",
			steps: []usageStep{{time.Second, 300, 0}, {time.Second, 300, 0}, {time.Second, 600, 0}},
			want: AgentRunUsage{Samples: 3, ExpectedSamples: 3, MeasuredMillis: 3000, PrivateMBMillis: 1_200_000,
				UnpausedPrivateMBMillis: 1_200_000, BillableMBMillis: (300+overhead)*2000 + (600+overhead)*1000, PeakPrivateMB: 600},
		},
		{
			name:  "below the floor counts at the floor",
			steps: []usageStep{{time.Second, 40, 0}, {time.Second, 40, 0}},
			want: AgentRunUsage{Samples: 2, ExpectedSamples: 2, MeasuredMillis: 2000, PrivateMBMillis: 80_000,
				UnpausedPrivateMBMillis: 80_000, BillableMBMillis: (floor + overhead) * 2000, PeakPrivateMB: 40},
		},
		{
			name:  "paused time is counted apart and never billable",
			steps: []usageStep{{time.Second, 300, 0}, {time.Second, 300, 750 * time.Millisecond}, {time.Second, 300, time.Second}},
			want: AgentRunUsage{Samples: 3, ExpectedSamples: 3, MeasuredMillis: 3000, PausedMillis: 1750, PrivateMBMillis: 900_000,
				UnpausedPrivateMBMillis: 300 * 1250, BillableMBMillis: (300 + overhead) * 1250, PeakPrivateMB: 300},
		},
		{
			name:  "a failed read adds time but no memory",
			steps: []usageStep{{time.Second, 300, 0}, {time.Second, -1, 0}, {time.Second, 300, 0}},
			want: AgentRunUsage{Samples: 2, ExpectedSamples: 3, MeasuredMillis: 3000, PrivateMBMillis: 600_000,
				UnpausedPrivateMBMillis: 600_000, BillableMBMillis: (300 + overhead) * 2000, PeakPrivateMB: 300},
		},
		{
			name:  "a ledger that counts more than the interval is capped",
			steps: []usageStep{{time.Second, 300, 5 * time.Second}},
			want:  AgentRunUsage{Samples: 1, ExpectedSamples: 1, MeasuredMillis: 1000, PausedMillis: 1000, PrivateMBMillis: 300_000, PeakPrivateMB: 300},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.want.Method, tc.want.ColdBoot = AgentRunUsageMethod, true
			if got := meterUsage(t, tc.steps); got != tc.want {
				t.Fatalf("usage\n got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

// The ADR's bounds hold for any run: billable time is at least the floor and
// at most the peak (or floor) plus overhead over the unpaused read time,
// paused time is never billable, and no part exceeds the whole.
func TestPropertyAgentRunUsageBounds(t *testing.T) {
	r := rand.New(rand.NewPCG(20261027, 90011))
	floor, overhead := int64(api.AgentRunUsageFloorMB), int64(api.PerVMOverheadMB)
	for run := 0; run < 500; run++ {
		var steps []usageStep
		var unpausedReadMS int64
		for i := 0; i < 1+r.IntN(40); i++ {
			s := usageStep{after: time.Duration(200+r.IntN(1600)) * time.Millisecond, mb: int64(r.IntN(3000))}
			if r.IntN(10) == 0 {
				s.mb = -1
			}
			if r.IntN(3) == 0 {
				s.paused = time.Duration(r.Int64N(int64(s.after) + 1))
			}
			if s.mb >= 0 {
				unpausedReadMS += (s.after - s.paused).Milliseconds()
			}
			steps = append(steps, s)
		}
		u := meterUsage(t, steps)
		if u.Samples > u.ExpectedSamples || u.PausedMillis > u.MeasuredMillis || u.UnpausedPrivateMBMillis > u.PrivateMBMillis {
			t.Fatalf("run %d: a part exceeds its whole: %+v", run, u)
		}
		if lo := (floor + overhead) * unpausedReadMS; u.BillableMBMillis < lo {
			t.Fatalf("run %d: billable %d below the floor %d: %+v", run, u.BillableMBMillis, lo, u)
		}
		if hi := (max(u.PeakPrivateMB+1, floor) + overhead) * unpausedReadMS; u.BillableMBMillis > hi {
			t.Fatalf("run %d: billable %d above the peak %d: %+v", run, u.BillableMBMillis, hi, u)
		}
		if allPaused := meterUsage(t, pauseAll(steps)); allPaused.BillableMBMillis != 0 || allPaused.UnpausedPrivateMBMillis != 0 {
			t.Fatalf("run %d: a run paused throughout has billable time: %+v", run, allPaused)
		}
	}
}

func pauseAll(steps []usageStep) []usageStep {
	out := append([]usageStep(nil), steps...)
	for i := range out {
		out[i].paused = out[i].after
	}
	return out
}

// usageVMM is a run VM whose private memory the test sets.
type usageVMM struct {
	*agentRunTestVMM
	mu    sync.Mutex
	bytes int64
	reads int
}

func (v *usageVMM) VMPrivateBytes(Lease) (int64, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.reads++
	return v.bytes, nil
}

// RunAgent reports the run's usage on success and on a failed exchange, and
// takes its last sample before the VM is destroyed.
func TestRunAgentReportsUsage(t *testing.T) {
	t.Run("succeeded", func(t *testing.T) {
		vmm := &usageVMM{agentRunTestVMM: &agentRunTestVMM{fakeVMM: &fakeVMM{}}, bytes: 300 * mib}
		m := agentRunTestManager(t, vmm, &Instance{ExecutionOnly: true, AgentRunOnly: true, Method: WakeRestore})
		m.agentUsageTick = time.Millisecond
		_, usage, err := m.RunAgent(context.Background(), "agent-vm-1", agentRunTestRequest(), nil, nil)
		if err != nil || usage == nil {
			t.Fatalf("usage %+v, %v", usage, err)
		}
		if usage.Method != AgentRunUsageMethod || usage.Samples < 1 || usage.Samples != usage.ExpectedSamples ||
			usage.PeakPrivateMB != 300 || usage.ColdBoot || usage.PausedMillis != 0 {
			t.Fatalf("usage %+v", usage)
		}
	})
	t.Run("exchange failed", func(t *testing.T) {
		vmm := &usageVMM{agentRunTestVMM: &agentRunTestVMM{fakeVMM: &fakeVMM{}, dialErr: errors.New("no vsock")}, bytes: 64 * mib}
		m := agentRunTestManager(t, vmm, &Instance{ExecutionOnly: true, AgentRunOnly: true})
		_, usage, err := m.RunAgent(context.Background(), "agent-vm-1", agentRunTestRequest(), nil, nil)
		if err == nil || usage == nil || usage.ExpectedSamples != 1 || !usage.ColdBoot || usage.PeakPrivateMB != 64 {
			t.Fatalf("usage %+v, %v", usage, err)
		}
		if m.LiveCount() != 0 {
			t.Fatal("the VM outlived the failed run")
		}
	})
	t.Run("no memory reader", func(t *testing.T) {
		m := agentRunTestManager(t, &agentRunTestVMM{fakeVMM: &fakeVMM{}}, &Instance{ExecutionOnly: true, AgentRunOnly: true})
		if _, usage, err := m.RunAgent(context.Background(), "agent-vm-1", agentRunTestRequest(), nil, nil); err != nil || usage != nil {
			t.Fatalf("usage %+v, %v", usage, err)
		}
	})
}
