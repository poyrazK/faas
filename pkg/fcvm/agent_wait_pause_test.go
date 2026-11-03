// adr: 20261027090012 — vmmd pauses an agent run VM while it waits on the
// model through the relay and does nothing else, and resumes it before the
// relay's next bytes, a cancel or the run's deadline reach the guest.
package fcvm

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

type fakeAgentWaitCPU struct {
	mu      sync.Mutex
	seconds map[string]float64
}

func (c *fakeAgentWaitCPU) CPUSeconds(id string) (float64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.seconds[id]
	return s, ok
}

func (c *fakeAgentWaitCPU) set(id string, s float64) {
	c.mu.Lock()
	c.seconds[id] = s
	c.mu.Unlock()
}

const agentWaitID = "agent-vm-1"

type agentWaitFixture struct {
	m    *Manager
	vmm  *pausingVMM
	cpu  *fakeAgentWaitCPU
	inst *Instance
	stop func()
}

// newAgentWaitFixture registers one live agent run VM with a 2 s model-wait
// delay and the pause on, as RunAgent does at the start of its exchange.
func newAgentWaitFixture(t *testing.T, edit func(*Instance), pauseOn bool) *agentWaitFixture {
	t.Helper()
	f := &agentWaitFixture{vmm: &pausingVMM{fakeVMM: &fakeVMM{}}, cpu: &fakeAgentWaitCPU{seconds: map[string]float64{}}}
	f.inst = &Instance{ExecutionOnly: true, AgentRunOnly: true, AgentWaitPauseAfter: 2 * time.Second}
	f.m = agentRunTestManager(t, f.vmm, f.inst)
	if edit != nil {
		edit(f.inst)
	}
	if pauseOn {
		f.m.WithAgentWaitPause(AgentWaitPauseConfig{CPU: f.cpu})
	}
	_, _, f.stop = f.m.agentWaitRun(t.Context(), agentWaitID, f.inst, time.Hour, nil, nil)
	t.Cleanup(f.stop)
	return f
}

// waitedOnModel makes the run look like it has waited on one open relay
// connection for 3 s, with cpuMillis of CPU over that window.
func (f *agentWaitFixture) waitedOnModel(t *testing.T, now time.Time, cpuMillis float64) *AgentRelay {
	t.Helper()
	relay := f.m.AgentRelayOpened(agentWaitID)
	f.m.agentWaitMu.Lock()
	if st := f.m.agentWaits[agentWaitID]; st != nil {
		st.lastActivity = now.Add(-3 * time.Second)
	}
	f.m.agentWaitMu.Unlock()
	if f.m.agentWaitOn() {
		f.cpu.set(agentWaitID, 1.0)
		f.m.agentWaitSample(agentWaitID, 2*time.Second, now.Add(-3*time.Second))
	}
	f.cpu.set(agentWaitID, 1.0+cpuMillis/1000)
	return relay
}

func (f *agentWaitFixture) paused() bool {
	p, _ := f.m.QuietPauseState(agentWaitID)
	return p
}

func TestAgentWaitPausePass(t *testing.T) {
	for _, tc := range []struct {
		name      string
		pauseOn   bool
		edit      func(*Instance)
		noRelay   bool
		recent    bool
		cpuMillis float64
		noCPU     bool
		stopping  bool
		wantPause bool
	}{
		{name: "waits on the relay with idle CPU", pauseOn: true, cpuMillis: 10, wantPause: true},
		{name: "switch off", cpuMillis: 10},
		{name: "no model-wait delay", pauseOn: true, edit: func(i *Instance) { i.AgentWaitPauseAfter = 0 }, cpuMillis: 10},
		{name: "networked VM", pauseOn: true, edit: func(i *Instance) { i.Lease.Networkless = false }, cpuMillis: 10},
		{name: "no relay connection open", pauseOn: true, noRelay: true, cpuMillis: 10},
		{name: "relay bytes just now", pauseOn: true, recent: true, cpuMillis: 10},
		{name: "harness using CPU", pauseOn: true, cpuMillis: 80},
		{name: "no CPU reading", pauseOn: true, noCPU: true},
		{name: "cancelled or past its deadline", pauseOn: true, stopping: true, cpuMillis: 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAgentWaitFixture(t, tc.edit, tc.pauseOn)
			now := time.Now()
			relay := f.waitedOnModel(t, now, tc.cpuMillis)
			if tc.noRelay && relay != nil {
				relay.Close()
				f.m.agentWaitMu.Lock()
				f.m.agentWaits[agentWaitID].lastActivity = now.Add(-3 * time.Second)
				f.m.agentWaitMu.Unlock()
			}
			if tc.recent && relay != nil {
				relay.FromGuest()
			}
			if tc.noCPU {
				f.cpu.mu.Lock()
				delete(f.cpu.seconds, agentWaitID)
				f.cpu.mu.Unlock()
			}
			if tc.stopping {
				f.m.agentWaitMu.Lock()
				f.m.agentWaits[agentWaitID].stopping = true
				f.m.agentWaitMu.Unlock()
			}
			f.m.quietPausePass(t.Context(), now)
			if got := f.vmm.pauses(); (got == 1) != tc.wantPause {
				t.Fatalf("pauses = %d, want paused %v", got, tc.wantPause)
			}
			if f.paused() != tc.wantPause {
				t.Fatalf("paused state = %v, want %v", f.paused(), tc.wantPause)
			}
		})
	}
}

// Bytes from outboundd resume the VM before they are handed on to be
// written to the guest; bytes from the guest only count as activity.
func TestAgentRelayResumesBeforeBytesReachTheGuest(t *testing.T) {
	f := newAgentWaitFixture(t, nil, true)
	now := time.Now()
	relay := f.waitedOnModel(t, now, 5)
	f.m.quietPausePass(t.Context(), now)
	if !f.paused() {
		t.Fatal("the waiting run was not paused")
	}

	var guest bytes.Buffer
	if _, err := io.Copy(&guest, relay.Reader(t.Context(), bytes.NewReader([]byte("ping")), false)); err != nil {
		t.Fatal(err)
	}
	if !f.paused() || f.vmm.resumes() != 0 {
		t.Fatal("bytes from the guest resumed the VM")
	}

	var out bytes.Buffer
	if _, err := io.Copy(&out, relay.Reader(t.Context(), bytes.NewReader([]byte("model response")), true)); err != nil {
		t.Fatal(err)
	}
	if out.String() != "model response" || f.paused() || f.vmm.resumes() != 1 {
		t.Fatalf("out %q, paused %v, resumes %d: want the bytes after one resume", out.String(), f.paused(), f.vmm.resumes())
	}
	// The resume counted as activity: the next pass does not pause again.
	f.m.quietPausePass(t.Context(), time.Now())
	if f.vmm.pauses() != 1 {
		t.Fatalf("pauses = %d right after relay bytes", f.vmm.pauses())
	}
}

// A cancel resumes the guest before the cancel frame is written, and the run
// is never paused again; so does the run's deadline.
func TestAgentWaitCancelAndDeadlineResume(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		f := newAgentWaitFixture(t, nil, true)
		f.stop()
		cancel := make(chan struct{})
		_, wrapped, stop := f.m.agentWaitRun(t.Context(), agentWaitID, f.inst, time.Hour, nil, cancel)
		defer stop()
		now := time.Now()
		f.waitedOnModel(t, now, 5)
		f.m.quietPausePass(t.Context(), now)
		if !f.paused() {
			t.Fatal("the waiting run was not paused")
		}
		close(cancel)
		select {
		case <-wrapped:
		case <-time.After(5 * time.Second):
			t.Fatal("the cancel was not passed on")
		}
		if f.paused() || f.vmm.resumes() != 1 {
			t.Fatalf("paused %v, resumes %d after the cancel", f.paused(), f.vmm.resumes())
		}
		f.m.agentWaitMu.Lock()
		stopping := f.m.agentWaits[agentWaitID].stopping
		f.m.agentWaitMu.Unlock()
		if !stopping {
			t.Fatal("a cancelled run can still be paused")
		}
	})
	t.Run("deadline", func(t *testing.T) {
		f := newAgentWaitFixture(t, nil, true)
		f.stop()
		_, _, stop := f.m.agentWaitRun(t.Context(), agentWaitID, f.inst, 500*time.Millisecond, nil, nil)
		defer stop()
		now := time.Now()
		f.waitedOnModel(t, now, 5)
		f.m.quietPausePass(t.Context(), now)
		if !f.paused() {
			t.Fatal("the waiting run was not paused")
		}
		deadline := time.Now().Add(5 * time.Second)
		for f.paused() && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if f.paused() {
			t.Fatal("the run's deadline did not resume the VM")
		}
	})
}

// Output frames count as activity, and the run's state is gone once its
// exchange ends, so a later relay connection keeps the plain splice.
func TestAgentWaitRunOutputAndCleanup(t *testing.T) {
	f := newAgentWaitFixture(t, nil, true)
	f.stop()
	var got []byte
	receive, _, stop := f.m.agentWaitRun(t.Context(), agentWaitID, f.inst, time.Hour, func(_ context.Context, _ string, chunk []byte) error {
		got = append(got, chunk...)
		return nil
	}, nil)
	f.waitedOnModel(t, time.Now(), 5)
	if err := receive(t.Context(), "stdout", []byte("turn 1")); err != nil {
		t.Fatal(err)
	}
	f.m.quietPausePass(t.Context(), time.Now())
	if string(got) != "turn 1" || f.vmm.pauses() != 0 {
		t.Fatalf("got %q, pauses %d: an output frame is activity", got, f.vmm.pauses())
	}
	stop()
	if relay := f.m.AgentRelayOpened(agentWaitID); relay != nil {
		t.Fatal("a relay opened after the run ended is still tracked")
	}
}
