//go:build linux && amd64

package fcvm

// adr: 783
import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// Exercise the real helper negotiation and independent procfs check without
// creating a cgroup, running as root or starting a guest.
func TestNativeHelperSessionPolicyLinuxVerifiesOriginalRestrictedThreads(t *testing.T) {
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := newNativeHostHelperCommand(helper, []string{command})
	if err != nil {
		t.Fatal(err)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = append(os.Environ(), "GORACE=atexit_sleep_ms=0", "GREGALE_NATIVE_LAUNCH_FIXTURE=1", "GREGALE_NATIVE_REAL_SESSION_POLICY=1")
	gate, release, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	defer release.Close()
	ready, acknowledge, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer ready.Close()
	defer acknowledge.Close()
	cmd.ExtraFiles = []*os.File{gate, acknowledge}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	_ = gate.Close()
	_ = acknowledge.Close()
	if err := awaitNativeHelperSessionPolicy(t.Context(), ready); err != nil {
		t.Fatal(err)
	}
	start, err := nativeHostHelperStartTime(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	groups := nativeLinuxHostHelperGroups{}
	if err := groups.SessionRestricted(cmd.Process.Pid, start); err != nil {
		t.Fatal("acknowledged original threads lack kernel restriction", err)
	}
	if err := groups.SessionRestricted(cmd.Process.Pid, start+1); err == nil {
		t.Fatal("another incarnation borrowed original restriction")
	}
	if _, err := release.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal("restricted helper cannot execute native command", err)
	}
}
