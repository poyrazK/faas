//go:build metal

// adr: 20261027090012 — keyless agent memory experiment
// (scripts/experiments/agent-runs/MEMORY_EXPERIMENT.md). Agent VMs run a
// scripted harness session against the layer's response-holding fake
// provider and are sampled once a second. The test records numbers for the
// memory-tiering ADR; it asserts only that each run followed its script.
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

type agentCall struct {
	Call         int      `json:"call"`
	Turn         int      `json:"turn"`
	Arrived      float64  `json:"arrived"`
	HoldStart    *float64 `json:"hold_start"`
	Released     *float64 `json:"released"`
	Sent         *float64 `json:"sent"`
	Manual       bool     `json:"manual"`
	PrevToolOK   *bool    `json:"prev_tool_ok"`
	RequestBytes int      `json:"request_bytes"`
}

type agentStatus struct {
	Phase         string           `json:"phase"`
	ExitCode      *int             `json:"exit_code"`
	Calls         int              `json:"calls"`
	Held          int              `json:"held"`
	DoneMark      bool             `json:"done_mark"`
	Timeline      []agentCall      `json:"timeline"`
	SideRequests  int              `json:"side_requests"`
	TreeRSSKB     int64            `json:"tree_rss_kb"`
	PeakTreeRSSKB int64            `json:"peak_tree_rss_kb"`
	TreeCPUMs     float64          `json:"tree_cpu_ms"`
	ProbeCPUMs    float64          `json:"probe_cpu_ms"`
	Guest         map[string]int64 `json:"guest_meminfo_kb"`
	StdoutTail    string           `json:"stdout_tail"`
	StderrTail    string           `json:"stderr_tail"`
	WarmS         float64          `json:"warm_s"`
	Started       float64          `json:"started"`
	Ended         *float64         `json:"ended"`
}

type agentVM struct {
	id, url, scope string
	labels         map[string]any
	frozen         atomic.Bool
}

type agentExp struct {
	t                    *testing.T
	ctx                  context.Context
	m                    *Manager
	base, layer, fcVer   string
	memMiB               int
	hold                 time.Duration
	out                  *json.Encoder
	mu                   sync.Mutex
	label                string
	snapRoot             string
	interval             time.Duration
	runTimeout, probeOut time.Duration
}

func agentEnv(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func TestMetalAgentMemory(t *testing.T) {
	kernel, base, layer := metalImages(t)
	if os.Getenv("FAAS_TEST_AGENT_MEMORY") != "1" {
		t.Skip("set FAAS_TEST_AGENT_MEMORY=1 with the agent base and layer (scripts/experiments/agent-runs/memory)")
	}
	outPath := os.Getenv("FAAS_TEST_AGENT_MEMORY_OUT")
	if outPath == "" {
		t.Fatal("set FAAS_TEST_AGENT_MEMORY_OUT to a JSON-lines output path")
	}
	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx := context.Background()
	fcVersion, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Fatalf("firecracker version: %v", err)
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	snapRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(snapRoot, "snap"), 0o2770); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocalStorageBackend(snapRoot)
	if err != nil {
		t.Fatal(err)
	}
	hold, err := time.ParseDuration(agentEnv("FAAS_TEST_AGENT_WAIT_HOLD", "60s"))
	if err != nil {
		t.Fatal(err)
	}
	memMiB, _ := strconv.Atoi(agentEnv("FAAS_TEST_AGENT_MEM_MIB", "2048"))
	e := &agentExp{
		t: t, ctx: ctx, base: base, layer: layer, fcVer: fcVersion, memMiB: memMiB, hold: hold,
		m:     NewManager(wire.ExecRunner{}, newMetalVMM(t, 180*time.Second).WithStorage(store), Paths{Kernel: kernel}, fcVersion, nil, nil),
		out:   json.NewEncoder(f),
		label: agentEnv("FAAS_TEST_AGENT_LABEL", "default"), snapRoot: snapRoot,
		interval: time.Second, runTimeout: 10 * time.Minute, probeOut: 2 * time.Second,
	}
	e.record(map[string]any{"kind": "config", "label": e.label, "mem_mib": memMiB, "hold_s": hold.Seconds(),
		"fpr": os.Getenv("FAAS_TEST_FREE_PAGE_REPORTING") == "1", "swap_ready": frozenSwapReady(ParentCgroupFor("scale")),
		"zswap": hostZswapParams(), "host": hostMeminfo()})
	cells := map[string]bool{}
	for _, c := range strings.Split(agentEnv("FAAS_TEST_AGENT_CELLS", "A,B,D,E"), ",") {
		cells[strings.TrimSpace(c)] = true
	}
	repeats, _ := strconv.Atoi(agentEnv("FAAS_TEST_AGENT_REPEATS", "3"))
	k, _ := strconv.Atoi(agentEnv("FAAS_TEST_AGENT_K", "4"))
	for _, preset := range strings.Split(agentEnv("FAAS_TEST_AGENT_PRESETS", "codex,claude-code,opencode"), ",") {
		preset := strings.TrimSpace(preset)
		t.Run(preset, func(t *testing.T) {
			snap := e.warmSnapshot(t, preset)
			if cells["A"] {
				e.cellA(t, preset, snap, repeats)
			}
			if cells["B"] {
				for _, mode := range strings.Split(agentEnv("FAAS_TEST_AGENT_B_MODES", "none,pause,reclaim"), ",") {
					e.cellB(t, preset, snap, mode)
				}
			}
			if cells["D"] {
				e.runOne(t, e.wake(t, "d-"+preset, preset, snap, map[string]any{"cell": "D", "start": "warm"}),
					map[string]any{"preset": preset, "script": "s2", "hold_s": 1})
			}
			if cells["E"] {
				e.cellE(t, preset, snap, k)
			}
		})
	}
	if m := e.m.LeasedCount(); m != 0 {
		t.Errorf("leaked leases: %d", m)
	}
}

func (e *agentExp) record(rec map[string]any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rec["t"] = float64(time.Now().UnixNano()) / 1e9
	if err := e.out.Encode(rec); err != nil {
		e.t.Errorf("record: %v", err)
	}
}

// warmSnapshot boots one VM, runs the warm script once so the guest page
// cache holds the harness's files, and parks it as the preset snapshot.
func (e *agentExp) warmSnapshot(t *testing.T, preset string) *Snapshot {
	snap := &Snapshot{FCVersion: e.fcVer, StorageKey: "snap/" + preset + "/mem",
		VMStateStorageKey: "snap/" + preset + "/vmstate", VMStatePath: t.TempDir() + "/vmstate"}
	vm := e.wake(t, "w-"+preset, preset, nil, map[string]any{"cell": "warm"})
	var st agentStatus
	if err := e.post(vm, "/warm", map[string]any{"preset": preset, "timeout_s": 300}, &st, 6*time.Minute); err != nil {
		t.Fatalf("warm %s: %v", preset, err)
	}
	if st.ExitCode == nil || *st.ExitCode != 0 || !st.DoneMark {
		t.Fatalf("warm %s did not finish its script: exit=%v stderr=%s", preset, st.ExitCode, st.StderrTail)
	}
	time.Sleep(2 * time.Second)
	before := e.sample(vm)
	info, err := e.m.Park(e.ctx, vm.id, SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey,
		VMStateStorageKey: snap.VMStateStorageKey})
	if err != nil {
		t.Fatalf("park %s: %v", preset, err)
	}
	e.record(map[string]any{"kind": "snapshot", "preset": preset, "warm_s": st.WarmS, "mem_bytes": info.MemBytes,
		"stored_bytes": info.StoredBytes, "warm_peak_tree_rss_kb": st.PeakTreeRSSKB, "before_park": before,
		"label": e.label})
	return snap
}

func (e *agentExp) wake(t *testing.T, id, preset string, snap *Snapshot, labels map[string]any) *agentVM {
	t.Helper()
	inst, err := e.m.Wake(e.ctx, WakeRequest{Instance: id, AccountID: "acct-agent-mem", AppID: "app-agent-" + preset,
		Plan: "scale", BaseKey: e.base, LayerKey: e.layer, VcpuCount: 2, MemSizeMiB: e.memMiB, Snapshot: snap})
	if err != nil {
		t.Fatalf("wake %s: %v", id, err)
	}
	t.Cleanup(func() { _ = e.m.Destroy(context.Background(), id) })
	if snap != nil && inst.Method != WakeRestore {
		t.Fatalf("wake %s came back by %s, want restore", id, inst.Method)
	}
	vm := &agentVM{id: id, url: "http://" + inst.Lease.HostIP.String() + ":8080", scope: vmCgroupScope(inst.Lease),
		labels: map[string]any{"preset": preset, "label": e.label}}
	for k, v := range labels {
		vm.labels[k] = v
	}
	if _, err := firstResponse(vm.url+"/", 120*time.Second); err != nil {
		t.Fatalf("%s does not serve: %v", id, err)
	}
	e.record(map[string]any{"kind": "wake", "vm": id, "preset": preset, "method": fmt.Sprint(inst.Method),
		"restore_ms": inst.RestoreMs, "label": e.label})
	return vm
}

func (e *agentExp) destroy(vm *agentVM) {
	if err := e.m.Destroy(context.Background(), vm.id); err != nil {
		e.t.Errorf("destroy %s: %v", vm.id, err)
	}
}

func (e *agentExp) status(vm *agentVM, timeout time.Duration) (agentStatus, error) {
	var st agentStatus
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(vm.url + "/status")
	if err != nil {
		return st, err
	}
	defer resp.Body.Close()
	return st, json.NewDecoder(resp.Body).Decode(&st)
}

func (e *agentExp) post(vm *agentVM, path string, body any, out any, timeout time.Duration) error {
	raw, _ := json.Marshal(body)
	client := &http.Client{Timeout: timeout}
	resp, err := client.Post(vm.url+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %d %s", path, resp.StatusCode, data)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (e *agentExp) waitFor(t *testing.T, vm *agentVM, what string, cond func(agentStatus) bool, within time.Duration) agentStatus {
	deadline := time.Now().Add(within)
	for {
		st, err := e.status(vm, e.probeOut)
		if err == nil && cond(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %s not reached in %s (last phase %q calls %d held %d, err %v)", vm.id, what, within, st.Phase, st.Calls, st.Held, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// sample reads the VM's cgroup, its Firecracker process's smaps_rollup and,
// while the guest runs, the probe's status.
func (e *agentExp) sample(vm *agentVM) map[string]any {
	rec := map[string]any{"kind": "sample", "vm": vm.id}
	for k, v := range vm.labels {
		rec[k] = v
	}
	if cur, err := readCgroupInt(vm.scope, "memory.current"); err == nil {
		rec["current"] = cur
	}
	if st, err := readCgroupStat(vm.scope); err == nil {
		for _, k := range []string{"anon", "file", "shmem", "file_mapped", "active_file", "inactive_file",
			"active_anon", "inactive_anon", "zswap", "zswapped", "zswpin", "zswpout", "pgmajfault",
			"workingset_refault_anon", "workingset_refault_file"} {
			rec[k] = st[k]
		}
	}
	for _, name := range []string{"memory.zswap.current", "memory.swap.current", "memory.peak"} {
		if n, err := readCgroupInt(vm.scope, name); err == nil {
			rec[strings.TrimPrefix(name, "memory.")] = n
		}
	}
	rec["cpu_usec"] = cgroupCPUUsec(vm.scope)
	rec["smaps_kb"] = scopeSmapsKB(vm.scope)
	if vm.frozen.Load() {
		rec["frozen"] = true
		return rec
	}
	if st, err := e.status(vm, 400*time.Millisecond); err == nil {
		rec["phase"], rec["calls"], rec["held"] = st.Phase, st.Calls, st.Held
		rec["tree_rss_kb"], rec["guest_kb"] = st.TreeRSSKB, st.Guest
		rec["tree_cpu_ms"], rec["probe_cpu_ms"] = st.TreeCPUMs, st.ProbeCPUMs
	}
	return rec
}

// sampleEvery records samples of vm until the returned stop is called.
func (e *agentExp) sampleEvery(vm *agentVM, every time.Duration) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			e.record(e.sample(vm))
			select {
			case <-done:
				return
			case <-tick.C:
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done); wg.Wait() }) }
}

func (e *agentExp) finish(t *testing.T, vm *agentVM, st agentStatus) {
	ok := st.ExitCode != nil && *st.ExitCode == 0 && st.DoneMark
	for _, c := range st.Timeline {
		if c.PrevToolOK != nil && !*c.PrevToolOK {
			ok = false
		}
	}
	rec := map[string]any{"kind": "run", "vm": vm.id, "ok": ok, "status": st}
	for k, v := range vm.labels {
		rec[k] = v
	}
	e.record(rec)
	if !ok {
		t.Errorf("%s did not follow its script: exit=%v done=%v stderr=%s", vm.id, st.ExitCode, st.DoneMark, st.StderrTail)
	}
}

func (e *agentExp) runOne(t *testing.T, vm *agentVM, req map[string]any) {
	stop := e.sampleEvery(vm, e.interval)
	if err := e.post(vm, "/run", req, nil, 30*time.Second); err != nil {
		stop()
		t.Fatalf("run %s: %v", vm.id, err)
	}
	st := e.waitFor(t, vm, "harness exit", func(s agentStatus) bool { return s.Phase == "done" }, e.runTimeout)
	time.Sleep(3 * time.Second)
	stop()
	e.finish(t, vm, st)
	e.destroy(vm)
}

// cellA: the active profile, cold boot against warm restore, plus a baseline
// VM whose probe runs but starts no harness.
func (e *agentExp) cellA(t *testing.T, preset string, snap *Snapshot, repeats int) {
	base := e.wake(t, "a-base-"+preset, preset, snap, map[string]any{"cell": "A", "start": "baseline"})
	stop := e.sampleEvery(base, e.interval)
	time.Sleep(15 * time.Second)
	stop()
	e.destroy(base)
	for r := 0; r < repeats; r++ {
		for _, start := range []string{"cold", "warm"} {
			s := snap
			if start == "cold" {
				s = nil
			}
			vm := e.wake(t, fmt.Sprintf("a-%s-%s-%d", start, preset, r), preset, s,
				map[string]any{"cell": "A", "start": start, "rep": r})
			e.runOne(t, vm, map[string]any{"preset": preset, "script": "s1", "hold_s": 3})
		}
	}
}

// cellB: the harness waits on turn 2 for e.hold while the VM runs (none), is
// paused (pause), or is paused and reclaimed into compressed swap (reclaim).
// After the wait the VM thaws and the response is released at once.
func (e *agentExp) cellB(t *testing.T, preset string, snap *Snapshot, mode string) {
	vm := e.wake(t, "b-"+mode+"-"+preset, preset, snap, map[string]any{"cell": "B", "mode": mode, "start": "warm"})
	stop := e.sampleEvery(vm, e.interval)
	defer stop()
	if err := e.post(vm, "/run", map[string]any{"preset": preset, "script": "s1", "hold_s": 3, "manual": []int{2},
		"max_hold_s": e.hold.Seconds() + 300}, nil, 30*time.Second); err != nil {
		t.Fatalf("run %s: %v", vm.id, err)
	}
	e.waitFor(t, vm, "hold on turn 2", func(s agentStatus) bool { return s.Held > 0 && s.Calls >= 2 }, 5*time.Minute)
	time.Sleep(5 * time.Second)
	e.record(map[string]any{"kind": "event", "event": "wait_start", "vm": vm.id, "mode": mode, "preset": preset, "label": e.label})
	ev := map[string]any{"kind": "event", "event": "wait_end", "vm": vm.id, "mode": mode, "preset": preset, "label": e.label}
	switch mode {
	case "none":
		time.Sleep(e.hold)
	case "pause", "reclaim":
		vm.frozen.Store(true)
		rep, err := e.m.Freeze(e.ctx, vm.id, FreezeOptions{Reclaim: mode == "reclaim"})
		if err != nil {
			t.Fatalf("freeze %s: %v", vm.id, err)
		}
		ev["freeze"] = map[string]any{"pause_ms": rep.Pause.Milliseconds(), "reclaim_ms": rep.Reclaim.Milliseconds(),
			"before": rep.BeforeBytes, "after": rep.AfterBytes, "swapped": rep.SwappedBytes}
		e.record(e.sample(vm))
		time.Sleep(e.hold)
		ev["frozen_sample"] = e.sample(vm)
		started := time.Now()
		if err := e.m.Thaw(e.ctx, vm.id); err != nil {
			t.Fatalf("thaw %s: %v", vm.id, err)
		}
		ev["thaw_ms"] = float64(time.Since(started).Microseconds()) / 1000
		vm.frozen.Store(false)
		if _, err := e.status(vm, 5*time.Second); err != nil {
			t.Fatalf("%s does not answer after thaw: %v", vm.id, err)
		}
		ev["thaw_to_status_ms"] = float64(time.Since(started).Microseconds()) / 1000
	default:
		t.Fatalf("unknown mode %q", mode)
	}
	var rel map[string]float64
	if err := e.post(vm, "/release", map[string]any{}, &rel, 10*time.Second); err != nil {
		t.Fatalf("release %s: %v", vm.id, err)
	}
	ev["released_guest"] = rel["released"]
	// The first seconds after the wait: refaults of what the reclaim moved.
	for i := 0; i < 20; i++ {
		e.record(e.sample(vm))
		time.Sleep(250 * time.Millisecond)
	}
	st := e.waitFor(t, vm, "turn 3", func(s agentStatus) bool { return s.Calls >= 3 }, 5*time.Minute)
	for _, c := range st.Timeline {
		if c.Turn == 3 {
			ev["next_request_s"] = c.Arrived - rel["released"]
			break
		}
	}
	e.record(ev)
	st = e.waitFor(t, vm, "harness exit", func(s agentStatus) bool { return s.Phase == "done" }, e.runTimeout)
	stop()
	e.finish(t, vm, st)
	e.destroy(vm)
}

// cellE: k restores of one snapshot wait together, are compressed, and are
// thawed and released together.
func (e *agentExp) cellE(t *testing.T, preset string, snap *Snapshot, k int) {
	vms := make([]*agentVM, k)
	hostBefore := hostMeminfo()
	for i := range vms {
		vms[i] = e.wake(t, fmt.Sprintf("e-%s-%d", preset, i), preset, snap, map[string]any{"cell": "E", "k": k, "start": "warm"})
		if err := e.post(vms[i], "/run", map[string]any{"preset": preset, "script": "s1", "hold_s": 3, "manual": []int{2},
			"max_hold_s": e.hold.Seconds() + 600}, nil, 30*time.Second); err != nil {
			t.Fatalf("run %s: %v", vms[i].id, err)
		}
		time.Sleep(2 * time.Second)
	}
	for _, vm := range vms {
		e.waitFor(t, vm, "hold on turn 2", func(s agentStatus) bool { return s.Held > 0 && s.Calls >= 2 }, 5*time.Minute)
	}
	time.Sleep(5 * time.Second)
	phase := func(name string) {
		rec := map[string]any{"kind": "group", "phase": name, "preset": preset, "k": k, "label": e.label,
			"host": hostMeminfo(), "host_before": hostBefore}
		var samples []map[string]any
		for _, vm := range vms {
			samples = append(samples, e.sample(vm))
		}
		rec["vms"] = samples
		e.record(rec)
	}
	phase("all_waiting")
	for _, vm := range vms {
		vm.frozen.Store(true)
		rep, err := e.m.Freeze(e.ctx, vm.id, FreezeOptions{Reclaim: true})
		if err != nil {
			t.Fatalf("freeze %s: %v", vm.id, err)
		}
		e.record(map[string]any{"kind": "event", "event": "freeze", "vm": vm.id, "preset": preset, "label": e.label,
			"reclaim_ms": rep.Reclaim.Milliseconds(), "before": rep.BeforeBytes, "after": rep.AfterBytes, "swapped": rep.SwappedBytes})
	}
	time.Sleep(5 * time.Second)
	phase("all_compressed")
	var wg sync.WaitGroup
	for _, vm := range vms {
		wg.Add(1)
		go func(vm *agentVM) {
			defer wg.Done()
			if err := e.m.Thaw(e.ctx, vm.id); err != nil {
				t.Errorf("thaw %s: %v", vm.id, err)
				return
			}
			vm.frozen.Store(false)
			if err := e.post(vm, "/release", map[string]any{}, nil, 10*time.Second); err != nil {
				t.Errorf("release %s: %v", vm.id, err)
			}
		}(vm)
	}
	burstStart := time.Now()
	for time.Since(burstStart) < 15*time.Second {
		e.record(map[string]any{"kind": "burst", "preset": preset, "k": k, "label": e.label, "host": hostMeminfo(),
			"since_ms": time.Since(burstStart).Milliseconds(), "sum_current": sumCurrent(vms)})
		time.Sleep(100 * time.Millisecond)
	}
	wg.Wait()
	phase("after_release")
	for _, vm := range vms {
		st := e.waitFor(t, vm, "harness exit", func(s agentStatus) bool { return s.Phase == "done" }, e.runTimeout)
		e.finish(t, vm, st)
		e.destroy(vm)
	}
}

func sumCurrent(vms []*agentVM) (total int64) {
	for _, vm := range vms {
		if n, err := readCgroupInt(vm.scope, "memory.current"); err == nil {
			total += n
		}
	}
	return total
}

func cgroupCPUUsec(scope string) int64 {
	raw, err := os.ReadFile(filepath.Join(scope, "cpu.stat"))
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "usage_usec "); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n
		}
	}
	return -1
}

// scopeSmapsKB sums smaps_rollup over the scope's processes (the jailed
// Firecracker). Pss divides shared pages among the processes mapping them,
// so Rss - Pss is what this VM shares with others.
func scopeSmapsKB(scope string) map[string]int64 {
	out := map[string]int64{}
	raw, err := os.ReadFile(filepath.Join(scope, "cgroup.procs"))
	if err != nil {
		return out
	}
	for _, pid := range strings.Fields(string(raw)) {
		data, err := os.ReadFile("/proc/" + pid + "/smaps_rollup")
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 || !strings.HasSuffix(f[0], ":") {
				continue
			}
			key := strings.TrimSuffix(f[0], ":")
			switch key {
			case "Rss", "Pss", "Pss_Anon", "Pss_File", "Pss_Shmem", "Shared_Clean", "Shared_Dirty",
				"Private_Clean", "Private_Dirty", "Anonymous", "Swap", "SwapPss":
				n, _ := strconv.ParseInt(f[1], 10, 64)
				out[key] += n
			}
		}
	}
	return out
}

func hostMeminfo() map[string]int64 {
	out := map[string]int64{}
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch key := strings.TrimSuffix(f[0], ":"); key {
		case "MemTotal", "MemFree", "MemAvailable", "Cached", "AnonPages", "Shmem", "SwapTotal", "SwapFree",
			"Zswap", "Zswapped":
			n, _ := strconv.ParseInt(f[1], 10, 64)
			out[key] = n
		}
	}
	return out
}

func hostZswapParams() map[string]string {
	out := map[string]string{}
	for _, name := range []string{"enabled", "compressor", "zpool", "max_pool_percent", "shrinker_enabled"} {
		if raw, err := os.ReadFile("/sys/module/zswap/parameters/" + name); err == nil {
			out[name] = strings.TrimSpace(string(raw))
		}
	}
	return out
}
