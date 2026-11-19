//go:build metal

package fcvm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
)

// TestMetalAppTaskDebugSession is the metal gate for `gregale app exec
// --debug` (ADR-20261117070620). It boots real task VMs from a copy of the
// host's runtime base that carries this tree's guest-init and the debug tools
// (FAAS_TEST_GUEST_INIT, FAAS_TEST_DEBUG_BUSYBOX), so no shared host base is
// touched, and checks that:
//   - a debug session runs busybox as the deployment user, with the tools
//     appended to PATH, mounted read-only at /.gregale/debug only in the
//     session's own mount namespace (guest-init's PID 1 does not see them);
//   - a plain session on the same base cannot reach the tools;
//   - every VM, netns and cgroup is gone afterwards.
func TestMetalAppTaskDebugSession(t *testing.T) {
	kernel, base, layer := metalImages(t)
	guestInit, busybox := os.Getenv("FAAS_TEST_GUEST_INIT"), os.Getenv("FAAS_TEST_DEBUG_BUSYBOX")
	if guestInit == "" || busybox == "" {
		t.Skip("set FAAS_TEST_GUEST_INIT (this tree's guest-init) and FAAS_TEST_DEBUG_BUSYBOX (a static busybox)")
	}
	debugBase := buildDebugToolsBase(t, base, guestInit, busybox)
	m := newMetalManager(t, kernel)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	t.Run("debug_session", func(t *testing.T) {
		script := `echo "uid=$(id -u)"; echo "path=$PATH"; ` +
			`echo "self_mounts=$(grep -c ' /.gregale/debug ' /proc/self/mountinfo)"; ` +
			`echo "pid1_mounts=$(grep -c ' /.gregale/debug ' /proc/1/mountinfo 2>/dev/null || echo unreadable)"; ` +
			`if touch /.gregale/debug/probe 2>/dev/null; then echo tools=writable; else echo tools=readonly; fi; ` +
			`ls /.gregale/debug/bin/ls >/dev/null && echo applets=ok`
		res, out := runMetalAppTaskSession(t, m, "dbg-session", debugBase, layer,
			[]string{api.AppTaskDebugCommand, "sh", "-c", script})
		t.Logf("debug session output:\n%s", out)
		if res.ExitCode == nil || *res.ExitCode != 0 {
			t.Fatalf("debug session result = %+v", res)
		}
		for _, want := range []string{":/.gregale/debug/bin\n", "self_mounts=1\n", "tools=readonly\n", "applets=ok\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("debug output lacks %q", want)
			}
		}
		if strings.Contains(out, "uid=0\n") {
			t.Errorf("debug session ran as root; want the deployment user")
		}
		if !strings.Contains(out, "pid1_mounts=0\n") && !strings.Contains(out, "pid1_mounts=unreadable\n") {
			t.Errorf("guest-init's mount namespace sees the debug tools")
		}
	})

	t.Run("plain_session_cannot_reach_tools", func(t *testing.T) {
		res, out := runMetalAppTaskSession(t, m, "dbg-plain", debugBase, layer,
			[]string{"/.gregale/debug/bin/sh", "-c", "echo leaked"})
		if strings.Contains(out, "leaked") || res.ExitCode == nil || *res.ExitCode != 127 {
			t.Fatalf("plain session reached the tools: result=%+v output=%q", res, out)
		}
	})

	if m.LiveCount() != 0 {
		t.Fatalf("live=%d after sessions, want 0", m.LiveCount())
	}
	leakcheck.AssertZero(t)
}

// runMetalAppTaskSession boots one task VM and runs one non-tty interactive
// session with stdin closed. The manager tears the VM down afterwards.
func runMetalAppTaskSession(t *testing.T, m *Manager, instance, base, layer string, command []string) (apptaskproto.Result, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_, err := m.WakeAppTask(ctx, AppTaskWakeRequest{WakeRequest: WakeRequest{
		Instance: instance, AccountID: "acct-debug-metal", AppID: "app-debug-metal",
		DeploymentID: "00000000-0000-4000-8000-0000000000d1", Plan: api.PlanPro,
		BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 256, CPUMillicores: 1000,
	}})
	if err != nil {
		t.Fatalf("wake task VM %s: %v", instance, err)
	}
	input := make(chan apptaskproto.InputEvent, 1)
	input <- apptaskproto.InputEvent{Kind: apptaskproto.InputStdinClose}
	close(input)
	var mu sync.Mutex
	var out bytes.Buffer
	res, err := m.ExecuteInteractiveAppTask(ctx, instance, apptaskproto.Request{
		Version: apptaskproto.InteractiveVersion, TaskID: instance, Command: command,
		TimeoutSeconds: 60, Interactive: true,
	}, input, func(_ context.Context, _ string, chunk []byte) error {
		mu.Lock()
		defer mu.Unlock()
		out.Write(chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("session %s: %v", instance, err)
	}
	mu.Lock()
	defer mu.Unlock()
	return res, out.String()
}

// buildDebugToolsBase copies the runtime base and, with debugfs, installs
// guestInit as /sbin/init and the debug tools exactly as the runtime base
// Dockerfiles do: /usr/lib/gregale/debug/busybox (root:root 0555) and
// bin/<applet> links to it.
func buildDebugToolsBase(t *testing.T, base, guestInit, busybox string) string {
	t.Helper()
	dir := t.TempDir()
	dst := filepath.Join(dir, "debug-base.ext4")
	mustRun(t, "cp", "--reflink=auto", base, dst)
	mustRun(t, "truncate", "-s", "+64M", dst)
	// e2fsck exits 1 when it fixed something; resize2fs refuses a dirty fs.
	_ = exec.Command("e2fsck", "-fy", dst).Run()
	mustRun(t, "resize2fs", dst)
	applets := []string{"sh", "id", "ls", "grep", "touch", "echo", "cat"}
	var cmds strings.Builder
	cmds.WriteString("rm /sbin/init\n")
	fmt.Fprintf(&cmds, "write %s /sbin/init\n", guestInit)
	cmds.WriteString("sif /sbin/init mode 0100755\nsif /sbin/init uid 0\nsif /sbin/init gid 0\n")
	for _, d := range []string{"/usr", "/usr/lib", "/usr/lib/gregale", "/usr/lib/gregale/debug", "/usr/lib/gregale/debug/bin"} {
		fmt.Fprintf(&cmds, "mkdir %s\n", d)
	}
	fmt.Fprintf(&cmds, "write %s /usr/lib/gregale/debug/busybox\n", busybox)
	cmds.WriteString("sif /usr/lib/gregale/debug/busybox mode 0100555\n")
	cmds.WriteString("sif /usr/lib/gregale/debug/busybox uid 0\nsif /usr/lib/gregale/debug/busybox gid 0\n")
	for _, a := range applets {
		fmt.Fprintf(&cmds, "symlink /usr/lib/gregale/debug/bin/%s ../busybox\n", a)
	}
	script := filepath.Join(dir, "debugfs.cmds")
	if err := os.WriteFile(script, []byte(cmds.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	// debugfs reports a failed command (an existing /usr, say) and goes on;
	// the checks below catch anything that matters.
	if out, err := exec.Command("debugfs", "-w", "-f", script, dst).CombinedOutput(); err != nil {
		t.Fatalf("debugfs: %v\n%s", err, out)
	}
	for _, path := range []string{"/sbin/init", "/usr/lib/gregale/debug/busybox", "/usr/lib/gregale/debug/bin/sh"} {
		out, err := exec.Command("debugfs", "-R", "stat "+path, dst).CombinedOutput()
		if err != nil || bytes.Contains(out, []byte("File not found")) {
			t.Fatalf("debug base lacks %s: %v\n%s", path, err, out)
		}
	}
	return dst
}

func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
