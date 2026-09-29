//go:build linux

package vmmdgrpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func waitForwardProcessStopped(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if os.IsNotExist(err) {
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
