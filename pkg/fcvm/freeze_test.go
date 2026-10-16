// adr: 1151 — a frozen instance is paused in place and reclaimed only on
// request (ADR-906), refuses what a paused guest cannot serve, and every
// failed freeze leaves the guest running.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type freezingVMM struct {
	*fakeVMM
	callsMu    sync.Mutex
	calls      []string
	pauseErr   error
	reclaimErr error
	resident   int64
}

func (v *freezingVMM) PauseVM(_ context.Context, l Lease) error {
	v.callsMu.Lock()
	defer v.callsMu.Unlock()
	v.calls = append(v.calls, "pause:"+l.Instance)
	return v.pauseErr
}

func (v *freezingVMM) ReclaimVMMemory(l Lease) (VMMemoryReclaim, error) {
	v.callsMu.Lock()
	defer v.callsMu.Unlock()
	v.calls = append(v.calls, "reclaim:"+l.Instance)
	return VMMemoryReclaim{BeforeBytes: 200 << 20, AfterBytes: 60 << 20, SwappedBytes: 120 << 20}, v.reclaimErr
}

func (v *freezingVMM) VMResidentBytes(Lease) (int64, error) {
	if v.resident == 0 {
		return 0, errors.New("no memory.current")
	}
	return v.resident, nil
}

func (v *freezingVMM) callList() []string {
	v.callsMu.Lock()
	defer v.callsMu.Unlock()
	return slices.Clone(v.calls)
}

func (v *freezingVMM) resumes() []string {
	v.fakeVMM.mu.Lock()
	defer v.fakeVMM.mu.Unlock()
	return slices.Clone(v.fakeVMM.resumed)
}

// newFreezeManager wakes instance "f" on a VMM that can freeze.
func newFreezeManager(t *testing.T, v *freezingVMM) *Manager {
	t.Helper()
	m := newTestManager(&fakeRunner{}, v)
	if _, err := m.Wake(t.Context(), admissionWake("f")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "f") })
	return m
}

func TestFreezeThaw(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name       string
		reclaim    bool
		pauseErr   error
		reclaimErr error
		wantCalls  []string
		wantReport FreezeReport
	}{
		{name: "pause only", wantCalls: []string{"pause:f"}, wantReport: FreezeReport{BeforeBytes: 90 << 20, AfterBytes: 90 << 20}},
		{name: "pause and reclaim", reclaim: true, wantCalls: []string{"pause:f", "reclaim:f"},
			wantReport: FreezeReport{BeforeBytes: 200 << 20, AfterBytes: 60 << 20, SwappedBytes: 120 << 20}},
		{name: "pause fails", pauseErr: boom, wantCalls: []string{"pause:f"}},
		{name: "reclaim fails", reclaim: true, reclaimErr: boom, wantCalls: []string{"pause:f", "reclaim:f"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &freezingVMM{fakeVMM: &fakeVMM{}, pauseErr: tc.pauseErr, reclaimErr: tc.reclaimErr, resident: 90 << 20}
			m := newFreezeManager(t, v)
			rep, err := m.Freeze(t.Context(), "f", FreezeOptions{Reclaim: tc.reclaim})
			failed := tc.pauseErr != nil || tc.reclaimErr != nil
			if failed != (err != nil) || (failed && !errors.Is(err, boom)) {
				t.Fatalf("Freeze error = %v", err)
			}
			if got := v.callList(); !slices.Equal(got, tc.wantCalls) {
				t.Fatalf("VMM calls = %v, want %v", got, tc.wantCalls)
			}
			frozen, since := m.FrozenState("f")
			if frozen == failed || frozen == since.IsZero() {
				t.Fatalf("FrozenState = %v, %v after Freeze err=%v", frozen, since, err)
			}
			if failed {
				// Every failed freeze resumes the guest and leaves it serving.
				if !slices.Equal(v.resumes(), []string{"f"}) {
					t.Fatalf("resumes after a failed freeze = %v, want [f]", v.resumes())
				}
				return
			}
			rep.Pause, rep.Reclaim = 0, 0
			if rep != tc.wantReport {
				t.Fatalf("report = %+v, want %+v", rep, tc.wantReport)
			}
			if len(v.resumes()) != 0 || m.LiveCount() != 1 || len(m.instanceFlights) != 0 || m.FrozenCount() != 1 {
				t.Fatalf("after freeze: resumes=%v live=%d flights=%d frozen=%d",
					v.resumes(), m.LiveCount(), len(m.instanceFlights), m.FrozenCount())
			}
			if err := m.Thaw(t.Context(), "f"); err != nil {
				t.Fatalf("Thaw: %v", err)
			}
			if !slices.Equal(v.resumes(), []string{"f"}) {
				t.Fatalf("Thaw resumes = %v, want [f]", v.resumes())
			}
			if frozen, since := m.FrozenState("f"); frozen || !since.IsZero() || m.FrozenCount() != 0 {
				t.Fatalf("after thaw: FrozenState = %v, %v; count %d", frozen, since, m.FrozenCount())
			}
		})
	}
}

func TestFreezeIsIdempotent(t *testing.T) {
	v := &freezingVMM{fakeVMM: &fakeVMM{}}
	m := newFreezeManager(t, v)
	if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); err != nil {
		t.Fatal(err)
	}
	_, since := m.FrozenState("f")
	// With Reclaim a second freeze compresses instead (TestCompressFrozenInstance).
	rep, err := m.Freeze(t.Context(), "f", FreezeOptions{})
	if err != nil || rep != (FreezeReport{}) {
		t.Fatalf("second Freeze = %+v, %v; want a zero report and no error", rep, err)
	}
	if got := v.callList(); !slices.Equal(got, []string{"pause:f"}) {
		t.Fatalf("VMM calls = %v, want one pause", got)
	}
	if frozen, again := m.FrozenState("f"); !frozen || !again.Equal(since) {
		t.Fatalf("FrozenState after the second freeze = %v, %v; want unchanged %v", frozen, again, since)
	}
}

func TestFreezeRefusals(t *testing.T) {
	plain := newTestManager(&fakeRunner{}, &fakeVMM{})
	if _, err := plain.Wake(t.Context(), admissionWake("p")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plain.Destroy(context.Background(), "p") })
	if _, err := plain.Freeze(t.Context(), "p", FreezeOptions{}); err == nil {
		t.Fatal("Freeze succeeded on a VMM without the capability")
	}
	v := &freezingVMM{fakeVMM: &fakeVMM{}}
	if _, err := newTestManager(&fakeRunner{}, v).Freeze(t.Context(), "missing", FreezeOptions{}); err == nil ||
		!strings.Contains(err.Error(), "not live") {
		t.Fatalf("Freeze of an instance that is not live = %v", err)
	}

	for _, tc := range []struct {
		name   string
		edit   func(*Instance)
		reason string
	}{
		{name: "warm pool", edit: func(i *Instance) { i.Paused = true }, reason: "warm-pool"},
		{name: "execution", edit: func(i *Instance) { i.ExecutionOnly = true }, reason: "execution"},
		{name: "job", edit: func(i *Instance) { i.IsJob = true }, reason: "job"},
		{name: "app task", edit: func(i *Instance) { i.AppTaskOnly = true }, reason: "app task"},
		{name: "qualification", edit: func(i *Instance) { i.QualificationOnly = true }, reason: "qualification"},
		{name: "builder", edit: func(i *Instance) { i.Lease.IsBuilder = true }, reason: "builder"},
		{name: "private network", edit: func(i *Instance) { i.Net.PrivateNetworkBridge = "pn-1" }, reason: "private network"},
		{name: "waitUntil tail", edit: func(i *Instance) { i.TailCount = 1 }, reason: "waitUntil"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &freezingVMM{fakeVMM: &fakeVMM{}}
			m := newFreezeManager(t, v)
			m.mu.Lock()
			tc.edit(m.live["f"])
			m.mu.Unlock()
			_, err := m.Freeze(t.Context(), "f", FreezeOptions{Reclaim: true})
			if !errors.Is(err, ErrNotFreezable) || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("Freeze = %v, want ErrNotFreezable naming %q", err, tc.reason)
			}
			if calls := v.callList(); len(calls) != 0 {
				t.Fatalf("refused freeze reached the VMM: %v", calls)
			}
			if frozen, _ := m.FrozenState("f"); frozen {
				t.Fatal("refused freeze marked the instance frozen")
			}
			// The edited flags change teardown; restore an ordinary app VM.
			m.mu.Lock()
			inst := m.live["f"]
			inst.Paused, inst.ExecutionOnly, inst.IsJob, inst.AppTaskOnly = false, false, false, false
			inst.QualificationOnly, inst.Lease.IsBuilder, inst.Net.PrivateNetworkBridge, inst.TailCount = false, false, "", 0
			m.mu.Unlock()
		})
	}
}

func TestFreezeRefusesInstanceWithForwardInFlight(t *testing.T) {
	act := newFakeQuietActivity()
	act.set("f", 1, time.Now())
	v := &freezingVMM{fakeVMM: &fakeVMM{}}
	m := newFreezeManager(t, v).WithFreeze(FreezeConfig{Activity: act})
	if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); !errors.Is(err, ErrInstanceBusy) {
		t.Fatalf("Freeze with a forward in flight = %v, want ErrInstanceBusy", err)
	}
	if calls := v.callList(); len(calls) != 0 {
		t.Fatalf("refused freeze reached the VMM: %v", calls)
	}
	// The refused freeze withdrew its gate: a forward does not wait.
	if frozen, err := m.AwaitFrozen(t.Context(), "f"); frozen || err != nil {
		t.Fatalf("AwaitFrozen after a refused freeze = %v, %v", frozen, err)
	}
	act.set("f", 0, time.Now())
	if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); err != nil {
		t.Fatalf("Freeze once the forward ended: %v", err)
	}
}

func TestAwaitFrozen(t *testing.T) {
	v := &freezingVMM{fakeVMM: &fakeVMM{}}
	m := newFreezeManager(t, v)
	if frozen, err := m.AwaitFrozen(t.Context(), "missing"); frozen || err != nil {
		t.Fatalf("AwaitFrozen(missing) = %v, %v", frozen, err)
	}
	if frozen, err := m.AwaitFrozen(t.Context(), "f"); frozen || err != nil {
		t.Fatalf("AwaitFrozen(running) = %v, %v", frozen, err)
	}

	// A forward that finds a freeze deciding waits for its outcome.
	m.mu.Lock()
	inst := m.live["f"]
	m.mu.Unlock()
	gate, _, err := m.announceFreeze(inst)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan bool, 1)
	go func() {
		frozen, _ := m.AwaitFrozen(context.Background(), "f")
		result <- frozen
	}()
	select {
	case <-result:
		t.Fatal("AwaitFrozen returned before the freeze settled")
	case <-time.After(20 * time.Millisecond):
	}
	m.mu.Lock()
	inst.Frozen, inst.FrozenSince = true, time.Now()
	m.mu.Unlock()
	m.settleFreeze(inst, gate)
	select {
	case frozen := <-result:
		if !frozen {
			t.Fatal("AwaitFrozen = false after the freeze took effect")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AwaitFrozen did not return once the freeze settled")
	}

	// A caller that gives up stops waiting.
	gate = make(chan struct{})
	m.mu.Lock()
	inst.freezing = gate
	m.mu.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.AwaitFrozen(ctx, "f"); !errors.Is(err, context.Canceled) {
		t.Fatalf("AwaitFrozen with a cancelled context = %v", err)
	}
	m.settleFreeze(inst, gate)
}

// quietFreezingVMM can both quiet-pause and freeze.
type quietFreezingVMM struct {
	*pausingVMM
}

func (v *quietFreezingVMM) ReclaimVMMemory(Lease) (VMMemoryReclaim, error) {
	return VMMemoryReclaim{}, nil
}

func TestQuietPauseLeavesFrozenInstanceAlone(t *testing.T) {
	act := newFakeQuietActivity()
	v := &quietFreezingVMM{pausingVMM: &pausingVMM{fakeVMM: &fakeVMM{}}}
	m := newTestManager(&fakeRunner{}, v).WithQuietPause(QuietPauseConfig{Activity: act})
	if _, err := m.Wake(t.Context(), quietWake("q")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "q") })
	seenQuiet(m, "q", time.Now().Add(-time.Hour))
	act.set("q", 0, time.Now().Add(-time.Minute))

	// Freezing a quiet-paused instance takes over its pause.
	m.quietPausePass(t.Context(), time.Now())
	if paused, _ := m.QuietPauseState("q"); !paused {
		t.Fatal("instance was not quiet-paused")
	}
	if _, err := m.Freeze(t.Context(), "q", FreezeOptions{}); err != nil {
		t.Fatal(err)
	}
	if paused, _ := m.QuietPauseState("q"); paused || v.pauses() != 2 || v.resumes() != 1 {
		t.Fatalf("after freeze: quiet paused=%v pauses=%d resumes=%d, want a quiet resume then the freeze's pause",
			paused, v.pauses(), v.resumes())
	}

	// The quiet pass never pauses a frozen instance, and a quiet resume
	// never resumes one.
	m.quietPausePass(t.Context(), time.Now())
	if err := m.QuietResume(t.Context(), "q"); err != nil {
		t.Fatal(err)
	}
	if v.pauses() != 2 || v.resumes() != 1 {
		t.Fatalf("quiet pause touched a frozen instance: pauses=%d resumes=%d", v.pauses(), v.resumes())
	}
	if frozen, _ := m.FrozenState("q"); !frozen {
		t.Fatal("instance no longer frozen")
	}
	m.mu.Lock()
	eligible := m.quietEligibleLocked("q", m.live["q"])
	m.mu.Unlock()
	if eligible {
		t.Fatal("quietEligibleLocked accepted a frozen instance")
	}
}

func TestSnapshotPathsRefuseFrozenInstance(t *testing.T) {
	spec := SnapshotSpec{StorageKey: "snap/f/mem", VMStateStorageKey: "snap/f/vmstate"}
	for _, tc := range []struct {
		name string
		call func(*Manager) error
	}{
		{name: "park", call: func(m *Manager) error { _, err := m.Park(context.Background(), "f", spec); return err }},
		{name: "warm snapshot", call: func(m *Manager) error { _, err := m.WarmSnapshot(context.Background(), "f", spec); return err }},
		{name: "snapshot keep alive", call: func(m *Manager) error {
			_, err := m.SnapshotKeepAlive(context.Background(), "f", spec)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &freezingVMM{fakeVMM: &fakeVMM{}}
			m := newFreezeManager(t, v)
			if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := tc.call(m); !errors.Is(err, ErrInstanceFrozen) {
				t.Fatalf("%s of a frozen instance = %v, want ErrInstanceFrozen", tc.name, err)
			}
			v.fakeVMM.mu.Lock()
			snapshots := len(v.fakeVMM.snapshotted) + len(v.fakeVMM.keepAliveSnapshotted)
			v.fakeVMM.mu.Unlock()
			if snapshots != 0 || len(v.resumes()) != 0 {
				t.Fatalf("refused %s reached the VMM: snapshots=%d resumes=%v", tc.name, snapshots, v.resumes())
			}
			if frozen, _ := m.FrozenState("f"); !frozen || m.LiveCount() != 1 {
				t.Fatalf("refused %s changed the instance: frozen=%v live=%d", tc.name, frozen, m.LiveCount())
			}
		})
	}
}

func TestStopFrozenInstanceKillsWithoutGrace(t *testing.T) {
	for _, frozen := range []bool{false, true} {
		t.Run(map[bool]string{false: "running", true: "frozen"}[frozen], func(t *testing.T) {
			v := &freezingVMM{fakeVMM: &fakeVMM{}}
			m := newFreezeManager(t, v)
			if frozen {
				if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := m.SignalAndKill(t.Context(), "f", syscall.SIGTERM, 30*time.Second); err != nil {
				t.Fatal(err)
			}
			v.fakeVMM.mu.Lock()
			calls := slices.Clone(v.fakeVMM.signalAndKillCalls)
			v.fakeVMM.mu.Unlock()
			want := signalAndKillCall{Instance: "f", Signal: int32(syscall.SIGTERM), Grace: 30 * time.Second}
			if frozen {
				want = signalAndKillCall{Instance: "f"}
			}
			if len(calls) != 1 || calls[0] != want {
				t.Fatalf("SignalAndKill calls = %+v, want [%+v]", calls, want)
			}
			if m.LiveCount() != 0 || len(v.resumes()) != 0 {
				t.Fatalf("after stop: live=%d resumes=%v", m.LiveCount(), v.resumes())
			}
		})
	}
}

func TestDestroyFrozenInstance(t *testing.T) {
	v := &freezingVMM{fakeVMM: &fakeVMM{}}
	m := newFreezeManager(t, v)
	if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := m.Destroy(t.Context(), "f"); err != nil {
		t.Fatalf("Destroy of a frozen instance: %v", err)
	}
	v.fakeVMM.mu.Lock()
	destroyed := slices.Clone(v.fakeVMM.destroyedWithExport)
	v.fakeVMM.mu.Unlock()
	if !slices.Equal(destroyed, []string{"f"}) || m.LiveCount() != 0 || m.FrozenCount() != 0 || m.LeasedCount() != 0 {
		t.Fatalf("after destroy: destroyed=%v live=%d frozen=%d leased=%d",
			destroyed, m.LiveCount(), m.FrozenCount(), m.LeasedCount())
	}
	if frozen, _ := m.FrozenState("f"); frozen {
		t.Fatal("destroyed instance still reported frozen")
	}
}

func TestThawOfInstanceThatIsNotFrozen(t *testing.T) {
	v := &freezingVMM{fakeVMM: &fakeVMM{}}
	m := newFreezeManager(t, v)
	if err := m.Thaw(t.Context(), "f"); err != nil {
		t.Fatalf("Thaw of a running instance: %v", err)
	}
	if len(v.resumes()) != 0 {
		t.Fatalf("Thaw of a running instance resumed it: %v", v.resumes())
	}
	if err := m.Thaw(t.Context(), "missing"); err == nil || !strings.Contains(err.Error(), "not live") {
		t.Fatalf("Thaw of a missing instance = %v, want not live", err)
	}
}

func TestThawFailureKeepsInstanceFrozen(t *testing.T) {
	v := &freezingVMM{fakeVMM: &fakeVMM{}}
	m := newFreezeManager(t, v)
	if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	v.fakeVMM.mu.Lock()
	v.fakeVMM.resumeErr = boom
	v.fakeVMM.mu.Unlock()
	if err := m.Thaw(t.Context(), "f"); !errors.Is(err, boom) {
		t.Fatalf("Thaw = %v, want %v", err, boom)
	}
	if frozen, _ := m.FrozenState("f"); !frozen {
		t.Fatal("a failed thaw cleared the frozen state of a guest that is still paused")
	}
}

type freezingImageCheckVMM struct {
	*imageReadinessManagerVMM
}

func (v *freezingImageCheckVMM) PauseVM(context.Context, Lease) error { return nil }
func (v *freezingImageCheckVMM) ReclaimVMMemory(Lease) (VMMemoryReclaim, error) {
	return VMMemoryReclaim{}, nil
}

func TestThawRunsRequiredImageHealthcheck(t *testing.T) {
	v := &freezingImageCheckVMM{imageReadinessManagerVMM: &imageReadinessManagerVMM{fakeVMM: &fakeVMM{}}}
	m := newTestManager(&fakeRunner{}, v)
	request := WakeRequest{Instance: "image", BaseKey: "/base", LayerKey: "/layer", VcpuCount: 2, MemSizeMiB: 256, Plan: api.PlanHobby, ImageHealthcheckRequired: true}
	if _, err := m.Wake(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "image") })
	if _, err := m.Freeze(t.Context(), "image", FreezeOptions{}); err != nil {
		t.Fatal(err)
	}
	checks, hooks := v.checks, len(v.resumeHookCalls)
	if err := m.Thaw(t.Context(), "image"); err != nil {
		t.Fatal(err)
	}
	if v.checks != checks+1 || len(v.resumeHookCalls) != hooks+1 {
		t.Fatalf("thaw ran %d healthchecks and %d resume hooks, want one each", v.checks-checks, len(v.resumeHookCalls)-hooks)
	}
}

// A thaw runs the resume hook, and with it the app's after_restore
// callback, only when schedd asks for it; a failed callback fails the thaw.
func TestThawWithRunsResumeHookWhenAsked(t *testing.T) {
	v := &freezingVMM{fakeVMM: &fakeVMM{}}
	m := newFreezeManager(t, v)
	for _, run := range []bool{false, true} {
		if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); err != nil {
			t.Fatal(err)
		}
		hooks := len(v.resumeHookCalls)
		if err := m.ThawWith(t.Context(), "f", ThawOptions{RunResumeHook: run}); err != nil {
			t.Fatal(err)
		}
		want := 0
		if run {
			want = 1
		}
		if got := len(v.resumeHookCalls) - hooks; got != want {
			t.Fatalf("RunResumeHook=%v ran %d resume hooks, want %d", run, got, want)
		}
	}
	if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); err != nil {
		t.Fatal(err)
	}
	v.resumeHookErr = ErrAfterRestoreHook
	if err := m.ThawWith(t.Context(), "f", ThawOptions{RunResumeHook: true}); !errors.Is(err, ErrAfterRestoreHook) {
		t.Fatalf("thaw with a failing after_restore = %v, want ErrAfterRestoreHook", err)
	}
}

func TestFreezeMetrics(t *testing.T) {
	v := &freezingVMM{fakeVMM: &fakeVMM{}, resident: 48 << 20}
	m := newFreezeManager(t, v)
	reg := prometheus.NewRegistry()
	m.WithFreeze(FreezeConfig{Metrics: NewFreezeMetrics(reg, m)})
	gauges := func() (float64, float64) {
		t.Helper()
		families, err := reg.Gather()
		if err != nil {
			t.Fatal(err)
		}
		var count, bytes float64
		for _, f := range families {
			switch f.GetName() {
			case "node_frozen_instances":
				count = f.GetMetric()[0].GetGauge().GetValue()
			case "node_frozen_resident_bytes":
				bytes = f.GetMetric()[0].GetGauge().GetValue()
			}
		}
		return count, bytes
	}
	if count, bytes := gauges(); count != 0 || bytes != 0 {
		t.Fatalf("before freeze: frozen=%v resident=%v", count, bytes)
	}
	if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); err != nil {
		t.Fatal(err)
	}
	if count, bytes := gauges(); count != 1 || bytes != 48<<20 {
		t.Fatalf("frozen: frozen=%v resident=%v, want 1 and %d", count, bytes, 48<<20)
	}
	if err := m.Thaw(t.Context(), "f"); err != nil {
		t.Fatal(err)
	}
	if count, bytes := gauges(); count != 0 || bytes != 0 {
		t.Fatalf("thawed: frozen=%v resident=%v", count, bytes)
	}
	if n, err := testutil.GatherAndCount(reg, "vmmd_freeze_seconds", "vmmd_thaw_seconds"); err != nil || n != 2 {
		t.Fatalf("freeze/thaw histograms = %d series, %v; want 2", n, err)
	}
	var nilMetrics *FreezeMetrics
	nilMetrics.observeFreeze(time.Second)
	nilMetrics.observeThaw(time.Second)
}

// The fake memory.reclaim is a regular file, so it holds the last chunk
// requested: file pages (20 MiB) arrive as 16 + 4 MiB, and file plus anon
// pages (28 MiB) as 16 + 12 MiB.
func TestReclaimVMMemoryRequestsReclaimablePagesInChunks(t *testing.T) {
	meminfo := filepath.Join(t.TempDir(), "meminfo")
	saved := procMeminfoPath
	procMeminfoPath = meminfo
	t.Cleanup(func() { procMeminfoPath = saved })
	for _, tc := range []struct {
		lease     Lease
		swapMax   string
		swapFree  string
		lastChunk int64
	}{
		{lease: Lease{Instance: "noswap", Plan: api.PlanHobby}, swapMax: "max", swapFree: "0", lastChunk: 4 << 20},
		{lease: Lease{Instance: "swap", Plan: api.PlanHobby}, swapMax: "max", swapFree: "1048576", lastChunk: 12 << 20},
		{lease: Lease{Instance: "swapoff", Plan: api.PlanPro}, swapMax: "0", swapFree: "1048576", lastChunk: 4 << 20},
		{lease: Lease{Instance: "build", IsBuilder: true}, swapMax: "max", swapFree: "1048576", lastChunk: 12 << 20},
	} {
		parent := ParentCgroupFor(tc.lease.Plan)
		if tc.lease.IsBuilder {
			parent = BuilderCgroupParent
		}
		scope := filepath.Join(cgroupRoot, parent, PerInstanceScope(tc.lease.Instance))
		if err := os.MkdirAll(scope, 0o755); err != nil {
			t.Fatal(err)
		}
		files := map[string]string{
			"memory.current":      "41943040\n",
			"memory.stat":         "anon 8388608\nfile 20971520\nkernel 1048576\n",
			"memory.swap.current": "1024\n",
			"memory.swap.max":     tc.swapMax + "\n",
			"memory.reclaim":      "",
		}
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(scope, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(meminfo, []byte("SwapTotal: 1048576 kB\nSwapFree: "+tc.swapFree+" kB\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		r, err := (&JailerVMM{}).ReclaimVMMemory(tc.lease)
		if err != nil {
			t.Fatalf("%s: %v", tc.lease.Instance, err)
		}
		if r != (VMMemoryReclaim{BeforeBytes: 40 << 20, AfterBytes: 40 << 20, SwappedBytes: 1024}) {
			t.Fatalf("%s: reclaim = %+v", tc.lease.Instance, r)
		}
		got, err := os.ReadFile(filepath.Join(scope, "memory.reclaim"))
		if err != nil || string(got) != strconv.FormatInt(tc.lastChunk, 10) {
			t.Fatalf("%s: last memory.reclaim request = %q, %v; want %d", tc.lease.Instance, got, err, tc.lastChunk)
		}
		if b, err := (&JailerVMM{}).VMResidentBytes(tc.lease); err != nil || b != 40<<20 {
			t.Fatalf("%s: VMResidentBytes = %d, %v", tc.lease.Instance, b, err)
		}
	}
}
