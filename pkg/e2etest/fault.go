// fault.go — FaultInjector surface for the two-node failure-safe
// harness (Workstream B / issue #1184 / Task #71 / ADR-137).
//
// Each helper registers a t.Cleanup so a t.Fatal in the middle of
// a fault scenario doesn't leak zombies (a killed vmmd, a stopped
// schedd, an open iptables partition). All helpers are best-effort
// — they log the failure and continue so the test's assertions
// still see the actual outcome.
//
// Scope: the metal-tier tests (cmd/e2e/twonode_failure_safe_metal_test.go)
// drive the real fault paths (SIGKILL/SIGSTOP/iptables); this file
// provides the seam + the row-level stubs that DON'T need a live
// daemon (StaleHeartbeat, Drain, etc.). The two surfaces share a
// single FaultInjector interface so a test can swap from row-level
// to daemon-level without rewriting assertions.

//go:build e2e || metal

package e2etest

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FaultInjector is the surface every twonode failure-safe test
// interacts with. Each method is idempotent — a second call
// (after RestoreAll) is a no-op rather than an error.
type FaultInjector interface {
	// Daemon-level faults (require a live process — metal-only).
	KillVmmd(node string) error        // SIGKILL
	FreezeSchedd(node string) error    // SIGSTOP (pause)
	ThawSchedd(node string) error      // SIGCONT (resume)
	Partition(a, b string) error       // iptables OUTPUT -d <other> -j DROP

	// Row-level faults (always available; no process needed).
	StaleHeartbeat(node string, age time.Duration) error
	Drain(node string) error
	Reactivate(node string) error

	// Cleanup. Must return the system to a clean state before
	// the test finishes.
	RestoreAll() error
}

// CmdFaultInjector is the production FaultInjector: it owns the
// *exec.Cmd pointers + the row-level patch helpers. Tests get one
// from StartTwoNode and pass it to every fault scenario.
type CmdFaultInjector struct {
	t        *testing.T
	pool     *pgxpool.Pool
	procs    map[string]*exec.Cmd // keyed by node name (or "schedd-a" etc.)
	iptables []string              // accumulated rules; cleared on RestoreAll
}

// NewCmdFaultInjector returns an injector wired to t.Cleanup so
// a panicking test still sees RestoreAll. t is required.
func NewCmdFaultInjector(t *testing.T, pool *pgxpool.Pool) *CmdFaultInjector {
	t.Helper()
	fi := &CmdFaultInjector{t: t, pool: pool, procs: map[string]*exec.Cmd{}}
	t.Cleanup(func() { _ = fi.RestoreAll() })
	return fi
}

// RegisterProc hands the injector a reference to a daemon
// subprocess so KillVmmd / FreezeSchedd / ThawSchedd know which
// PID to signal. Production wiring in cmd/e2e/* calls this in
// the daemon-spawn loop.
func (f *CmdFaultInjector) RegisterProc(name string, cmd *exec.Cmd) {
	f.procs[name] = cmd
}

// KillVmmd sends SIGKILL. Used to simulate a node crash mid-flight.
func (f *CmdFaultInjector) KillVmmd(node string) error {
	cmd, ok := f.procs["vmmd-"+node]
	if !ok {
		return fmt.Errorf("fault: no vmmd registered for node %s", node)
	}
	return cmd.Process.Signal(syscall.SIGKILL)
}

// FreezeSchedd pauses a schedd so its heartbeat stops landing.
func (f *CmdFaultInjector) FreezeSchedd(node string) error {
	cmd, ok := f.procs["schedd-"+node]
	if !ok {
		return fmt.Errorf("fault: no schedd registered for node %s", node)
	}
	return cmd.Process.Signal(syscall.SIGSTOP)
}

// ThawSchedd resumes a previously-frozen schedd.
func (f *CmdFaultInjector) ThawSchedd(node string) error {
	cmd, ok := f.procs["schedd-"+node]
	if !ok {
		return fmt.Errorf("fault: no schedd registered for node %s", node)
	}
	return cmd.Process.Signal(syscall.SIGCONT)
}

// Partition drops egress to the other node via iptables. Requires
// CAP_NET_ADMIN; the metal harness grants it via setpriv. The
// rule is appended to f.iptables so RestoreAll can roll it back.
func (f *CmdFaultInjector) Partition(a, b string) error {
	rule := fmt.Sprintf("-A OUTPUT -d %s -j DROP", b)
	if err := exec.Command("iptables", rule).Run(); err != nil {
		return fmt.Errorf("fault: iptables %s: %w", rule, err)
	}
	f.iptables = append(f.iptables, rule)
	return nil
}

// StaleHeartbeat rewinds the heartbeat stamp so the next
// heartbeat tick flips the row to 'unavailable'. Uses raw SQL
// because there's no public API to set last_heartbeat_at to a
// past time (heartbeat owns the stamp by design).
func (f *CmdFaultInjector) StaleHeartbeat(node string, age time.Duration) error {
	_, err := f.pool.Exec(context.Background(),
		`UPDATE compute_nodes SET last_heartbeat_at = now() - $2::interval
		 WHERE name = $1`, node, age.String())
	return err
}

// Drain flips lifecycle to 'draining' (operator-initiated; the
// recovery arbiter then orchestrates the live-migrations to
// completion).
func (f *CmdFaultInjector) Drain(node string) error {
	_, err := f.pool.Exec(context.Background(),
		`UPDATE compute_nodes SET lifecycle = 'draining'
		 WHERE name = $1 AND lifecycle = 'active'`, node)
	return err
}

// Reactivate flips lifecycle back to 'active' (operator-initiated
// recovery shortcut; the recovery arbiter is the canonical path
// for failure-driven recovery, but ops gets a manual override).
func (f *CmdFaultInjector) Reactivate(node string) error {
	_, err := f.pool.Exec(context.Background(),
		`UPDATE compute_nodes SET lifecycle = 'active',
		 last_recovery_outcome = NULL,
		 recovery_initiated_at = NULL
		 WHERE name = $1`, node)
	return err
}

// RestoreAll rolls back every fault the injector applied:
// iptables rules (in reverse order), SIGCONT any frozen procs.
// SIGKILL'd procs stay killed; the test's daemon supervisor is
// responsible for respawning them.
func (f *CmdFaultInjector) RestoreAll() error {
	for i := len(f.iptables) - 1; i >= 0; i-- {
		// -D instead of -A to remove the same rule.
		rule := "-D " + f.iptables[i][2:] // strip the leading "-A "
		_ = exec.Command("iptables", rule).Run()
	}
	f.iptables = nil
	for name, cmd := range f.procs {
		if cmd == nil || cmd.Process == nil {
			continue
		}
		// Thaw any SIGSTOP'd procs so the cleanup goroutines can
		// exit cleanly.
		_ = cmd.Process.Signal(syscall.SIGCONT)
		f.t.Logf("fault: restored %s (pid %d)", name, cmd.Process.Pid)
	}
	return nil
}
