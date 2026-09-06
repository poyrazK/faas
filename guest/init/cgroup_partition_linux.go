//go:build linux

// Per-workload cgroup v2 partition (issue #463 / ADR-069 /
// PR-B AC #4).
//
// Why in-guest, not just host-side: the host-side
// per-instance / per-workload cgroup scopes (vmmd's
// writeWorkloadCgroup at pkg/fcvm/cgroup.go:168) scope the
// guest *from the outside*. Inside the guest, processes are
// in the guest kernel's memcg tree, and the host's cgroup
// hierarchy is not visible (the guest cgroup namespace is
// isolated by guest-init's CLONE_NEWCGROUP step in main_linux.go).
// A workload that exceeds its plan RAM or CPU triggers the
// host's per-VM scope, but inside the guest we want per-workload
// memory and CPU limits — a sidecar OOM or throttle must not
// affect the main workload (and vice versa).
//
// The contract:
//
//   1. cgroup2 is mounted at /sys/fs/cgroup inside the
//      guest (see mountCgroup2 in main_linux.go — the mount
//      happens AFTER pivotInto so the mount lives on the
//      new root).
//
//   2. Before each configured workload's exec.Command.Start, mkdir
//      the per-workload leaf at /sys/fs/cgroup/<safe-name>, write
//      any configured memory.max and cpu.max values, and after
//      Start write the child PID into cgroup.procs. Workloads
//      without either override remain under the parent scope.
//
//   3. Sidecar OOM stays scoped to that leaf (cgroup v2
//      memory controller kills only the offending leaf's
//      processes). The main workload keeps running.
//
// The cgroup name derivation is intentionally narrow: type
// + name joined with a single dash, with a guard against
// path separators that would let a workload escape the leaf.
// Mirrors writeWorkloadCgroup at pkg/fcvm/cgroup.go:172.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

// cgroupRoot (issue #463 / ADR-069 / PR-B AC #4) is the
// canonical mountpoint for the in-guest cgroup v2
// hierarchy. guest-init's mountCgroup2 (main_linux.go) mounts
// cgroup2 here after pivotInto so the leaf writes below land
// on the new root. Hardcoded to /sys/fs/cgroup because every
// Linux userland agrees on the path and the alternative
// (env-driven) would let a deployment redirect the partition
// into a workload-controlled path.
var cgroupRoot = "/sys/fs/cgroup"

// prepareWorkloadCgroup creates the in-guest resource leaf for a workload and
// returns its path for the subsequent cgroup.procs placement. A zero RAM or
// CPU value inherits the parent limit; when both are zero the legacy path is
// preserved and no child leaf is created.
func prepareWorkloadCgroup(typ, name string, ramMB int, log *slog.Logger, cpuMillicoresOpt ...int) (string, error) {
	if ramMB < 0 {
		return "", fmt.Errorf("invalid workload cgroup ram_mb %d", ramMB)
	}
	cpuMillicores := 0
	if len(cpuMillicoresOpt) > 0 {
		cpuMillicores = cpuMillicoresOpt[0]
	}
	if cpuMillicores < 0 || (cpuMillicores != 0 && !api.ValidAppCPUMillicores(cpuMillicores)) {
		return "", fmt.Errorf("invalid workload cgroup cpu_millicores %d", cpuMillicores)
	}
	if ramMB == 0 && cpuMillicores == 0 {
		return "", nil
	}
	leaf := leafDir(typ, name)
	if leaf == "" {
		return "", fmt.Errorf("invalid workload cgroup name %q/%q", typ, name)
	}
	if log == nil {
		log = slog.Default()
	}
	if err := partitionInto(leaf, ramMB, cpuMillicores); err != nil {
		log.Warn("cgroup partition into leaf failed",
			"leaf", leaf, "name", name, "err", err)
		return "", err
	}
	return leaf, nil
}

// cgroupSafeName (issue #463 / ADR-069 / PR-B AC #4) is
// the leaf-name helper. Joins type + name with a single
// dash, with a guard against path separators (a workload
// that smuggles ".." into its name must NOT escape the
// cgroup hierarchy). Empty type or name returns "" so the
// caller can skip the leaf write — empty leaves are
// indistinguishable from the root scope, and writing into
// the root would defeat the per-workload partition.
//
// Mirrors the host-side writeWorkloadCgroup path at
// pkg/fcvm/cgroup.go:172-174 (which clamps `..` and `/`),
// with the additional constraint that the in-guest
// partition never uses the workload's StorageKey (the
// guest can't read StorageBackend keys — the leaf name
// stays short, type + name only).
func cgroupSafeName(typ, name string) string {
	if typ == "" || name == "" {
		return ""
	}
	for _, ch := range name {
		if ch == '/' || ch == '\\' || ch == 0 {
			return ""
		}
	}
	// Belt-and-suspenders against the ".." escape
	// (cgroup v2 rejects leaf names containing .. in
	// practice, but checking here keeps the failure
	// observable at workload boot, not at the first
	// write to memory.max).
	if strings.Contains(name, "..") {
		return ""
	}
	return typ + "-" + name
}

// leafDir returns the absolute cgroup v2 leaf path for a
// workload spec. The path is rooted at cgroupRoot so the
// caller can os.WriteFile into it directly. The leaf is
// NOT created here — partitionInto creates it before
// writing memory.max. Empty safe name returns "" so the
// caller can skip the partition without an error (the
// workload still runs, just at the root cgroup scope —
// the per-instance parent scope still has the cap from
// vmmd's writePlanCgroup).
func leafDir(typ, name string) string {
	safe := cgroupSafeName(typ, name)
	if safe == "" {
		return ""
	}
	return filepath.Join(cgroupRoot, safe)
}

// partitionInto (issue #463 / ADR-069 / PR-B AC #4) sets up
// the per-workload cgroup v2 leaf BEFORE the workload is
// exec'd:
//
//  1. mkdir <leaf>
//  2. write memory.max = ramMB << 20 (bytes), when configured
//  3. write cpu.max = quota period, when configured
//
// Errors are logged + returned (the caller decides whether
// to fail the deploy). A zero override is omitted so the
// workload inherits the parent cgroup's corresponding limit;
// cpu.max uses a 100ms period and converts millicores into a
// quota against that period.
//
// cgroup v2 must be mounted at cgroupRoot for the writes
// to land. mountCgroup2 (main_linux.go) is called between
// pivotInto and the supervisor's first workload, so by the
// time partitionInto runs the leaf path is reachable.
func partitionInto(leaf string, ramMB int, cpuMillicoresOpt ...int) error {
	if leaf == "" {
		return errors.New("cgroup partition: empty leaf")
	}
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		return fmt.Errorf("cgroup partition: mkdir %s: %w", leaf, err)
	}
	cpuMillicores := 0
	if len(cpuMillicoresOpt) > 0 {
		cpuMillicores = cpuMillicoresOpt[0]
	}
	if ramMB < 0 || cpuMillicores < 0 || (cpuMillicores != 0 && !api.ValidAppCPUMillicores(cpuMillicores)) {
		return fmt.Errorf("cgroup partition: invalid limits ram_mb=%d cpu_millicores=%d", ramMB, cpuMillicores)
	}
	if ramMB > 0 {
		bytes := int64(ramMB) << 20
		if err := os.WriteFile(
			filepath.Join(leaf, "memory.max"),
			[]byte(strconv.FormatInt(bytes, 10)+"\n"),
			0o644,
		); err != nil {
			return fmt.Errorf("cgroup partition: write memory.max for %s: %w", leaf, err)
		}
	}
	if cpuMillicores > 0 {
		const periodUS = 100_000
		quotaUS := cpuMillicores * periodUS / 1000
		if err := os.WriteFile(
			filepath.Join(leaf, "cpu.max"),
			[]byte(fmt.Sprintf("%d %d\n", quotaUS, periodUS)),
			0o644,
		); err != nil {
			return fmt.Errorf("cgroup partition: write cpu.max for %s: %w", leaf, err)
		}
	}
	return nil
}

// placeIntoLeaf writes the child PID into the leaf's
// cgroup.procs. Called AFTER exec.Command.Start so the PID
// is the forked child's PID (the kernel's exec semantics
// preserve the PID across execve). Empty pid means the
// caller did not capture the PID — skip without error so a
// workload that fails to fork does not produce a spurious
// "cgroup.procs write failed" log line.
//
// Race window: between Start and placeIntoLeaf, the
// forked child can fork+mmap before the cgroup.procs
// write lands. The window is benign because the leaf's
// parent scope (the cgroup root, which has no cap)
// doesn't enforce a limit either; worst case is a brief
// moment where the cap is unenforced, then enforced once
// the write completes. Same posture as Docker / runc.
func placeIntoLeaf(leaf string, pid int, log *slog.Logger) {
	if leaf == "" || pid <= 0 {
		return
	}
	if err := os.WriteFile(
		filepath.Join(leaf, "cgroup.procs"),
		[]byte(strconv.Itoa(pid)+"\n"),
		0o644,
	); err != nil && log != nil {
		log.Warn("cgroup.procs write failed",
			"leaf", leaf, "pid", pid, "err", err)
	}
}

// mountCgroup2 (issue #463 / ADR-069 / PR-B AC #4) mounts
// cgroup2 at /sys/fs/cgroup inside the guest. Called from
// main_linux.go::boot AFTER pivotInto so the mount lives on
// the new root (mounting before pivot would put the
// mountpoint on the OLD root, and pivot would hide it).
//
// Returns the mount error verbatim. The caller (boot)
// tolerates a non-nil return (the host-side per-instance
// scope from vmmd is still enforced even when the in-guest
// partition is unavailable) and logs the error so a
// missing CONFIG_CGROUP_V2=y (kernel ENOSYS) is visible in
// the journalctl logs as a soft warning, not the silent
// no-op the previous shape produced.
func mountCgroup2() error {
	if err := os.MkdirAll(cgroupRoot, 0o755); err != nil {
		return fmt.Errorf("cgroup2 mkdir: %w", err)
	}
	// Firecracker guests start in the root cgroup namespace without a
	// delegated hierarchy. Enter a private cgroup namespace before mounting
	// cgroup2 so the mount is scoped to this VM and cannot block while trying
	// to attach to the host-side hierarchy.
	if err := unix.Unshare(unix.CLONE_NEWCGROUP); err != nil {
		return fmt.Errorf("cgroup2 namespace: %w", err)
	}
	if err := syscall.Mount("cgroup2", cgroupRoot, "cgroup2", 0, ""); err != nil {
		return fmt.Errorf("cgroup2 mount %s: %w", cgroupRoot, err)
	}
	if err := enableWorkloadControllers(); err != nil {
		return err
	}
	return nil
}

// enableWorkloadControllers delegates the controllers needed by workload
// leaves to the private cgroup namespace's root. A cgroup v2 child exposes
// cpu.max and memory.max only after the corresponding controller is enabled in
// its parent. Kernels that do not provide either controller remain usable for
// the legacy host-side fence; the caller receives no error when neither is
// available.
func enableWorkloadControllers() error {
	availableRaw, err := os.ReadFile(filepath.Join(cgroupRoot, "cgroup.controllers"))
	if err != nil {
		return fmt.Errorf("cgroup2 controllers: %w", err)
	}
	available := strings.Fields(string(availableRaw))
	wanted := make([]string, 0, 2)
	for _, controller := range []string{"memory", "cpu"} {
		for _, candidate := range available {
			if candidate == controller {
				wanted = append(wanted, controller)
				break
			}
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	currentRaw, err := os.ReadFile(filepath.Join(cgroupRoot, "cgroup.subtree_control"))
	if err != nil {
		return fmt.Errorf("cgroup2 subtree_control: %w", err)
	}
	current := make(map[string]bool, len(strings.Fields(string(currentRaw))))
	for _, controller := range strings.Fields(string(currentRaw)) {
		current[controller] = true
	}
	missing := make([]string, 0, len(wanted))
	for _, controller := range wanted {
		if !current[controller] {
			missing = append(missing, "+"+controller)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if err := os.WriteFile(filepath.Join(cgroupRoot, "cgroup.subtree_control"), []byte(strings.Join(missing, " ")+"\n"), 0o644); err != nil {
		return fmt.Errorf("cgroup2 enable controllers %s: %w", strings.Join(wanted, ","), err)
	}
	return nil
}

// workloadOOMEMitter is the callback the WatchOOM function
// fires once per detected oom_kill event. The signature is
// (peakMB, planMB int) — both in MB; planMB is the customer
// plan cap (what the per-leaf memory.max was set to) and
// peakMB is the highest watermark of the leaf's current
// usage before the kill landed (a saner proxy than the
// post-kill reading, which is 0 because the process is
// gone). The fan-in from peak to the schedd stamps the
// whycopy Observed closure's struct{ PeakMB, PlanMB int }
// template (pkg/whycopy/whycopy.go::CodeAppRuntimeOOM).
//
// The emit is best-effort: the workload is dead, the VM is
// about to be torn down, and a missed signal just means the
// customer sees the deployment "succeeded then failed" in
// the dashboard rather than fail-immediate. The shape is
// intentionally narrow — no ctx, no logger — because the
// emit (guest/init/framework_ready_emit.go) wires its own
// send-timeout + logger.
type workloadOOMEMitter func(peakMB, planMB int)

// WatchOOM (Cluster C / ADR-121) is the per-VM cgroup
// oom_kill listener. It blocks (in a goroutine) until
// either:
//
//  1. ctx is cancelled — the VM is shutting down, clean
//     exit.
//  2. The leaf records a new oom_kill event — the
//     listener samples the leaf's memory.current (or
//     memory.high watermark if available), invokes emit
//     (peakMB, planMB), and returns. The workload is
//     dead; the host's Manager.ReportWorkloadOOM relay
//     tears the VM down on its end.
//
// Wire protocol (issue #470 / PR #470-FU-B port 1027 +
// Cluster C type=0x05): the emit is a guest-init
// vsock DGRAM write — see
// guest/init/framework_ready_emit.go::EmitWorkloadOOM.
//
// Why guest-side, not just host-side cgroup events
// polling: the host's per-VM cgroup scope is the
// *firecracker process*, not the workload inside the
// VM. The in-guest workload cgroup v2 leaf is the only
// view that sees the per-PID OOM kill (see the file-level
// doc comment at the top of this file). A host-side
// memory.events reader would only fire when the FC
// process itself OOM'd, which is a different failure
// class.
//
// Implementation: poll(2) with POLLPRI on the leaf's
// memory.events file. The kernel publishes memory.events
// as a seqfile that supports poll(2) — POLLPRI fires
// when ANY of its counters (low, high, max, oom, oom_kill,
// oom_group_kill) increments. We track the oom_kill
// counter specifically; oom_kill fires when the memory
// controller kills a child.
//
// Why memory.events, NOT cgroup.events (review finding #1):
// the kernel's cgroup.events file only fires POLLPRI on
// populated/frozen transitions, NOT on memory.events
// oom_kill increments. A previous implementation polled
// cgroup.events and missed every real OOM kill — the
// listener would only fire when the workload started
// or exited (populated flips), which is too late: the
// processes are already torn down by the time the
// listener wakes. The kernel DOES fire POLLPRI on
// memory.events (per Linux kernel
// Documentation/admin-guide/cgroup-v2.rst §5.2 "memory.events"),
// so we poll that file instead. Each wakeup re-reads
// the leaf's memory.events file for the oom_kill counter
// and compares against the delta — the listener tracks
// the DELTA, not the absolute counter (a leaf with
// pre-existing kills shouldn't fire on watch start).
//
// Short-read tolerance: the kernel may truncate
// memory.events (it's a seqfile). The poll loop treats
// any read returning EOS as a re-try; ctx.Done() is the
// only real exit signal.
func WatchOOM(ctx context.Context, leaf string, planMB int, emit workloadOOMEMitter, log *slog.Logger) error {
	if leaf == "" {
		return errors.New("WatchOOM: empty leaf")
	}
	if emit == nil {
		return errors.New("WatchOOM: nil emitter")
	}
	leafDir := leaf
	eventsPath := filepath.Join(leafDir, "memory.events")
	// Open the memory.events file (read-only, non-blocking).
	// The kernel publishes memory.events as a seqfile that
	// supports poll(2) — POLLPRI fires when any of its
	// counters (including oom_kill) increments.
	fd, err := syscall.Open(eventsPath, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("WatchOOM: open %s: %w", eventsPath, err)
	}
	defer func() { _ = unix.Close(fd) }()

	// Track the baseline oom_kill counter so a leaf that
	// already has kills on watch-start does not fire
	// spuriously. We re-read on every wakeup and compare
	// against this baseline.
	baseline, _ := readMemoryEventsOOMKills(leafDir)

	pollFds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLPRI}}
	buf := make([]byte, 4096)
	for {
		// Honor ctx cancel — the VM shutdown path
		// expects the listener to exit within one tick.
		if err := ctx.Err(); err != nil {
			return err
		}
		// 1s poll timeout keeps ctx cancellation
		// responsive without burning CPU. memory.events
		// updates don't fire frequently in a healthy
		// workload, so the wake is event-driven, not
		// timer-driven.
		n, perr := unix.Poll(pollFds, 1000)
		if perr != nil {
			if errors.Is(perr, unix.EINTR) {
				continue
			}
			return fmt.Errorf("WatchOOM: poll: %w", perr)
		}
		if n == 0 {
			continue
		}
		// Drain the seqfile — every read() advances
		// the kernel's read position. The data itself
		// is irrelevant (we re-parse memory.events
		// below); the read clears any POLLPRI
		// re-arming.
		if _, rerr := unix.Read(fd, buf); rerr != nil && rerr != unix.EAGAIN {
			if errors.Is(rerr, unix.EINTR) {
				continue
			}
			// Non-fatal: continue the loop, the
			// next poll tick will re-check.
			if log != nil {
				log.Debug("WatchOOM: memory.events read", "err", rerr)
			}
		}
		// Re-sample the leaf's memory.events for the
		// current oom_kill counter. Compare against
		// the baseline; a delta is the trigger.
		current, currentErr := readMemoryEventsOOMKills(leafDir)
		if currentErr != nil {
			if log != nil {
				log.Debug("WatchOOM: memory.events read", "err", currentErr)
			}
			continue
		}
		if current <= baseline {
			// No new kill yet. The memory.events
			// wakeup may have been a different
			// event (e.g. memory.low); keep
			// polling.
			continue
		}
		// delta > 0 — fire the emit. peakMB is the
		// leaf's current usage at the moment of the
		// kill (rounded to MB); reading memory.high
		// would be ideal but it's a "high watermark"
		// that the leaf may or may not have (it's
		// emitted under memory.events.high). Fall back
		// to memory.current (the live usage) if high
		// is unavailable.
		peakBytes, _ := readMemoryHighOrCurrent(leafDir)
		peakMB := int((peakBytes + (1 << 20) - 1) >> 20) // ceil to MB
		// planMB on the wire signature is the customer's
		// plan cap. The caller must pass the real
		// plan MB (mirroring how the schedd stamps
		// Why / Fix prose); if the caller passes 0
		// (legacy / unknown plan) we leave it 0 and
		// the whycopy Observed closure degrades to
		// the static prose. We intentionally do NOT
		// re-read the leaf's memory.max — it's a
		// 1 MiB defense-in-depth floor set by
		// partitionInto, not the customer's plan cap.
		// Reading it produces misleading "1 MB plan"
		// prose. See review finding #2 — a future PR
		// should inject the real plan cap via a vmmd
		// boot env var.
		effectivePlanMB := planMB
		emit(peakMB, effectivePlanMB)
		return nil
	}
}

// readMemoryMax reads the leaf's memory.max cap (in bytes).
// Returns 0 + error if the file is absent or unparseable —
// the caller degrades to the static whycopy prose in that
// case.
func readMemoryMax(leafDir string) (uint64, error) {
	b, err := os.ReadFile(filepath.Join(leafDir, "memory.max"))
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(b))
	if s == "max" {
		// Kernel semantics: "max" = unlimited
		return 0, nil
	}
	return strconv.ParseUint(s, 10, 64)
}

// readMemoryEventsOOMKills parses the leaf's
// memory.events file for the "oom_kill" counter. Returns
// 0 + nil if the file is absent (kernel pre-5.x or a
// test fixture that hasn't populated the cgroup events
// file yet) — the listener stays silent, matching the
// pre-Fix-1 contract. A parse failure on a present file
// surfaces as the underlying error so a real bug is
// visible in the log, not silently swallowed.
func readMemoryEventsOOMKills(leafDir string) (uint64, error) {
	b, err := os.ReadFile(filepath.Join(leafDir, "memory.events"))
	if err != nil {
		if os.IsNotExist(err) {
			// Absent file = silent zero (the kernel
			// doesn't expose memory.events on pre-5.x,
			// and a fresh cgroup leaf hasn't populated
			// the seqfile until the first event lands).
			// The listener stays quiet; the
			// WatchOOM baseline stays 0, so no delta
			// to fire on.
			return 0, nil
		}
		return 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "oom_kill ") || strings.HasPrefix(line, "oom_kill\t") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return strconv.ParseUint(fields[1], 10, 64)
			}
		}
	}
	return 0, nil
}

// readMemoryHighOrCurrent prefers memory.events.high
// (the cgroup v2 high-watermark since the last reset),
// falls back to memory.current if the high field is
// absent (kernel pre-6.x). Returns 0 + nil on read
// failure — the listener treats 0 as "unknown peak"
// rather than "0 MB peak", and the silent-zero path
// matches the readMemoryEventsOOMKills contract (a
// fresh cgroup leaf with no populated events stays
// quiet rather than erroring out).
func readMemoryHighOrCurrent(leafDir string) (uint64, error) {
	if b, err := os.ReadFile(filepath.Join(leafDir, "memory.events")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "high ") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					return strconv.ParseUint(fields[1], 10, 64)
				}
			}
		}
	}
	b, err := os.ReadFile(filepath.Join(leafDir, "memory.current"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
}
