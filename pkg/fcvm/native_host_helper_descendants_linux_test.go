//go:build linux

package fcvm

// adr: 654
import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// Real procfs/session diagnostics only; this neither creates cgroups nor runs a VM.
func TestNativeHelperFamilyLinuxObservesOrphanInOriginalPrivateSession(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 30 & echo $!; wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || childPID <= 0 {
		t.Fatal("missing owned child identity", line, err)
	}
	child, err := os.FindProcess(childPID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Kill(); _ = child.Release() })
	start, err := nativeHostHelperStartTime(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	session, err := (nativeLinuxHostHelperGroups{}).OriginalSession(cmd.Process.Pid, start)
	if err != nil || session != cmd.Process.Pid {
		t.Fatal("private original session missing", session, err)
	}
	if _, err := (nativeLinuxHostHelperGroups{}).OriginalSession(cmd.Process.Pid, start+1); err == nil {
		t.Fatal("replacement incarnation borrowed session")
	}
	if err := (nativeLinuxHostHelperGroups{}).SessionRestricted(cmd.Process.Pid, start); err == nil {
		t.Fatal("private session alone supplied a kernel restriction")
	}
	_, owner, _ := nativeHelperJournalFixture(t)
	frame := nativeHelperTaskRecord(owner, cmd.Process.Pid, start)
	frame.SessionID = session
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	frame.Launch.Revoked, frame.Launch.ExitConfirmed, frame.Launch.ResourcesRemoved = true, true, true
	tasks, err := (nativeProcessProbe{root: "/proc"}).helperTasks(t.Context(), []nativeHostHelperRecord{frame})
	if err != nil || len(tasks) != 1 || tasks[0].PID != childPID || tasks[0].ObservedUID != os.Geteuid() || tasks[0].LeaderPID != cmd.Process.Pid || tasks[0].SessionID != session || !tasks[0].RetirementClaimed {
		t.Fatal("orphan lost with leader and group", tasks, err)
	}
}
