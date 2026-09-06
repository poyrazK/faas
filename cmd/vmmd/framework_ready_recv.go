//go:build linux

// Host-side DGRAM recv loop for the framework-ready signal (issue
// #470 / PR #470-FU-B) AND the sidecar events channel
// (issue #463 / ADR-069 / ADR-071 / PR-C). The guest-init proxy
// (see guest/init/framework_ready_proxy_linux.go) dials CID=2
// (VMADDR_CID_HOST) port 1027 with a DGRAM datagram; this loop
// binds the same port on CID=2 and parses each receipt,
// resolving the source instance via the per-DGRAM peer CID
// (each Firecracker guest has a unique CID derived from
// Lease.Slot, see pkg/fcvm.GuestVsockCID).
//
// The wire shape (mirrored from
// guest/init/{framework_ready,sidecar_events}_proxy_linux.go):
//
//	[1B type=0x01][optional 4B BE uint32 warmup_ms][NUL][runtime]
//	[1B type=0x02][json envelope: sidecar_init_exit]
//	[1B type=0x03][json envelope: sidecar_restart]
//	[1B type=0x04][1B outcome][6B reserved][8B elapsed_ms BE uint64]
//	[1B type=0x05][json envelope: workload_oom]                          ← NEW (Cluster C / ADR-121)
//	[1B type=0x06][json envelope: disk telemetry]
//
// The host strips the NUL-terminated runtime and uses the
// preceding 4 bytes (if present) as the warmup_ms duration for
// type=0x01; for type=0x02/0x03 the remainder of the body is a
// small UTF-8 JSON envelope. type=0x04 (issue #667 / ADR-078)
// is the waitUntil(post-response tail) terminal-event channel:
// a 16-byte fixed-size envelope with the per-task outcome
// (completed / failed / timeout) and elapsed_ms in
// milliseconds. type=0x05 (Cluster C / ADR-121) is the
// workload_OOM channel: the guest-init cgroup.events listener
// emits a JSON envelope {peak_mb, plan_mb} when the per-VM
// cgroup v2 leaf detects an oom_kill event. Type outside the
// closed set {0x01, 0x02, 0x03, 0x04, 0x05, 0x06} is dropped with a
// Warn (forward-compatible with future event classes).
//
// Concurrency: one goroutine reads the DGRAM fd. Each receipt
// is parsed and dispatched to the Manager synchronously. A
// misframed datagram is warn-logged and dropped; the loop never
// crashes on a bad peer.
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

// VsockFrameworkReadyHostPort mirrors the guest-side
// VsockFrameworkReadyPort (issue #470 / PR #470-FU-B). The host
// binds VMADDR_CID_HOST=2 on this port; the guest-init proxy
// dials VMADDR_CID_HOST=2 on the same port. Must match on both
// sides. (guest/init/framework_ready_proxy_linux.go defines the
// guest-side constant `VsockFrameworkReadyPort`; cmd/vmmd does
// not import guest/init so the constant is duplicated here.)
const VsockFrameworkReadyHostPort uint32 = 1027

// VsockFrameworkReadyHostTypeReady is the discriminator byte for
// the "ready" message type. Issue #463 / ADR-069 / ADR-071 / PR-C
// adds two more types to the same channel: 0x02 = sidecar_init_exit,
// 0x03 = sidecar_restart. The closed set keeps the wire bounded
// — a future event class picks the next free byte and is its own
// PR.
const (
	VsockFrameworkReadyHostTypeReady    byte = 0x01
	VsockFrameworkReadyHostTypeInitExit byte = 0x02
	VsockFrameworkReadyHostTypeRestart  byte = 0x03
	// VsockFrameworkReadyHostTypeTail (issue #667 / ADR-078)
	// is the discriminator byte for the waitUntil(post-response
	// tail) terminal-event envelope. Same DGRAM port 1027
	// channel as framework_ready / sidecar events; the
	// guest-init proxy (guest/init/sidecar_events_proxy_linux.go)
	// emits a 16-byte fixed-size body following the type byte.
	// The host resolves instance identity from the DGRAM peer
	// CID (same join the other three types use); the
	// elapsed_ms payload feeds the telemetry histogram (PR 5).
	VsockFrameworkReadyHostTypeTail byte = 0x04
	// VsockFrameworkReadyHostTypeWorkloadOOM (Cluster C /
	// ADR-121) is the discriminator byte for the workload-OOM
	// signal emitted by the guest-init cgroup.events listener
	// (guest/init/cgroup_partition_linux.go::WatchOOM). Same
	// DGRAM port 1027 channel as the four closed-set siblings;
	// the body is a small UTF-8 JSON envelope
	// {"peak_mb":N,"plan_mb":N}. The host resolves instance
	// identity from the DGRAM peer CID (same join) and
	// forwards the (peakMB, planMB) tuple to
	// Manager.ReportWorkloadOOM → schedd
	// Engine.DestroyForWorkloadOOMFailure → whycopy
	// CodeAppRuntimeOOM (templated peak + plan into the
	// customer's ErrorWhy / ErrorFix prose).
	VsockFrameworkReadyHostTypeWorkloadOOM byte = 0x05
	// VsockFrameworkReadyHostTypeDisk carries the latest writable-root
	// filesystem sample emitted by guest-init.
	VsockFrameworkReadyHostTypeDisk byte = 0x06
)

// Sidecar init-exit status closed enum (issue #463 / ADR-069 /
// PR-C §3, PR-B AC #1). The constants live in
// cmd/vmmd/sidecar_events_emit.go (platform-neutral, no build
// tag) so the same source compiles on darwin + linux. This
// file is //go:build linux; the dispatch consumes the
// constants from the platform-neutral file.

// frameworkReadyMaxDatagram is the upper bound on the DGRAM
// body the host will accept. The guest-side wire for type=0x01 is
// at most 5 bytes (1B type + 4B BE uint32 warmup_ms + NUL) plus
// the runtime string (≤ 32 bytes — bounded by the guest runner id
// set {node22, node24, python312, python313, go124}); 64 is a
// comfortable future-proof margin. Type 0x02/0x03 carry a JSON
// envelope that the guest-init proxy caps at
// guest/init::sidecarMaxDatagram = 512 bytes; we read up to
// frameworkReadyMaxDatagram for ALL types so the host bound is
// the larger of the two. The Linux vsock DGRAM max is well
// above 4 KiB on a stock kernel; 1024 is a generous future-proof
// margin that still pinpoints a runaway sender (4+ KiB frames
// from guest-init are a bug).
const frameworkReadyMaxDatagram = 1024

// FrameworkReadyReceiver is the host-side DGRAM listener. It
// owns the bound AF_VSOCK DGRAM socket and the read loop. The
// receiver is held by the vmmd main loop and torn down on
// context cancellation.
//
// fd is an atomic.Int32 (not a plain int) because Close() writes
// to it from the main path while loop() reads it on every
// recv call. Using a plain int trips `go test -race` between
// the two goroutines (CRIT-related review feedback on PR
// #470-FU-B). The zero value is meaningless; Close publishes
// the sentinel -1 to break the loop.
//
// emitter (issue #463 / ADR-069 / ADR-071 / PR-C) is the
// audit sink for the sidecar event classes (init_exit /
// restart). Set by WithSidecarEmitter after construction (the
// method lives in sidecar_events_wire.go so cmd/vmmd/main.go
// can call it on every build); nil falls back to a no-op
// emitter so the framework_ready path is unaffected by a
// missing sidecar wiring (e.g. local-dev without a
// state.Store).
type FrameworkReadyReceiver struct {
	ctx     context.Context
	fd      atomic.Int32
	log     *slog.Logger
	mgr     *fcvm.Manager
	emitter SidecarEventEmitter
}

// StartFrameworkReadyReceiver binds the host-side DGRAM
// listener on CID=2:VsockFrameworkReadyHostPort and spawns the
// read loop. Returns an error if the bind fails (which means
// vmmd is running on a host without AF_VSOCK — the host kernel
// doesn't have vsock loaded, or the vmmd binary is missing
// CAP_NET_RAW). The error is fatal at the cmd main() level —
// the framework-ready receipt is required for the warm-tier
// path, so the cmd path aborts if it can't come up.
//
// The Manager is the destination for every receipt. The
// receiver stores a pointer (not a value) so a Manager
// reinstalled by the cmd main loop after a config reload is
// reflected without restarting the listener. sidecarEmitter
// is the audit sink for the sidecar event classes; nil = the
// no-op default (no audit, but the dispatch never blocks).
func StartFrameworkReadyReceiver(ctx context.Context, log *slog.Logger, mgr *fcvm.Manager) (*FrameworkReadyReceiver, error) {
	if log == nil {
		log = slog.Default()
	}
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("framework_ready DGRAM socket: %w", err)
	}
	addr := &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: VsockFrameworkReadyHostPort}
	if err := unix.Bind(fd, addr); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("framework_ready DGRAM bind port %d: %w", VsockFrameworkReadyHostPort, err)
	}
	r := &FrameworkReadyReceiver{ctx: ctx, log: log, mgr: mgr, emitter: noopSidecarEventEmitter{}}
	r.fd.Store(int32(fd))
	go r.loop()
	log.Info("framework_ready receiver started", "vsock_host_port", VsockFrameworkReadyHostPort)
	return r, nil
}

// Close releases the DGRAM socket. Safe to call multiple times.
// Synchronises with the loop() reader via atomic.Int32.Load:
// the loop checks r.fd < 0 on every iteration and exits when
// Close publishes the sentinel.
func (r *FrameworkReadyReceiver) Close() {
	if r == nil {
		return
	}
	old := r.fd.Swap(-1)
	if old < 0 {
		return
	}
	_ = unix.Close(int(old))
}

// loop reads datagrams in a tight loop. Each receipt is parsed
// and dispatched to the Manager (type=0x01) or to the
// sidecar-event emitter (type=0x02/0x03). The loop terminates
// when the fd is closed (Close) or the kernel returns a syscall
// error (typically the VM exiting — the kernel may close the
// vsock proxy on the host side).
func (r *FrameworkReadyReceiver) loop() {
	buf := make([]byte, frameworkReadyMaxDatagram)
	for {
		// Atomic load: a concurrent Close publishes -1
		// here. The check runs on every iteration so the
		// loop exits within one recv of a Close call.
		if r.fd.Load() < 0 {
			return
		}
		n, from, err := unix.Recvfrom(int(r.fd.Load()), buf, 0)
		if err != nil {
			// EBADF is the expected terminal error when Close()
			// publishes the -1 sentinel between the inner Load
			// and the kernel entering the syscall. Log at Debug
			// to keep the Info channel clean on graceful
			// shutdown (MED-6 review feedback on PR #543).
			// Other errors (EINTR, EAGAIN under non-blocking,
			// ENOTCONN if the vsock device unloads) are also
			// terminal for this loop — keep the Debug level so
			// a noisy kernel doesn't alarm the operator.
			r.log.Debug("framework_ready recv loop ended", "err", err, "ebadf", errors.Is(err, unix.EBADF))
			return
		}
		sa, ok := from.(*unix.SockaddrVM)
		if !ok {
			r.log.Warn("framework_ready non-vsock peer", "from", from)
			continue
		}
		msg, perr := parseFrameworkReadyDatagram(buf[:n])
		if perr != nil {
			r.log.Warn("framework_ready parse", "err", perr, "len", n, "peer_cid", sa.CID)
			continue
		}
		// Resolve the peer CID → instance id via the live map.
		// The Manager owns the CID↔instance join (it knows each
		// instance's Lease.Slot which derives the CID via
		// pkg/fcvm.GuestVsockCID). A fresh lookup on every
		// receipt keeps the loop stateless across churn.
		instance, lookupErr := r.mgr.InstanceByCID(sa.CID)
		if lookupErr != nil {
			// Expected during instance churn (a DGRAM racing
			// a wake-park cycle). Log at Debug. Closed for
			// ALL types so a sidecar_init_exit / restart
			// datagram from a guest that just parked isn't
			// a noisy Warn.
			r.log.Debug("framework_ready-scope DGRAM for unknown CID",
				"peer_cid", sa.CID, "type", msg.TypeLabel())
			continue
		}
		switch msg.Kind {
		case parseFWReadyKindOK:
			r.dispatchFrameworkReady(instance, msg.WarmupMs)
		case parseFWReadyKindInitExit:
			r.dispatchSidecarInitExit(instance, msg.InitExit)
		case parseFWReadyKindRestart:
			r.dispatchSidecarRestart(instance, msg.Restart)
		case parseFWReadyKindTail:
			r.dispatchTailEvent(instance, msg.Tail)
		case parseFWReadyKindWorkloadOOM:
			r.dispatchWorkloadOOM(instance, msg.WorkloadOOM)
		case parseFWReadyKindDisk:
			r.dispatchDiskUsage(instance, msg.Disk)
		}
	}
}

// dispatchFrameworkReady (extracted from the loop body, issue
// #463 / ADR-069 / ADR-071 / PR-C): the type=0x01 dispatch path.
// Stamps the per-instance `framework_ready_at` clock and
// observes the warmup histogram. The receiver's stored ctx is
// passed so a cmd shutdown cancels the call before it returns
// to the loop. Runtime label is the runner id from the wire
// (e.g. "node22"). The Manager's histogram already stamped under
// (runtime, appID).
func (r *FrameworkReadyReceiver) dispatchFrameworkReady(instance string, warmupMs int64) {
	stamped, appID, runtime, merr := r.mgr.MarkInstanceFrameworkReady(r.ctx, instance, warmupMs)
	if merr != nil {
		r.log.Warn("framework_ready manager call", "err", merr)
		return
	}
	if !stamped {
		r.log.Debug("framework_ready manager found no live instance",
			"instance", instance)
		return
	}
	_ = appID
	_ = runtime
}

// dispatchSidecarInitExit (issue #463 / ADR-069 / ADR-071 /
// PR-C §3): the type=0x02 dispatch path. Resolves the
// (instance → appID, deploymentID) join via Manager and
// forwards the wire envelope to the sidecar-event emitter.
// A failed lookup (the instance parked between the guest's
// send and our recv) is logged at Debug, not Warn — the
// audit is best-effort.
//
// PR-B AC #1: deploymentID is resolved via the new
// InstanceDeploymentIDAndAppID helper (single lock-held
// read so a Park racing the DGRAM recv returns a consistent
// pair). Empty deploymentID (legacy pre-PR-B wake) is
// tolerated — the emitter skips the deploy-row flip on "",
// but the audit row still lands.
func (r *FrameworkReadyReceiver) dispatchSidecarInitExit(instance string, wire sidecarInitExitWire) {
	if wire.Status != sidecarStatusInitOK && wire.Status != sidecarStatusInitFailed {
		r.log.Warn("sidecar_init_exit unknown status", "instance", instance, "status", wire.Status)
		return
	}
	depID, appID, perr := r.mgr.InstanceDeploymentIDAndAppID(instance)
	if perr != nil {
		r.log.Debug("sidecar_init_exit unknown instance", "instance", instance, "err", perr)
		return
	}
	r.emitter.EmitSidecarInitExit(r.ctx, instance, appID, depID, "" /* wakeID not on wire — see pkg/events.SidecarInitExit's struct doc */, wire)
	if wire.Status == sidecarStatusInitFailed {
		// AC #1 surface: a failed init is a hard fail, and
		// the operator-visible audit must show
		// failure_class: user_error. The audit row is keyed
		// on (deployment_id, sidecar) so the deployments UI
		// can group init-side failures across the fleet.
		r.log.Error("sidecar_init_exit init_failed (AC #1)",
			"instance", instance, "app_id", appID, "deployment_id", depID,
			"sidecar", wire.Sidecar,
			"exit_code", wire.ExitCode, "duration_ms", wire.DurationMs)
	}
}

// dispatchSidecarRestart (issue #463 / ADR-069 / ADR-071 /
// PR-C §4): the type=0x03 dispatch path. Same join as init_exit;
// PR-C §4 increments the vmmd_sidecar_restart_total counter
// in the emitter (the counter lives on wire.OpsMetrics which the
// emitter wraps). Wired here so the §3 commit ships both
// dispatch arms; the §4 commit only needs to add the
// guest-init Supervisor.OnCrash emit hook to actually drive
// type=0x03.
func (r *FrameworkReadyReceiver) dispatchSidecarRestart(instance string, wire sidecarRestartWire) {
	appID, perr := r.mgr.InstanceAppID(instance)
	if perr != nil {
		r.log.Debug("sidecar_restart unknown instance", "instance", instance, "err", perr)
		return
	}
	r.emitter.EmitSidecarRestart(r.ctx, instance, appID, "" /* wakeID not on wire — see pkg/events.SidecarRestart's struct doc */, wire)
}

// dispatchTailEvent (issue #667 / ADR-078): the type=0x04
// dispatch path. Decrements the in-memory TailCount via the
// Manager's MarkInstanceTailTerminal + mirrors the decrement to
// the SQL `tail_count` column (via the Manager's optional
// TailTerminalStamper). The elapsed_ms payload is currently
// read-but-not-acted-on — PR 5 wires the
// guest_tail_seconds histogram to observe it. A closed-set
// outcome guard surfaces unknown bytes at Warn (a runner
// emitting an unknown outcome is a wire-incompatible bug).
func (r *FrameworkReadyReceiver) dispatchTailEvent(instance string, wire parseFWReadyTailWire) {
	// Closed-set guard. An unknown outcome byte means the
	// guest-init proxy shipped a wire-incompatible value —
	// the closed-set constant in
	// guest/init/sidecar_events_proxy_linux.go is the
	// source of truth; if a new outcome is added there,
	// the union here and the byte set in pkg/fcvm must
	// move together.
	var outcome fcvm.TailOutcome
	switch wire.Outcome {
	case tailEventOutcomeCompleted:
		outcome = fcvm.TailOutcomeCompleted
	case tailEventOutcomeFailed:
		outcome = fcvm.TailOutcomeFailed
	case tailEventOutcomeTimeout:
		outcome = fcvm.TailOutcomeTimeout
	default:
		r.log.Warn("tail_event unknown outcome",
			"instance", instance, "outcome", wire.Outcome)
		return
	}
	stamped, appID, merr := r.mgr.MarkInstanceTailTerminal(r.ctx, instance, outcome, wire.ElapsedMs)
	if merr != nil {
		r.log.Warn("tail_event manager call", "err", merr)
		return
	}
	if !stamped {
		r.log.Debug("tail_event manager found no live instance",
			"instance", instance)
		return
	}
	_ = appID
}

// dispatchWorkloadOOM (Cluster C / ADR-121) is the type=0x05
// dispatch path. The guest-init cgroup.events listener
// detected an oom_kill on the per-VM workload cgroup v2 leaf
// and emitted the (peakMB, planMB) tuple over DGRAM. The host
// forwards the tuple to the Manager's optional workload-OOM
// sink (wired in cmd/vmmd/main.go via fcvm.WithWorkloadOOMSink),
// which in turn relays it to the schedd via the
// ReportWorkloadOOM gRPC. The schedd stamps the deployment
// with CodeAppRuntimeOOM and the whycopy Observed prose
// (peak_mb + plan_mb templated into Why / Fix).
//
// Best-effort delivery: the guest-init exits its listener
// after one emit (the workload is dead, the VM is about to be
// torn down); a relay failure is logged at Warn, not Fatal,
// because the customer's deploy was going to fail anyway.
// The Manager's ReportWorkloadOOM is nil-safe: if the sink
// was not wired (e.g. local-dev vmmd without schedd), the
// call is a no-op.
func (r *FrameworkReadyReceiver) dispatchWorkloadOOM(instance string, wire workloadOOMWire) {
	if r.mgr == nil {
		return
	}
	// peak_mb + plan_mb flow verbatim into the schedd; the
	// whycopy Observed closure signs struct{ PeakMB, PlanMB int }
	// (pkg/whycopy/whycopy.go::CodeAppRuntimeOOM). Zero values
	// are tolerated at this boundary — the engine stamps
	// whatever it gets; templating with peak=0 just degrades
	// the customer's ErrorWhy to a generic prose (still stamps
	// the code, doesn't fail the flow).
	r.mgr.ReportWorkloadOOM(r.ctx, instance, wire.PeakMB, wire.PlanMB)
	r.log.Debug("workload_oom dispatched", "instance", instance,
		"peak_mb", wire.PeakMB, "plan_mb", wire.PlanMB)
}

func (r *FrameworkReadyReceiver) dispatchDiskUsage(instance string, wire diskUsageWire) {
	if r.mgr == nil {
		return
	}
	pressure, changed := r.mgr.ReportDiskUsage(instance, wire.UsedBytes, wire.CapacityBytes)
	if !changed {
		return
	}
	if pressure == fcvm.DiskPressureNormal {
		r.log.Info("guest writable filesystem pressure recovered", "instance", instance, "pressure", pressure.String())
		return
	}
	r.log.Warn("guest writable filesystem pressure", "instance", instance,
		"pressure", pressure.String(), "used_bytes", wire.UsedBytes,
		"capacity_bytes", wire.CapacityBytes)
}

// parseFWKind is the discriminator for
// parseFrameworkReadyDatagram (issue #463 / ADR-069 /
// ADR-071 / PR-C, extended for tail events in issue #667 /
// ADR-078, extended for workload OOM in Cluster C /
// ADR-121). Closed set: OK for type=0x01, InitExit for
// type=0x02, Restart for type=0x03, Tail for type=0x04,
// WorkloadOOM for type=0x05, DiskTelemetry for type=0x06. A future type=0x07 adds its
// own enum value here.
type parseFWKind uint8

const (
	parseFWReadyKindUnknown parseFWKind = iota
	parseFWReadyKindOK
	parseFWReadyKindInitExit
	parseFWReadyKindRestart
	// parseFWReadyKindTail (issue #667 / ADR-078) — the
	// waitUntil terminal-event receipt (type=0x04).
	parseFWReadyKindTail
	// parseFWReadyKindWorkloadOOM (Cluster C / ADR-121) — the
	// workload OOM receipt (type=0x05) carrying a small
	// UTF-8 JSON envelope with peak_mb / plan_mb.
	parseFWReadyKindWorkloadOOM
	// parseFWReadyKindDisk carries guest writable-root filesystem usage.
	parseFWReadyKindDisk
)

// tailEventOutcome (issue #667 / ADR-078) mirrors the
// guest-init wire byte for the per-task outcome. Duplicated
// from pkg/fcvm.TailOutcomeClosed set because cmd/vmmd does
// not import pkg/fcvm (cmd/vmmd IS the consumer; it imports
// the Manager but the Manager only exposes the typed
// TailOutcome from the package boundary — keeping the byte
// value here too is the wire-correctness guard the
// parseFWReadyMsg.Tail decoder relies on).
const (
	tailEventOutcomeCompleted byte = 0x01
	tailEventOutcomeFailed    byte = 0x02
	tailEventOutcomeTimeout   byte = 0x03
)

// parseFWReadyMsg is the typed return of
// parseFrameworkReadyDatagram. Type=0x01 fills WarmupMs +
// Kind; type=0x02/0x03 fill the matching envelope; type=0x04
// fills the Tail outcome + elapsed_ms; type=0x05 (Cluster C /
// ADR-121) fills WorkloadOOM's peak_mb + plan_mb; type=0x06 fills Disk's
// used_bytes + capacity_bytes. The
// instance id is NOT on the wire — the host resolves it from
// the DGRAM peer CID.
type parseFWReadyMsg struct {
	Kind     parseFWKind
	WarmupMs int64
	// runtime carries the runner id (e.g. "node22") for type=0x01 only.
	Runtime  string
	InitExit sidecarInitExitWire
	Restart  sidecarRestartWire
	// Tail (issue #667 / ADR-078) carries the per-task
	// outcome + elapsed_ms for type=0x04 only. Outcome is the
	// closed enum byte (1=completed, 2=failed, 3=timeout);
	// ElapsedMs is the wall-clock duration from waitUntil
	// registration to terminal in milliseconds.
	Tail parseFWReadyTailWire
	// WorkloadOOM (Cluster C / ADR-121) carries the peak /
	// plan MB tuple for type=0x05 only. Both are wall-clock
	// integers MB units; the host flows them verbatim into
	// Manager.ReportWorkloadOOM → schedd
	// Engine.DestroyForWorkloadOOMFailure → whycopy
	// CodeAppRuntimeOOM Observed closure template. Zero
	// values are tolerated at the wire (the engine guard
	// is downstream).
	WorkloadOOM workloadOOMWire
	Disk        diskUsageWire
}

type diskUsageWire struct {
	UsedBytes     int64 `json:"used_bytes"`
	CapacityBytes int64 `json:"capacity_bytes"`
}

// parseFWReadyTailWire is the type=0x04 body view (issue #667 /
// ADR-078). The wire is fixed-size: [1B type][1B outcome][6B
// reserved][8B BE uint64 elapsed_ms]; the type byte is
// stripped by parseFrameworkReadyDatagram before this struct
// is populated.
type parseFWReadyTailWire struct {
	Outcome   byte
	ElapsedMs int64
}

// workloadOOMWire is the type=0x05 body view (Cluster C /
// ADR-121). The wire is a small UTF-8 JSON envelope following
// the type byte; shape:
//
//	[1B type=0x05][json:{"peak_mb":N,"plan_mb":N}]
//
// Body cap: see VsockWorkloadOOMMaxBody (256 bytes — the JSON
// envelope of two ints is < 32 bytes; 256 is a generous
// future-proof margin). The same struct is mirrored on the
// guest-init emit side at guest/init/framework_ready_emit.go
// (the values flow end-to-end into the schedd's
// whycopy.Observed closure). The host does NOT validate the
// numeric ranges here — payload hygiene is the guest's
// responsibility; the schedd engine stamps whatever it
// receives (the typed int conversion at
// SchedAPI.DestroyForWorkloadOOMFailure is the schema
// boundary).
type workloadOOMWire struct {
	PeakMB int `json:"peak_mb"`
	PlanMB int `json:"plan_mb"`
}

// VsockWorkloadOOMMaxBody is the upper bound the host
// will read for the type=0x05 JSON envelope. The actual
// payload is < 32 bytes; the 256-byte bound is a
// future-proof margin that still pinpoints a runaway sender.
const VsockWorkloadOOMMaxBody uint32 = 256

// TypeLabel returns a human-readable label of the discriminated
// type, used only for diagnostic logs (Debug level).
func (m parseFWReadyMsg) TypeLabel() string {
	switch m.Kind {
	case parseFWReadyKindOK:
		return fmt.Sprintf("framework_ready(0x%02x)", VsockFrameworkReadyHostTypeReady)
	case parseFWReadyKindInitExit:
		return fmt.Sprintf("sidecar_init_exit(0x%02x)", VsockFrameworkReadyHostTypeInitExit)
	case parseFWReadyKindRestart:
		return fmt.Sprintf("sidecar_restart(0x%02x)", VsockFrameworkReadyHostTypeRestart)
	case parseFWReadyKindTail:
		return fmt.Sprintf("tail_event(0x%02x)", VsockFrameworkReadyHostTypeTail)
	case parseFWReadyKindWorkloadOOM:
		return fmt.Sprintf("workload_oom(0x%02x)", VsockFrameworkReadyHostTypeWorkloadOOM)
	case parseFWReadyKindDisk:
		return fmt.Sprintf("disk_telemetry(0x%02x)", VsockFrameworkReadyHostTypeDisk)
	default:
		return "unknown"
	}
}

// parseFrameworkReadyDatagram parses one DGRAM body into the
// typed parseFWReadyMsg union. Closed type set: 0x01
// (framework_ready), 0x02 (sidecar_init_exit), 0x03
// (sidecar_restart), 0x04 (tail_event, issue #667 / ADR-078).
// The instance id is NOT on the wire — the host resolves it
// from the DGRAM peer CID instead.
func parseFrameworkReadyDatagram(b []byte) (parseFWReadyMsg, error) {
	var msg parseFWReadyMsg
	if len(b) == 0 {
		return msg, fmt.Errorf("empty body")
	}
	rest := b[1:]
	switch b[0] {
	case VsockFrameworkReadyHostTypeReady:
		msg.Kind = parseFWReadyKindOK
		var warmup int64
		if len(rest) >= 4 {
			warmup = int64(binary.BigEndian.Uint32(rest[:4]))
			rest = rest[4:]
		}
		// rest is now [NUL][runtime string]. The NUL is
		// the boundary marker the proxy inserted; the
		// runtime tail follows.
		if idx := indexNUL(rest); idx >= 0 {
			msg.Runtime = string(rest[idx+1:])
		}
		msg.WarmupMs = warmup
	case VsockFrameworkReadyHostTypeInitExit:
		msg.Kind = parseFWReadyKindInitExit
		// The body after the type byte is a UTF-8 JSON
		// envelope. json.Unmarshal tolerates trailing
		// whitespace; it does NOT tolerate trailing bytes
		// past the JSON value, so the guest proxy's
		// canonical shape (a single JSON object, no
		// trailing junk) is what we rely on.
		if err := json.Unmarshal(rest, &msg.InitExit); err != nil {
			return msg, fmt.Errorf("sidecar_init_exit: %w", err)
		}
	case VsockFrameworkReadyHostTypeRestart:
		msg.Kind = parseFWReadyKindRestart
		if err := json.Unmarshal(rest, &msg.Restart); err != nil {
			return msg, fmt.Errorf("sidecar_restart: %w", err)
		}
	case VsockFrameworkReadyHostTypeTail:
		// Issue #667 / ADR-078: waitUntil terminal-event
		// envelope. Wire layout after the type byte is
		// fixed-size:
		//   [1B outcome][6B reserved][8B BE uint64 elapsed_ms]
		// Total: 15 bytes of payload. The reserved 6 bytes
		// stay 0x00 in PR 3 — reserved for a future
		// wire-level instance_id (the host currently
		// resolves instance identity via the DGRAM peer CID).
		// Short-read tolerance: any missing trailing bytes
		// surface as 0 (the runner-side emit guarantees the
		// full 15 bytes — a short read means the kernel
		// truncated the DGRAM, which is logged at Debug and
		// dropped via the loop's existing per-DGRAM error
		// handling).
		msg.Kind = parseFWReadyKindTail
		if len(rest) < 1 {
			return msg, fmt.Errorf("tail_event: missing outcome byte")
		}
		msg.Tail.Outcome = rest[0]
		// rest[1:7] reserved — intentionally not read; we
		// don't widen the struct to a slice of 6 bytes
		// just to discard it.
		if len(rest) >= 15 {
			msg.Tail.ElapsedMs = int64(binary.BigEndian.Uint64(rest[7:15]))
		}
	case VsockFrameworkReadyHostTypeWorkloadOOM:
		// Cluster C / ADR-121: workload OOM signal emitted
		// by the guest-init cgroup.events listener
		// (guest/init/cgroup_partition_linux.go::WatchOOM).
		// Wire layout after the type byte is a small
		// UTF-8 JSON envelope:
		//   {"peak_mb":<int>,"plan_mb":<int>}
		// A malformed JSON envelope is rejected here
		// (the dispatcher never sees it). The numeric
		// ranges (peak_mb > 0, plan_mb > 0, peak_mb
		// ≤ plan_mb) are intentionally NOT enforced at the
		// host — payload hygiene is the guest's
		// responsibility; the schedd engine is the type
		// boundary and stamps whatever it receives.
		//
		// Review finding #5: the body cap is now enforced
		// here. The read buffer is frameworkReadyMaxDatagram
		// = 1024 (a generous margin that bounds ALL types),
		// but the workload-OOM envelope is ≤ 32 bytes; a
		// guest emitting > VsockWorkloadOOMMaxBody is a
		// bug (the guest-side EmitWorkloadOOM clamps to
		// workloadOOMEmitMaxBody = 256 before the socket
		// opens). The host-side cap catches a hostile or
		// buggy guest that bypasses the guest-side clamp.
		if uint32(len(rest)) > VsockWorkloadOOMMaxBody {
			return msg, fmt.Errorf("workload_oom: body too large: %d > %d",
				len(rest), VsockWorkloadOOMMaxBody)
		}
		msg.Kind = parseFWReadyKindWorkloadOOM
		if err := json.Unmarshal(rest, &msg.WorkloadOOM); err != nil {
			return msg, fmt.Errorf("workload_oom: %w", err)
		}
	case VsockFrameworkReadyHostTypeDisk:
		if uint32(len(rest)) > 256 {
			return msg, fmt.Errorf("disk_telemetry: body too large: %d", len(rest))
		}
		if err := json.Unmarshal(rest, &msg.Disk); err != nil {
			return msg, fmt.Errorf("disk_telemetry: %w", err)
		}
		if msg.Disk.UsedBytes < 0 || msg.Disk.CapacityBytes <= 0 || msg.Disk.UsedBytes > msg.Disk.CapacityBytes {
			return msg, fmt.Errorf("disk_telemetry: invalid sample used=%d capacity=%d", msg.Disk.UsedBytes, msg.Disk.CapacityBytes)
		}
		msg.Kind = parseFWReadyKindDisk
	default:
		return msg, fmt.Errorf("unknown msg sub-type 0x%02x", b[0])
	}
	return msg, nil
}

func indexNUL(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return -1
}
