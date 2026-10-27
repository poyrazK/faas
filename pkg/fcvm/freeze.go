package fcvm

// Frozen instances (ADR-906, ADR-1151). Freeze pauses a running VM in place
// and, when asked, has the kernel take its memory back. The VM keeps its
// Firecracker process, lease, network namespace and guest page tables. Thaw
// is a vCPU resume: no snapshot restore, no network setup and no resume hook
// unless the image's healthcheck contract requires one. The guest was never
// cloned, so its RNG state stays unique, and kvm-clock keeps its clock on
// host time across the pause.
//
// schedd owns the FROZEN state, its TTL and thaw admission. vmmd records the
// pause on the live Instance (Frozen, FrozenSince) so it can refuse what a
// paused guest cannot serve: quiet pause and quiet resume skip it, snapshots
// and migration refuse it (ErrInstanceFrozen), a stop kills it without a
// grace period, and forwards fail fast (AwaitFrozen).
//
// Ordering with forwards. A forward marks its activity first and then calls
// AwaitFrozen. Freeze publishes its gate (Instance.freezing) before it reads
// the activity tracker. Either the freeze sees the forward and refuses, or
// the forward sees the gate, waits for the freeze to settle and fails fast
// if the guest ended up frozen.
//
// What the kernel can reclaim depends on the host. Clean pages mapped from a
// snapshot memory file are dropped and re-read on access. Pages the guest has
// written are anonymous; they leave RAM only when the host has swap or zswap,
// otherwise they stay resident. ADR-1151 reclaims only under memory pressure.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/functionlifetime"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	// ErrNotFreezable refuses a freeze of an instance whose traffic or
	// lifecycle does not allow one. The wrapping error names the reason.
	ErrNotFreezable = errors.New("instance cannot be frozen")
	// ErrInstanceBusy refuses a freeze of an instance with a forwarded
	// request, stream or session in flight. The guest keeps running.
	ErrInstanceBusy = errors.New("instance is serving requests")
	// ErrInstanceFrozen refuses an operation that needs a running guest
	// (snapshot, warm capture, migration) on a frozen instance. schedd
	// destroys a frozen VM instead of snapshotting it.
	ErrInstanceFrozen = errors.New("instance is frozen")
)

// FreezeOptions tunes one Freeze.
type FreezeOptions struct {
	// Reclaim asks the kernel to reclaim the VM cgroup's memory after the
	// pause. ADR-1151 reclaims only under memory pressure; the default
	// freeze is a pause.
	Reclaim bool
}

// FreezeReport describes one Freeze. Byte counts are the VM cgroup's charge
// before and after reclaim (equal when no reclaim ran, zero when the cgroup
// could not be read), and the part of it moved to swap (zero on a host
// without swap or zswap). A freeze of an already-frozen instance reports
// zeros.
type FreezeReport struct {
	Pause        time.Duration
	Reclaim      time.Duration
	BeforeBytes  int64
	AfterBytes   int64
	SwappedBytes int64
}

// VMMemoryReclaim is one memory.reclaim pass over a VM's cgroup.
type VMMemoryReclaim struct {
	BeforeBytes  int64
	AfterBytes   int64
	SwappedBytes int64
}

// vmFreezer is the optional VMM capability Freeze needs; Thaw uses the
// VMM's ResumeVM.
type vmFreezer interface {
	PauseVM(ctx context.Context, l Lease) error
	ReclaimVMMemory(l Lease) (VMMemoryReclaim, error)
}

// vmResidentReader reads a VM cgroup's memory.current. JailerVMM provides
// it; without it frozen footprints read as zero.
type vmResidentReader interface {
	VMResidentBytes(l Lease) (int64, error)
}

// FreezeConfig wires the frozen tier into the Manager.
type FreezeConfig struct {
	// Activity is vmmd's forward activity tracker. With it, Freeze refuses
	// an instance with a forward in flight (ErrInstanceBusy).
	Activity QuietActivity
	Metrics  *FreezeMetrics
}

// WithFreeze wires the activity tracker and metrics Freeze and Thaw use.
// Freeze works without it but cannot see forwards in flight.
func (m *Manager) WithFreeze(cfg FreezeConfig) *Manager {
	m.freezeActivity = cfg.Activity
	m.freezeMetrics = cfg.Metrics
	return m
}

// Freeze pauses a running instance in place for the frozen tier and, with
// opts.Reclaim, returns its reclaimable memory to the host. Probes stop
// first because a paused guest cannot answer them; Thaw restarts them. The
// instance stays live and keeps its lease. Freezing a frozen instance
// succeeds without pausing it again; with opts.Reclaim it compresses the
// frozen guest's memory into memory-only swap, or fails with
// ErrCompressUnavailable on a host without it. A failure after the pause
// resumes the guest and restarts its probes.
func (m *Manager) Freeze(ctx context.Context, instance string, opts FreezeOptions) (FreezeReport, error) {
	started := time.Now()
	f, ok := m.vmm.(vmFreezer)
	if !ok {
		return FreezeReport{}, fmt.Errorf("freeze %s: VMM cannot freeze", instance)
	}
	operationCtx, inst, flight, err := m.registerLiveInstanceFlight(ctx, instance)
	if err != nil {
		return FreezeReport{}, fmt.Errorf("freeze %s: %w", instance, err)
	}
	defer m.finishInstanceFlight(instance, flight)
	defer m.beginAdoptionOp(instance, adoptionOpFreeze)()
	gate, frozen, err := m.announceFreeze(inst)
	if err != nil {
		return FreezeReport{}, fmt.Errorf("freeze %s: %w", instance, err)
	}
	if frozen {
		if opts.Reclaim {
			return m.compressFrozen(f, inst)
		}
		return FreezeReport{}, nil
	}
	defer m.settleFreeze(inst, gate)
	if m.forwardsInFlight(instance) {
		return FreezeReport{}, fmt.Errorf("freeze %s: %w", instance, ErrInstanceBusy)
	}
	// The registered flight keeps the quiet pause pass away; take the guest
	// out of a quiet pause so the freeze owns the only pause.
	if err := m.QuietResume(operationCtx, instance); err != nil {
		return FreezeReport{}, fmt.Errorf("freeze %s: %w", instance, err)
	}
	m.DeleteLivenessConsecutiveFailures(instance)
	m.cancelLivenessLoop(instance)
	m.cancelReadinessLoop(instance)
	m.cancelFrameworkReadyLoop(instance)

	rep, pausedAt, err := m.pauseForFreeze(operationCtx, f, inst, opts)
	if err != nil {
		return rep, errors.Join(fmt.Errorf("freeze %s: %w", instance, err), m.unfreeze(ctx, inst))
	}
	m.mu.Lock()
	inst.Frozen, inst.FrozenSince = true, pausedAt
	m.mu.Unlock()
	m.markAdoptionDirty(instance)
	m.freezeMetrics.observeFreeze(time.Since(started))
	m.log.Info("frozen", "instance", instance, "reclaim", opts.Reclaim, "pause_ms", rep.Pause.Milliseconds(),
		"reclaim_ms", rep.Reclaim.Milliseconds(), "before_bytes", rep.BeforeBytes,
		"after_bytes", rep.AfterBytes, "swapped_bytes", rep.SwappedBytes)
	return rep, nil
}

// freezeRefusal names why an instance cannot be frozen, or returns "". Only
// request-driven app VMs whose traffic all passes through vmmd's forward
// path qualify: private-network peers talk guest to guest, a pending
// waitUntil task needs the guest running, and warm-pool, execution, job,
// app-task, qualification and builder VMs have lifecycles of their own. The
// caller holds m.mu.
func freezeRefusal(inst *Instance) string {
	switch {
	case inst.Paused:
		return "warm-pool instance"
	case inst.ExecutionOnly:
		return "execution instance"
	case inst.IsJob:
		return "job instance"
	case inst.AppTaskOnly:
		return "app task instance"
	case inst.QualificationOnly:
		return "qualification instance"
	case inst.Lease.IsBuilder:
		return "builder VM"
	case inst.Net.PrivateNetworkBridge != "":
		return "private network member"
	case inst.TailCount > 0:
		return "waitUntil tasks pending"
	}
	return ""
}

// announceFreeze checks eligibility and publishes the gate forwards wait on.
// frozen reports an instance that is already frozen.
func (m *Manager) announceFreeze(inst *Instance) (gate chan struct{}, frozen bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst.Frozen {
		return nil, true, nil
	}
	if functionExecutionBusy(inst) {
		return nil, false, ErrInstanceBusy
	}
	if reason := freezeRefusal(inst); reason != "" {
		return nil, false, fmt.Errorf("%w: %s", ErrNotFreezable, reason)
	}
	gate = make(chan struct{})
	inst.freezing = gate
	return gate, false, nil
}

// settleFreeze withdraws the freeze gate and releases the forwards waiting
// on it.
func (m *Manager) settleFreeze(inst *Instance, gate chan struct{}) {
	m.mu.Lock()
	if inst.freezing == gate {
		inst.freezing = nil
	}
	m.mu.Unlock()
	close(gate)
}

// forwardsInFlight reports a forward, stream or session in flight. The
// freeze gate is already published, so a forward that begins after this
// check waits for the freeze in AwaitFrozen.
func (m *Manager) forwardsInFlight(instance string) bool {
	if m.freezeActivity == nil {
		return false
	}
	inflight, _, _ := m.freezeActivity.Quiet(instance)
	return inflight > 0
}

// pauseForFreeze pauses the guest and, when asked, reclaims its memory.
// pausedAt is when the pause took effect.
func (m *Manager) pauseForFreeze(ctx context.Context, f vmFreezer, inst *Instance, opts FreezeOptions) (rep FreezeReport, pausedAt time.Time, err error) {
	started := time.Now()
	if err := f.PauseVM(ctx, inst.Lease); err != nil {
		return rep, time.Time{}, err
	}
	pausedAt = time.Now()
	rep.Pause = pausedAt.Sub(started)
	// The paused guest's memory may now go to memory-only swap, under
	// pressure or the reclaim below.
	m.setFrozenSwap(inst.Lease, true)
	if !opts.Reclaim {
		rep.BeforeBytes = m.vmResidentBytes(inst.Lease)
		rep.AfterBytes = rep.BeforeBytes
		return rep, pausedAt, nil
	}
	reclaim, err := f.ReclaimVMMemory(inst.Lease)
	rep.Reclaim = time.Since(pausedAt)
	if err != nil {
		// The guest is intact; the caller resumes it rather than leave it
		// unreachable because the host could not reclaim its memory.
		return rep, pausedAt, err
	}
	rep.BeforeBytes, rep.AfterBytes, rep.SwappedBytes = reclaim.BeforeBytes, reclaim.AfterBytes, reclaim.SwappedBytes
	return rep, pausedAt, nil
}

// vmResidentBytes is the VM cgroup's memory.current, or zero when it cannot
// be read.
func (m *Manager) vmResidentBytes(l Lease) int64 {
	reader, ok := m.vmm.(vmResidentReader)
	if !ok {
		return 0
	}
	b, err := reader.VMResidentBytes(l)
	if err != nil {
		m.log.Debug("freeze: read VM memory.current", "instance", l.Instance, "err", err)
		return 0
	}
	return b
}

// unfreeze undoes a failed Freeze: it resumes the guest and restarts its
// probes. A failed pause call may still have paused the VM, and ResumeVM is
// idempotent on a running one, so the resume always runs.
func (m *Manager) unfreeze(ctx context.Context, inst *Instance) error {
	resumeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	m.setFrozenSwap(inst.Lease, false)
	if err := m.ensureRehearsalNetwork(resumeCtx, inst); err != nil {
		return err
	}
	if err := m.vmm.ResumeVM(resumeCtx, inst.Lease); err != nil {
		return fmt.Errorf("resume after failed freeze: %w", err)
	}
	instance := inst.Lease.Instance
	m.startLivenessLoop(context.WithoutCancel(ctx), instance, inst.Lease.Slot, inst.LivenessProbe)
	m.startReadinessLoop(context.WithoutCancel(ctx), instance, inst.Lease.Slot, inst.ReadinessProbe)
	m.startFrameworkReadyLoop(context.WithoutCancel(ctx), instance)
	return nil
}

// ThawOptions selects the optional work a thaw runs before it returns.
type ThawOptions struct {
	// RunResumeHook runs the guest's resume hook, and with it the app's
	// after_restore callback, as a snapshot restore does. A thaw is not a
	// restore, but an app that declared after_restore reconnects its clients
	// after the time it spent frozen. A failed callback fails the thaw.
	RunResumeHook bool
}

// Thaw resumes a frozen instance in place. Qualified automatic resources
// always run their restore hook before the VM becomes available again.
func (m *Manager) Thaw(ctx context.Context, instance string) error {
	return m.ThawWith(ctx, instance, ThawOptions{})
}

// ThawWith resumes a frozen instance in place and restarts its probes. When
// the instance's image requires a healthcheck, the resume hook and
// healthcheck run before ThawWith returns, as in a warm-pool resume; opts can
// ask for the resume hook alone. Memory the host reclaimed faults back in as
// the guest touches it. A live instance that is not frozen is left alone; one
// that is not live fails as "not live".
func (m *Manager) ThawWith(ctx context.Context, instance string, opts ThawOptions) error {
	started := time.Now()
	operationCtx, inst, flight, err := m.registerLiveInstanceFlight(ctx, instance)
	if err != nil {
		return fmt.Errorf("thaw %s: %w", instance, err)
	}
	defer m.finishInstanceFlight(instance, flight)
	if !m.isFrozen(inst) {
		return nil
	}
	if err := m.checkLiveAdmission(operationCtx, inst); err != nil {
		return err
	}
	defer m.beginAdoptionOp(instance, adoptionOpThaw)()
	// The guest runs again: its memory stays resident from here on.
	if m.functionActivity != nil && functionlifetime.NativeNode(inst.Runtime) {
		opts.RunResumeHook = true
	}
	m.setFrozenSwap(inst.Lease, false)
	if err := m.resumeAndProbe(operationCtx, "thaw", inst, opts.RunResumeHook); err != nil {
		return err
	}
	m.freezeMetrics.observeThaw(time.Since(started))
	m.log.Info("thawed", "instance", instance, "thaw_ms", time.Since(started).Milliseconds())
	return nil
}

func (m *Manager) isFrozen(inst *Instance) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return inst.Frozen
}

func (m *Manager) clearFrozen(inst *Instance) {
	m.mu.Lock()
	inst.Frozen, inst.FrozenSince = false, time.Time{}
	m.mu.Unlock()
	m.markAdoptionDirty(inst.Lease.Instance)
}

// FrozenState reports whether an instance is frozen and since when, for
// InstanceStats.
func (m *Manager) FrozenState(instance string) (frozen bool, since time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := m.live[instance]
	if inst == nil || !inst.Frozen {
		return false, time.Time{}
	}
	return true, inst.FrozenSince
}

// AwaitFrozen reports whether a forward to instance must fail because the
// instance is frozen. The caller has already marked the forward's activity.
// A freeze that is still deciding either sees that activity and refuses, or
// pauses the guest; AwaitFrozen waits until it has settled. A terminal park
// in progress is waited out the same way. An instance that is not live is not
// frozen: the forward finds that out on its own.
func (m *Manager) AwaitFrozen(ctx context.Context, instance string) (bool, error) {
	for {
		var frozen bool
		var gate chan struct{}
		var held *Instance
		m.mu.Lock()
		if inst := m.live[instance]; inst != nil {
			if functionMigrationActive(inst) {
				m.mu.Unlock()
				return false, ErrInstanceBusy
			}
			frozen, gate = inst.Frozen, inst.freezing
			if gate == nil && inst.parking != nil {
				// A terminal park is running (park_hold.go). Wait it out: the
				// VM is then gone and the forward fails as undelivered.
				gate, held = inst.parking, inst
				inst.parkHeld++
			}
		}
		m.mu.Unlock()
		if gate == nil {
			return frozen, nil
		}
		var err error
		select {
		case <-gate:
		case <-ctx.Done():
			err = ctx.Err()
		}
		if held != nil {
			m.mu.Lock()
			held.parkHeld--
			m.mu.Unlock()
		}
		if err != nil {
			return false, err
		}
	}
}

// FrozenCount is the number of frozen instances on this node.
func (m *Manager) FrozenCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, inst := range m.live {
		if inst.Frozen {
			n++
		}
	}
	return n
}

// FrozenResidentBytes sums memory.current over the frozen instances' VM
// cgroups: the host memory the frozen tier holds on this node.
func (m *Manager) FrozenResidentBytes() int64 {
	reader, ok := m.vmm.(vmResidentReader)
	if !ok {
		return 0
	}
	m.mu.Lock()
	var leases []Lease
	for _, inst := range m.live {
		if inst.Frozen {
			leases = append(leases, inst.Lease)
		}
	}
	m.mu.Unlock()
	var total int64
	for _, l := range leases {
		if b, err := reader.VMResidentBytes(l); err == nil {
			total += b
		}
	}
	return total
}

// FrozenFootprint is what the frozen gauges read at scrape time; *Manager
// implements it.
type FrozenFootprint interface {
	FrozenCount() int
	FrozenResidentBytes() int64
}

// FreezeMetrics owns the ADR-1151 frozen-tier series.
type FreezeMetrics struct {
	freeze prometheus.Histogram
	thaw   prometheus.Histogram
}

// NewFreezeMetrics registers vmmd_freeze_seconds, vmmd_thaw_seconds,
// node_frozen_instances and node_frozen_resident_bytes on reg. The two
// gauges read src when scraped.
func NewFreezeMetrics(reg prometheus.Registerer, src FrozenFootprint) *FreezeMetrics {
	m := &FreezeMetrics{
		freeze: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "vmmd_freeze_seconds",
			Help:    "Time to freeze an instance in place, including memory reclaim when requested (ADR-1151).",
			Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		}),
		thaw: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "vmmd_thaw_seconds",
			Help:    "Time to thaw a frozen instance in place, including a required image healthcheck (ADR-1151).",
			Buckets: []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}),
	}
	frozen := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "node_frozen_instances",
		Help: "Instances held frozen in place on this node (ADR-1151).",
	}, func() float64 { return float64(src.FrozenCount()) })
	resident := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "node_frozen_resident_bytes",
		Help: "Sum of memory.current over the VM cgroups of frozen instances on this node (ADR-1151).",
	}, func() float64 { return float64(src.FrozenResidentBytes()) })
	reg.MustRegister(m.freeze, m.thaw, frozen, resident)
	return m
}

func (f *FreezeMetrics) observeFreeze(d time.Duration) {
	if f != nil {
		f.freeze.Observe(d.Seconds())
	}
}

func (f *FreezeMetrics) observeThaw(d time.Duration) {
	if f != nil {
		f.thaw.Observe(d.Seconds())
	}
}

// PauseVM pauses the guest's vCPUs through the Firecracker API.
func (v *JailerVMM) PauseVM(ctx context.Context, l Lease) error {
	if l.Instance == "" {
		return fmt.Errorf("vmm: PauseVM: empty instance")
	}
	if err := v.apiPatch(ctx, l.Instance, "/vm", map[string]any{"state": "Paused"}); err != nil {
		return fmt.Errorf("vmm: pause: %w", err)
	}
	return nil
}

// A memory.reclaim write can stay in the kernel for minutes: on the n2 node
// the kernel kept retrying pages it could not free, with and without swap.
// ReclaimVMMemory therefore asks only for reclaimable pages, in chunks, stops
// at the first EAGAIN, and interrupts a write still running at reclaimBudget.
const (
	reclaimChunkBytes = 16 << 20
	reclaimBudget     = 2 * time.Second
)

// procMeminfoPath is replaced in tests.
var procMeminfoPath = "/proc/meminfo"

// vmCgroupScope is the VM's per-instance cgroup directory.
func vmCgroupScope(l Lease) string {
	parent := ParentCgroupFor(l.Plan)
	if l.IsBuilder {
		parent = BuilderCgroupParent
	}
	return filepath.Join(cgroupRoot, parent, PerInstanceScope(l.Instance))
}

// VMResidentBytes reads the VM cgroup's memory.current.
func (v *JailerVMM) VMResidentBytes(l Lease) (int64, error) {
	return readCgroupInt(vmCgroupScope(l), "memory.current")
}

// ReclaimVMMemory asks the kernel to reclaim the VM cgroup's file pages, and
// its anonymous pages too when the cgroup can swap.
func (v *JailerVMM) ReclaimVMMemory(l Lease) (VMMemoryReclaim, error) {
	scope := vmCgroupScope(l)
	var r VMMemoryReclaim
	var err error
	if r.BeforeBytes, err = readCgroupInt(scope, "memory.current"); err != nil {
		return r, err
	}
	stat, err := readCgroupStat(scope)
	if err != nil {
		return r, err
	}
	// tmpfs pages count as file pages but, like anonymous ones, can only
	// go to swap.
	target := stat["file"] - stat["shmem"]
	if canSwap(scope) {
		target += stat["anon"] + stat["shmem"]
	}
	deadline := time.Now().Add(reclaimBudget)
	for requested := int64(0); requested < target; {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		n := min(int64(reclaimChunkBytes), target-requested)
		err := writeMemoryReclaim(filepath.Join(scope, "memory.reclaim"), n, remaining)
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
			// Nothing more to reclaim, or the budget ran out mid-write.
			break
		}
		if err != nil {
			return r, fmt.Errorf("vmm: reclaim %s: %w", l.Instance, err)
		}
		requested += n
	}
	if r.AfterBytes, err = readCgroupInt(scope, "memory.current"); err != nil {
		return r, err
	}
	// memory.swap.current is absent without swap accounting.
	r.SwappedBytes, _ = readCgroupInt(scope, "memory.swap.current")
	return r, nil
}

// canSwap reports whether the kernel may move this cgroup's anonymous pages
// to swap: the host has free swap and memory.swap.max is not 0.
func canSwap(scope string) bool {
	if limit, err := os.ReadFile(filepath.Join(scope, "memory.swap.max")); err != nil || strings.TrimSpace(string(limit)) == "0" {
		return false
	}
	meminfo, err := os.ReadFile(procMeminfoPath)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(meminfo), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "SwapFree:" {
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			return err == nil && kb > 0
		}
	}
	return false
}

func readCgroupStat(scope string) (map[string]int64, error) {
	raw, err := os.ReadFile(filepath.Join(scope, "memory.stat"))
	if err != nil {
		return nil, fmt.Errorf("vmm: read memory.stat: %w", err)
	}
	stat := map[string]int64{}
	for _, line := range strings.Split(string(raw), "\n") {
		if key, value, ok := strings.Cut(line, " "); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
				stat[key] = n
			}
		}
	}
	return stat, nil
}

func readCgroupInt(scope, name string) (int64, error) {
	raw, err := os.ReadFile(filepath.Join(scope, name))
	if err != nil {
		return 0, fmt.Errorf("vmm: read %s: %w", name, err)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("vmm: parse %s: %w", name, err)
	}
	return n, nil
}
