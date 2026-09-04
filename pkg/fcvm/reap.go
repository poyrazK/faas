// reap.go — startup sweep for microVMs orphaned by a vmmd restart.
//
// Why this exists. Firecracker children do NOT die with vmmd. They are
// started via the jailer and live in faas-tenant.slice, a sibling of
// vmmd's own cgroup, so neither systemd's KillMode nor the tenant slice
// notices vmmd going away. vmmd's live-instance map, however, is purely
// in-memory: after a restart the new process has no handle on them.
//
// The result is a VM that is running and charged against tenant RAM but
// is unreachable — ForwardHTTPStream resolves through Manager.live, so
// nothing can route to it — and whose jail chroot keeps sitting on the
// /srv/fc/jail tmpfs. Measured on a production compute node on
// 2026-09-04: 23 such VMs, oldest 3.7 days, holding 5.3 GB in
// faas-tenant.slice. That violates spec §6.2-4 (a parked app consumes
// zero resident RAM) and starves real wakes through the §6.2-2 RAM
// admission ceiling.
//
// Why it is gated on the scheduler's view instead of just killing
// everything it finds. A vmmd restart does not imply its VMs are dead.
// On the same node, at the same moment, 2 of the 25 running Firecracker
// processes were instances schedd still considered live — one RUNNING
// and serving, one mid-SNAPSHOTTING. A blanket "vmmd started, so
// everything on disk is garbage" sweep would have killed a customer's
// running VM and corrupted a snapshot in flight. So the predicate is
// the durable state, not the in-memory map: reap only what the
// scheduler already believes is gone.
//
// Failure posture is deliberately conservative. Anything unknown is
// left alone: an IsLive error skips the instance (a wedged Postgres
// must never turn into a fleet-wide VM kill), a chroot younger than
// MinAge is skipped (it may belong to a wake that has not reached the
// database yet), and a directory whose name is not an instance UUID is
// ignored. Reaping late is cheap; reaping wrongly is an outage.

package fcvm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultReapMinAge is how old a jail chroot must be before the sweep
// will consider it. It covers the window between vmmd creating the
// chroot and schedd committing the instance row that IsLive reads.
// Cold boot is the long pole there and is budgeted in tens of seconds,
// so two minutes is several times the worst observed case.
const DefaultReapMinAge = 2 * time.Minute

// LiveInstanceFunc reports whether the scheduler still considers
// instanceID live (any of WAKING / COLD_BOOTING / RUNNING /
// SNAPSHOTTING / MIGRATING). Implementations read durable state, not
// vmmd's memory — that is the whole point of the gate.
//
// A non-nil error means "unknown", and the caller skips the instance.
// Returning (false, nil) authorises a kill, so an implementation must
// never collapse a lookup failure into false.
type LiveInstanceFunc func(ctx context.Context, instanceID string) (bool, error)

// ReapOptions configures ReapOrphanedJails. JailRoot, IsLive and Runner
// are required; the rest have documented defaults.
type ReapOptions struct {
	// JailRoot is the version-scoped jail directory whose children are
	// per-instance chroots — <chrootBase>/<fcName>, e.g.
	// /srv/fc/jail/firecracker-v1.7.0-x86_64.
	JailRoot string
	// IsLive is the durable-state gate. Required: a nil IsLive makes
	// the sweep a no-op rather than an unconditional kill.
	IsLive LiveInstanceFunc
	// Runner executes the teardown argv (ip netns del, ip link del).
	Runner Runner
	// Log is optional; nil disables logging.
	Log *slog.Logger
	// MinAge defaults to DefaultReapMinAge. Chroots younger than this
	// are skipped.
	MinAge time.Duration
	// KillGrace is how long a VM gets between SIGTERM and SIGKILL.
	// Defaults to 5s.
	KillGrace time.Duration
	// ProcRoot defaults to "/proc"; overridden by tests.
	ProcRoot string
	// killProcess and now are test seams.
	killProcess func(pid int, sig os.Signal) error
	now         func() time.Time
}

// ReapReport is the outcome of one sweep, suitable for a single
// structured log line and for assertions in tests.
type ReapReport struct {
	// Scanned is the number of per-instance chroots examined.
	Scanned int
	// Reaped is the number of instances torn down.
	Reaped int
	// SkippedLive is the number left alone because the scheduler still
	// considers them live — the count that must stay non-zero-able, as
	// it is the evidence the gate is doing its job.
	SkippedLive int
	// SkippedYoung is the number left alone for being younger than MinAge.
	SkippedYoung int
	// SkippedUnknown is the number left alone because IsLive errored.
	SkippedUnknown int
}

// ReapOrphanedJails sweeps JailRoot once and tears down every
// per-instance chroot whose instance the scheduler no longer considers
// live. It is safe to call on a node with live VMs; see the file
// comment for the gate and the failure posture.
//
// Teardown per orphan, best effort and in this order: SIGTERM the
// Firecracker process (SIGKILL after KillGrace), delete the network
// namespace, delete the host-side veth, remove the chroot. A failure at
// any step is logged and the sweep continues — a chroot that cannot be
// removed must not strand the rest of the sweep.
//
// A missing JailRoot is not an error: a node that has never booted a VM
// has no jail tree.
func ReapOrphanedJails(ctx context.Context, opts ReapOptions) (ReapReport, error) {
	var rep ReapReport
	if opts.IsLive == nil {
		return rep, errors.New("fcvm: reap: nil IsLive; refusing to sweep without a liveness gate")
	}
	if opts.JailRoot == "" {
		return rep, errors.New("fcvm: reap: empty JailRoot")
	}
	if opts.MinAge <= 0 {
		opts.MinAge = DefaultReapMinAge
	}
	if opts.KillGrace <= 0 {
		opts.KillGrace = 5 * time.Second
	}
	if opts.ProcRoot == "" {
		opts.ProcRoot = "/proc"
	}
	if opts.now == nil {
		opts.now = time.Now
	}
	if opts.killProcess == nil {
		opts.killProcess = func(pid int, sig os.Signal) error {
			p, err := os.FindProcess(pid)
			if err != nil {
				return err
			}
			return p.Signal(sig)
		}
	}

	entries, err := os.ReadDir(opts.JailRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return rep, nil
		}
		return rep, fmt.Errorf("fcvm: reap: read jail root %q: %w", opts.JailRoot, err)
	}

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if !e.IsDir() || !looksLikeInstanceID(e.Name()) {
			continue
		}
		id := e.Name()
		rep.Scanned++

		info, err := e.Info()
		if err != nil {
			rep.SkippedUnknown++
			continue
		}
		if opts.now().Sub(info.ModTime()) < opts.MinAge {
			rep.SkippedYoung++
			continue
		}

		live, err := opts.IsLive(ctx, id)
		if err != nil {
			// Unknown is not dead. A wedged store must never
			// escalate into killing VMs.
			rep.SkippedUnknown++
			logWarn(opts.Log, "vmmd: reap: liveness unknown; leaving instance alone",
				"instance", id, "err", err)
			continue
		}
		if live {
			rep.SkippedLive++
			continue
		}

		reapOne(ctx, opts, id)
		rep.Reaped++
	}
	return rep, nil
}

// reapOne tears down a single orphan. Every step is best effort: the
// point is to release resources, and a partial release beats aborting
// the sweep.
func reapOne(ctx context.Context, opts ReapOptions, id string) {
	slot := -1
	if pid, s, ok := findFirecrackerPID(opts.ProcRoot, id); ok {
		slot = s
		_ = opts.killProcess(pid, os.Interrupt)
		if waitForExit(opts.ProcRoot, pid, opts.KillGrace, opts.now) {
			logInfo(opts.Log, "vmmd: reap: orphan exited on SIGTERM", "instance", id, "pid", pid)
		} else {
			_ = opts.killProcess(pid, os.Kill)
			logInfo(opts.Log, "vmmd: reap: orphan required SIGKILL", "instance", id, "pid", pid)
		}
	}

	if opts.Runner != nil {
		// `ip netns del` also destroys the veth pair's peer, which
		// takes the host side with it. The explicit link delete
		// covers the case where the peer was never moved into the
		// namespace (a wake that failed between link add and netns
		// assign), which is exactly how host-side veths leaked
		// before this sweep existed.
		if err := opts.Runner.Run(ctx, []string{"ip", "netns", "del", "fc-" + id}); err != nil {
			logDebug(opts.Log, "vmmd: reap: netns del", "instance", id, "err", err)
		}
		if slot >= 0 {
			if err := opts.Runner.Run(ctx, []string{"ip", "link", "del", fmt.Sprintf("vh%d", slot)}); err != nil {
				logDebug(opts.Log, "vmmd: reap: veth del", "instance", id, "err", err)
			}
		}
	}

	chroot := filepath.Join(opts.JailRoot, id)
	if err := os.RemoveAll(chroot); err != nil {
		logWarn(opts.Log, "vmmd: reap: remove chroot", "instance", id, "path", chroot, "err", err)
		return
	}
	logInfo(opts.Log, "vmmd: reap: released orphaned microVM", "instance", id)
}

// waitForExit polls until pid is gone or the grace elapses. Polling
// rather than wait4 because the orphan is not our child — it was
// reparented to init when the previous vmmd died, so we can only
// observe it.
func waitForExit(procRoot string, pid int, grace time.Duration, now func() time.Time) bool {
	deadline := now().Add(grace)
	for now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(procRoot, fmt.Sprint(pid))); errors.Is(err, os.ErrNotExist) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	_, err := os.Stat(filepath.Join(procRoot, fmt.Sprint(pid)))
	return errors.Is(err, os.ErrNotExist)
}

// findFirecrackerPID locates the Firecracker process for instanceID by
// scanning procRoot for a cmdline carrying `--id <instanceID>`, and
// recovers the allocator slot from the jailer's `--uid`.
//
// Scanning /proc rather than tracking a pidfile is deliberate: the
// orphan predates this process, so there is no handle to inherit. The
// `--id` argument is the same instance UUID the chroot is named after,
// which is what makes the two views joinable at all.
//
// The returned slot is used to name the host-side veth (vh<slot>);
// ok=false means no process was found, which is normal for a chroot
// whose VM already exited.
func findFirecrackerPID(procRoot, instanceID string) (pid int, slot int, ok bool) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return 0, -1, false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p, err := parsePID(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		args := strings.Split(string(raw), "\x00")
		if !cmdlineHasIDArg(args, instanceID) {
			continue
		}
		return p, slotFromUIDArg(args), true
	}
	return 0, -1, false
}

// cmdlineHasIDArg reports whether argv contains the pair `--id <id>`.
// Matching the pair rather than substring-searching the raw cmdline
// keeps a UUID that merely appears in some other argument (a path, a
// storage key) from selecting the wrong process to kill.
func cmdlineHasIDArg(args []string, id string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--id" && args[i+1] == id {
			return true
		}
	}
	return false
}

// slotFromUIDArg recovers the allocator slot from the jailer's --uid
// argument (UID == JailUIDBase + slot). Returns -1 when absent or out
// of range, which suppresses the host-veth delete rather than guessing
// a link name that might belong to a live instance.
func slotFromUIDArg(args []string) int {
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "--uid" {
			continue
		}
		uid, err := parsePID(args[i+1])
		if err != nil {
			return -1
		}
		slot := uid - JailUIDBase
		if slot < 0 || slot >= MaxSlots {
			return -1
		}
		return slot
	}
	return -1
}

func parsePID(s string) (int, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not numeric: %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

// looksLikeInstanceID keeps the sweep to directories that are plausibly
// instance UUIDs, so a stray file or an operator's scratch directory
// under the jail root is never a kill target.
func looksLikeInstanceID(name string) bool {
	if len(name) != 36 {
		return false
	}
	for i, r := range name {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}

func logInfo(l *slog.Logger, msg string, args ...any) {
	if l != nil {
		l.Info(msg, args...)
	}
}

func logWarn(l *slog.Logger, msg string, args ...any) {
	if l != nil {
		l.Warn(msg, args...)
	}
}

func logDebug(l *slog.Logger, msg string, args ...any) {
	if l != nil {
		l.Debug(msg, args...)
	}
}
