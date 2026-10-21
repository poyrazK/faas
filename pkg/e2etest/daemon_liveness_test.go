package e2etest

// firstDeadDaemon turns a daemon that dies at boot into an immediate, named
// failure instead of an unrelated timeout minutes later.
//
// startProc only reports whether fork/exec succeeded; it deliberately never
// Waits (stop() owns the single Wait). imaged compounded that by having no
// listening socket, so unlike apid/schedd/vmmd it had no readiness gate at
// all. It exited at boot on the acceptance node —
//
//	imaged: sign key "/etc/faas/secrets/sign.key": ...
//
// and since nothing advances a deployment from `building` to `live` without
// imaged, six build subtests each burned a full 4-minute deployment deadline:
// roughly 24 minutes of a 30-minute phase, reported as a slow build.

import (
	"os/exec"
	"strings"
	"testing"
)

func TestFirstDeadDaemon_DetectsAnExitedProcess(t *testing.T) {
	// Stands in for imaged refusing to boot: exits immediately, non-empty
	// output. `sh -c` so we can emit the diagnostic the real daemon would.
	cmd := exec.Command("sh", "-c", "echo 'imaged: sign key missing'; exit 1")
	cmd.Stdout = &safeBuffer{}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatalf("start stub daemon: %v", err)
	}
	_ = cmd.Wait()

	name, out, dead := firstDeadDaemon([]*exec.Cmd{cmd})
	if !dead {
		t.Fatal("firstDeadDaemon accepted a process that had already exited; " +
			"the failure would resurface minutes later as an unrelated timeout")
	}
	if name == "" {
		t.Error("firstDeadDaemon did not name the daemon")
	}
	if !strings.Contains(out, "sign key missing") {
		t.Errorf("captured output lost the daemon's last words: %q", out)
	}
}

func TestFirstDeadDaemon_AcceptsARunningProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.Stdout = &safeBuffer{}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatalf("start stub daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	if name, _, dead := firstDeadDaemon([]*exec.Cmd{cmd}); dead {
		t.Errorf("firstDeadDaemon reported live process %q as dead", name)
	}
}

// A harness that started no daemons, or entries without a Process, must not
// trip the check — Start is called with many different daemon subsets.
func TestFirstDeadDaemon_ToleratesEmptyAndUnstarted(t *testing.T) {
	for _, tc := range []struct {
		name  string
		procs []*exec.Cmd
	}{
		{name: "no daemons", procs: nil},
		{name: "nil entry", procs: []*exec.Cmd{nil}},
		{name: "never started", procs: []*exec.Cmd{exec.Command("true")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, dead := firstDeadDaemon(tc.procs); dead {
				t.Error("firstDeadDaemon reported a failure for a harness with nothing running")
			}
		})
	}
}

// A daemon stopped on purpose by KillAPID or KillSchedd is not a crash: the
// test restarts the other daemon before its own, and that restart's health
// check must not fail on the deliberate kill. A crash is still reported.
func TestRequireDaemonsAliveSkipsDeliberateKills(t *testing.T) {
	exited := func(script string) *exec.Cmd {
		cmd := exec.Command("sh", "-c", script)
		cmd.Stdout = &safeBuffer{}
		cmd.Stderr = cmd.Stdout
		if err := cmd.Start(); err != nil {
			t.Fatalf("start stub daemon: %v", err)
		}
		_ = cmd.Wait()
		return cmd
	}
	killed := exited("exit 137")
	h := &Harness{procs: []*exec.Cmd{killed}}
	h.markKilled(killed)
	h.requireDaemonsAlive(t) // must not fail

	crashed := exited("echo 'schedd: boot failed'; exit 1")
	h.procs = append(h.procs, crashed)
	live := []*exec.Cmd{}
	for _, p := range h.procs {
		if !h.killed[p] {
			live = append(live, p)
		}
	}
	if _, out, dead := firstDeadDaemon(live); !dead || !strings.Contains(out, "boot failed") {
		t.Fatalf("a crashed daemon beside a deliberate kill went unreported: %q %v", out, dead)
	}
}
