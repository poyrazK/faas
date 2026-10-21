//go:build unix

package wire

import (
	"bytes"
	"fmt"
	"os"
	"strconv"

	"github.com/onebox-faas/faas/pkg/daemonunit"
)

// handoffSpawnEnv marks the short-lived intermediate that starts a
// hand-off's successor.
const handoffSpawnEnv = "FAAS_HANDOFF_SPAWN"

// The successor must be systemd's child, not the old generation's. systemd
// treats a main process it did not start as alien: on stop it sends SIGTERM
// and then SIGKILL at once, without waiting for a drain, and it cannot learn
// the process's exit status for Restart= (measured on systemd 255; see
// docs/adr/1736-zero-downtime-daemon-restarts.md). So the old generation
// starts an intermediate copy of the binary, which starts the successor,
// reports its PID on the ready pipe and exits. The orphaned successor is
// re-parented to PID 1 before it reports ready and is handed the unit.
//
// This runs in package initialization, before the packages that import
// pkg/wire initialize, so the intermediate does no daemon work.
func init() {
	if os.Getenv(handoffSpawnEnv) != "1" {
		return
	}
	if err := spawnSuccessor(); err != nil {
		fmt.Fprintln(os.Stderr, "hand-off intermediate:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func spawnSuccessor() error {
	_ = os.Unsetenv(handoffSpawnEnv)
	sockets, err := strconv.Atoi(os.Getenv(handoffFDsEnv))
	if err != nil || sockets < 0 {
		return fmt.Errorf("invalid %s", handoffFDsEnv)
	}
	readyFD, err := strconv.Atoi(os.Getenv(daemonunit.HandoffReadyFDEnv))
	if err != nil || readyFD != activationFirstFD+sockets {
		return fmt.Errorf("invalid %s", daemonunit.HandoffReadyFDEnv)
	}
	// A checking predecessor's connections and go pipe follow the ready
	// pipe (handoff_check.go).
	lastFD := readyFD
	if value := os.Getenv(daemonunit.HandoffGoFDEnv); value != "" {
		goFD, err := strconv.Atoi(value)
		probes, perr := loadHandoffProbes(os.LookupEnv)
		if err != nil || perr != nil || goFD != readyFD+len(probes)+1 {
			return fmt.Errorf("invalid %s or %s", daemonunit.HandoffGoFDEnv, handoffCheckFDsEnv)
		}
		lastFD = goFD
	}
	files := []*os.File{os.Stdin, os.Stdout, os.Stderr}
	for fd := activationFirstFD; fd <= lastFD; fd++ {
		files = append(files, os.NewFile(uintptr(fd), "handoff"))
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	p, err := os.StartProcess(exe, os.Args, &os.ProcAttr{Env: os.Environ(), Files: files})
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(files[3+readyFD-activationFirstFD], "%s %d\n", handoffPIDLine, p.Pid); err != nil {
		return err
	}
	return p.Release()
}

// handoffPIDLine prefixes the line that reports the successor's PID.
const handoffPIDLine = "pid"

// sameCgroup reports whether pid runs in this process's cgroup, which on a
// service host means the same unit. Without /proc it reports false.
func sameCgroup(pid int) bool {
	own, err := os.ReadFile("/proc/self/cgroup")
	if err != nil || len(own) == 0 {
		return false
	}
	other, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	return err == nil && bytes.Equal(own, other)
}
