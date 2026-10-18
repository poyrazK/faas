//go:build unix

package wire

// Two-generation hand-off (docs/adr/1580-zero-downtime-daemon-restarts.md).
//
// systemd restarts a service by stopping it and then starting it, so a
// request still running when the stop's grace ends is cut. A hand-off avoids
// the stop: `systemctl reload` sends SIGUSR2, and the running daemon starts
// its successor (the unit's ExecStart path, which a release has pointed at
// the new binary) with every listening socket it serves. Once the successor
// reports ready, the old daemon makes it the unit's main process
// (MAINPID=), stops accepting, and finishes the requests and streams it holds
// for up to api.HandoffDrainMaxSeconds. Both generations accept from the same
// kernel queues meanwhile, so no connection is refused or queued behind a
// drain. Anything that fails before the switch leaves the old daemon serving.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/daemonunit"
)

const (
	// handoffFDsEnv counts the sockets passed to a successor from fd 3.
	handoffFDsEnv = "FAAS_HANDOFF_FDS"
	// handoffParentEnv names the predecessor's PID.
	handoffParentEnv = "FAAS_HANDOFF_PARENT"
)

// HandoffOptions configures Handoff. The zero value is production's.
type HandoffOptions struct {
	// ReadyTimeout bounds the successor's start-up
	// (default api.HandoffReadyTimeoutSeconds).
	ReadyTimeout time.Duration
	// Path and Args start the successor; empty means os.Args, the unit's
	// ExecStart path that a release flips to the new binary.
	Path string
	Args []string
	// Log receives progress; nil discards it.
	Log *slog.Logger

	// Test seams: whether the process runs under systemd, the main-PID
	// notification and its confirmation, and the cgroup memory check.
	underSystemd  func() bool
	notify        func(state string) error
	confirmMain   func(ctx context.Context, pid int) error
	memoryAllowed func() error
}

// HandoffRequests delivers one value per SIGUSR2, the signal the units'
// ExecReload= sends. It stops delivering when ctx ends; the channel is never
// closed, so a select on it cannot fire spuriously during shutdown.
func HandoffRequests(ctx context.Context) <-chan struct{} {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGUSR2)
	out := make(chan struct{}, 1)
	go func() {
		defer signal.Stop(sig)
		for {
			select {
			case <-ctx.Done():
				return
			case <-sig:
				select {
				case out <- struct{}{}:
				default:
				}
			}
		}
	}()
	return out
}

// IsHandoffSuccessor reports whether a predecessor started this process.
func IsHandoffSuccessor() bool {
	_, ok := os.LookupEnv(handoffParentEnv)
	return ok
}

// Handoff starts this daemon's successor with every shared listening socket
// and returns its PID once it is ready and the unit's main process. On error
// the successor has been stopped and this process should keep serving.
func Handoff(ctx context.Context, opts HandoffOptions) (int, error) {
	opts = opts.withDefaults()
	if !opts.underSystemd() {
		return handoff(ctx, opts)
	}
	// A deploy that reloads the unit reads STATUS= to tell a refused or
	// failed hand-off from one still starting (cmd/deployctl).
	_ = opts.notify("STATUS=" + HandoffStatusStarted)
	pid, err := handoff(ctx, opts)
	if err != nil {
		msg := strings.ReplaceAll(err.Error(), "\n", " ")
		_ = opts.notify(fmt.Sprintf("STATUS=%s %s: %s", HandoffStatusFailed, time.Now().UTC().Format(time.RFC3339Nano), msg))
	}
	return pid, err
}

func handoff(ctx context.Context, opts HandoffOptions) (int, error) {
	if err := opts.memoryAllowed(); err != nil {
		return 0, fmt.Errorf("wire: hand-off refused: %w", err)
	}
	endPredecessor(opts.Log)

	files, err := processActivation().handoffFiles()
	if err != nil {
		return 0, err
	}
	defer closeFiles(files)
	readR, readW, err := os.Pipe()
	if err != nil {
		return 0, fmt.Errorf("wire: hand-off ready pipe: %w", err)
	}
	defer func() { _ = readR.Close() }()

	// An intermediate starts the successor so that it is not our child
	// (handoff_spawn.go); it reports the successor's PID on the ready pipe.
	cmd := exec.Command(opts.Path, opts.Args...) //nolint:gosec // the daemon's own ExecStart path and arguments
	cmd.Env = append(handoffEnv(os.Environ(), len(files)), handoffSpawnEnv+"=1")
	cmd.ExtraFiles = append(append([]*os.File(nil), files...), readW)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		_ = readW.Close()
		return 0, fmt.Errorf("wire: start successor %s: %w", opts.Path, err)
	}
	_ = readW.Close()
	lines := handoffLines(readR)
	pid, err := waitSuccessorStarted(ctx, cmd, lines)
	if err != nil {
		return 0, fmt.Errorf("wire: start successor %s: %w", opts.Path, err)
	}
	opts.Log.Info("hand-off: successor started", "pid", pid, "path", opts.Path, "sockets", len(files))

	if err := waitSuccessorReady(ctx, lines, opts.ReadyTimeout); err != nil {
		stopSuccessor(pid)
		return 0, fmt.Errorf("wire: successor %d: %w", pid, err)
	}
	if opts.underSystemd() {
		if err := opts.notify(fmt.Sprintf("MAINPID=%d\nSTATUS=handed off to %d", pid, pid)); err != nil {
			stopSuccessor(pid)
			return 0, fmt.Errorf("wire: hand main PID to %d: %w", pid, err)
		}
		if err := opts.confirmMain(ctx, pid); err != nil {
			// systemd would treat this process's exit as the service's and
			// stop the successor with it; keep serving instead.
			stopSuccessor(pid)
			return 0, fmt.Errorf("wire: systemd did not adopt successor %d: %w", pid, err)
		}
	}
	daemonunit.MarkHandedOff()
	opts.Log.Info("hand-off: successor is the main process; draining this generation", "pid", pid)
	return pid, nil
}

func (o HandoffOptions) withDefaults() HandoffOptions {
	if o.ReadyTimeout <= 0 {
		o.ReadyTimeout = time.Duration(api.HandoffReadyTimeoutSeconds) * time.Second
	}
	if o.Path == "" {
		o.Path, o.Args = os.Args[0], os.Args[1:]
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.underSystemd == nil {
		o.underSystemd = func() bool { return os.Getenv("NOTIFY_SOCKET") != "" }
	}
	if o.notify == nil {
		o.notify = daemonunit.Notify
	}
	if o.confirmMain == nil {
		o.confirmMain = confirmSystemdMainPID
	}
	if o.memoryAllowed == nil {
		o.memoryAllowed = cgroupMemoryAllowsSecondGeneration
	}
	return o
}

// endPredecessor asks a still-draining predecessor to finish now: at most
// two generations run at once, and the newest hand-off keeps the full drain.
func endPredecessor(log *slog.Logger) {
	parent, err := strconv.Atoi(os.Getenv(handoffParentEnv))
	// The successor is re-parented to PID 1 (handoff_spawn.go), so the
	// predecessor is recognized by its cgroup, the unit's, not as a parent.
	if err != nil || parent <= 1 || !sameCgroup(parent) {
		return
	}
	if err := syscall.Kill(parent, syscall.SIGTERM); err == nil {
		log.Info("hand-off: ending the previous generation's drain", "pid", parent)
	}
}

// handoffEnv is the successor's environment: no systemd activation or
// watchdog PID of ours (the successor becomes the main process and pings for
// itself), plus the passed sockets, the ready pipe and our PID.
func handoffEnv(env []string, sockets int) []string {
	out := make([]string, 0, len(env)+3)
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		switch key {
		case "LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES", "WATCHDOG_PID", handoffFDsEnv, handoffParentEnv, daemonunit.HandoffReadyFDEnv:
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		handoffFDsEnv+"="+strconv.Itoa(sockets),
		daemonunit.HandoffReadyFDEnv+"="+strconv.Itoa(activationFirstFD+sockets),
		handoffParentEnv+"="+strconv.Itoa(os.Getpid()),
	)
}

// handoffLines delivers each line written to the ready pipe and closes
// when every writer has closed it: the intermediate after the PID line, the
// successor after "ready" or when it exits.
func handoffLines(r *os.File) <-chan string {
	lines := make(chan string, 4)
	go func() {
		defer close(lines)
		br := bufio.NewReader(r)
		for {
			text, err := br.ReadString('\n')
			if t := strings.TrimSpace(text); t != "" {
				lines <- t
			}
			if err != nil {
				return
			}
		}
	}()
	return lines
}

// waitSuccessorStarted waits for the intermediate to report the
// successor's PID and exit.
func waitSuccessorStarted(ctx context.Context, intermediate *exec.Cmd, lines <-chan string) (int, error) {
	exited := make(chan error, 1)
	go func() { exited <- intermediate.Wait() }()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case line, ok := <-lines:
		<-exited
		pid, err := strconv.Atoi(strings.TrimPrefix(line, handoffPIDLine+" "))
		if !ok || !strings.HasPrefix(line, handoffPIDLine+" ") || err != nil || pid <= 1 {
			return 0, fmt.Errorf("intermediate reported no successor (read %q)", line)
		}
		return pid, nil
	case <-timer.C:
	case <-ctx.Done():
	}
	_ = intermediate.Process.Kill()
	<-exited
	return 0, errors.New("intermediate did not start the successor")
}

func waitSuccessorReady(ctx context.Context, lines <-chan string, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case got, ok := <-lines:
		if ok && got == daemonunit.HandoffReadyMessage {
			return nil
		}
		if !ok {
			return errors.New("exited before ready")
		}
		return fmt.Errorf("unexpected readiness report %q", got)
	case <-timer.C:
		return fmt.Errorf("not ready within %v", timeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stopSuccessor ends a successor that did not take over. It is PID 1's
// child, so this process signals it and waits for it to be gone.
func stopSuccessor(pid int) {
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(time.Duration(api.GatewayDrainGraceSeconds+5) * time.Second)
	for processExists(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func processExists(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// handoffFiles duplicates every shared socket for the successor. Unix
// listeners stop unlinking their path on close: the successor serves it.
//
// The duplicates come from the raw descriptor, not from net's File(): that
// File's Fd(), which os/exec calls for ExtraFiles, switches the descriptor to
// blocking mode. O_NONBLOCK belongs to the open file description that both
// generations share, so the old generation's accept would then block in the
// kernel and its Close would hang until another connection arrived.
func (s *activationSet) handoffFiles() ([]*os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var files []*os.File
	for _, x := range s.shared {
		if l, ok := x.(*net.UnixListener); ok {
			l.SetUnlinkOnClose(false)
		}
		conn, ok := x.(syscall.Conn)
		if !ok {
			continue
		}
		raw, err := conn.SyscallConn()
		if err != nil {
			continue
		}
		dup := -1
		var dupErr error
		if err := raw.Control(func(fd uintptr) {
			dup, dupErr = unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 0)
		}); err != nil {
			continue // closed since it was shared
		}
		if dupErr != nil {
			closeFiles(files)
			return nil, fmt.Errorf("wire: duplicate shared socket for hand-off: %w", dupErr)
		}
		files = append(files, os.NewFile(uintptr(dup), "handoff"))
	}
	return files, nil
}

func closeFiles(files []*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

// confirmSystemdMainPID waits until systemd reports pid as the unit's main
// process. A MAINPID= notification is not acknowledged, and if systemd kept
// this process as main, its exit would stop the unit with the successor.
func confirmSystemdMainPID(ctx context.Context, pid int) error {
	unit, err := ownUnit()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := exec.CommandContext(ctx, "systemctl", "show", "--property=MainPID", "--value", unit).Output()
		if err == nil && strings.TrimSpace(string(out)) == strconv.Itoa(pid) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s MainPID = %q, want %d: %w", unit, strings.TrimSpace(string(out)), pid, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// ownUnit names this process's systemd service from its cgroup.
func ownUnit() (string, error) {
	path, err := ownCgroup()
	if err != nil {
		return "", err
	}
	unit := filepath.Base(path)
	if !strings.HasSuffix(unit, ".service") {
		return "", fmt.Errorf("cgroup %q is not a service", path)
	}
	return unit, nil
}

func ownCgroup() (string, error) {
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if path, ok := strings.CutPrefix(line, "0::"); ok {
			return path, nil
		}
	}
	return "", errors.New("no cgroup v2 entry in /proc/self/cgroup")
}

// cgroupMemoryAllowsSecondGeneration refuses a hand-off while the unit's
// cgroup uses more than api.HandoffMaxMemoryPercent of its MemoryMax. Without
// cgroup v2 accounting or a limit (development) there is nothing to check.
func cgroupMemoryAllowsSecondGeneration() error {
	current, limit, ok := cgroupMemory()
	if !ok || current*100 <= limit*int64(api.HandoffMaxMemoryPercent) {
		return nil
	}
	return fmt.Errorf("cgroup uses %d of %d bytes; a second generation needs it below %d%%", current, limit, api.HandoffMaxMemoryPercent)
}

// cgroupMemory reads this process's cgroup memory.current and memory.max;
// ok is false when either is unavailable or unlimited.
func cgroupMemory() (current, limit int64, ok bool) {
	path, err := ownCgroup()
	if err != nil {
		return 0, 0, false
	}
	read := func(name string) (int64, bool) {
		raw, err := os.ReadFile(filepath.Join("/sys/fs/cgroup", path, name))
		if err != nil {
			return 0, false
		}
		v, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		return v, err == nil && v > 0
	}
	limit, okLimit := read("memory.max") // "max" fails to parse: unlimited
	current, okCurrent := read("memory.current")
	return current, limit, okLimit && okCurrent
}
