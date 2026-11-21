package e2etest

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestFirstDeadDaemonDetectsUnreapedExit(t *testing.T) {
	cmd := exec.Command("sh", "-c", "echo 'image staging failed'; exit 7")
	cmd.Stdout = &safeBuffer{}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, output, dead := firstDeadDaemon([]*exec.Cmd{cmd})
		if dead {
			if !strings.Contains(output, "image staging failed") {
				t.Fatalf("lost exit diagnostic: %q", output)
			}
			// The liveness probe must not consume teardown's exit status.
			if err := cmd.Wait(); err == nil || cmd.ProcessState.ExitCode() != 7 {
				t.Fatalf("exit status consumed or changed: %v, %v", err, cmd.ProcessState)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("unreaped daemon exit went undetected")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
