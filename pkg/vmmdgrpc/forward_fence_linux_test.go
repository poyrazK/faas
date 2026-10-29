//go:build linux

package vmmdgrpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A process may exit between opening its procfs file and reading it. Linux
// returns ESRCH for that read, distinct from ENOENT when opening a missing path.
func forwardProcessNoLongerExists(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, syscall.ESRCH)
}

func waitForwardProcessStopped(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if forwardProcessNoLongerExists(err) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		// Init may have not yet reaped an orphan zombie. It has no open
		// sockets or running threads and therefore cannot keep forwarding.
		if end := strings.LastIndexByte(string(stat), ')'); end >= 0 && strings.HasPrefix(string(stat[end+1:]), " Z ") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("bridge process %d still runs after its owner stopped", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestForwardProcessExitDuringProcRead(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	//nolint:forbidigo // Kernel procfs path uses this test's own child PID; retain its FD across exit.
	stat, err := os.Open(fmt.Sprintf("/proc/%d/stat", cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stat.Close() }()
	if _, err := io.ReadAll(stat); err != nil {
		t.Fatal(err)
	}
	if _, err := stat.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	// Keep the original file open across exit to exercise the kernel read race
	// deterministically; reopening would report ENOENT instead.
	_, err = io.ReadAll(stat)
	if !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("read exited process through open procfs file = %v, want ESRCH", err)
	}
	if !forwardProcessNoLongerExists(err) {
		t.Fatalf("exited process incorrectly treated as a cleanup failure: %v", err)
	}
	if forwardProcessNoLongerExists(&os.PathError{Op: "read", Path: stat.Name(), Err: syscall.EACCES}) {
		t.Fatal("permission failure incorrectly treated as process exit")
	}
}

func TestForwardCompatibilityCancellationKillsBodyCopyChildren(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", "sleep 60 & child=$!; echo $child; wait")
	configureForwardCommand(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = cmd.Wait()
	waitForwardProcessStopped(t, pid)
}

func TestForwardBridgePermitOwnerProcess(t *testing.T) {
	if os.Getenv("GREGALE_FORWARD_PARENTDEATH_HELPER") == "" {
		t.Skip("subprocess helper")
	}
	child := exec.CommandContext(context.Background(), "sleep", "60")
	configureForwardCommand(child)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(child.Process.Pid); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
}

func TestForwardBridgeParentDeathFence(t *testing.T) {
	owner := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestForwardBridgePermitOwnerProcess$")
	owner.Env = append(os.Environ(), "GREGALE_FORWARD_PARENTDEATH_HELPER=1")
	out, err := owner.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	owner.Stderr = os.Stderr
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := json.NewDecoder(out).Decode(&pid); err != nil {
		t.Fatal(err)
	}
	_ = owner.Process.Kill()
	_ = owner.Wait()
	waitForwardProcessStopped(t, pid)
}
