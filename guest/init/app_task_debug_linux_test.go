//go:build linux

package main

import (
	"os/exec"
	"syscall"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

// A debug helper starts as root in its own mount namespace and drops to the
// deployment user itself; every other session command starts as that user.
func TestAppTaskProcAttrDebugHelperStartsAsRootInOwnNamespace(t *testing.T) {
	credential := &syscall.Credential{Uid: 1000, Gid: 1000}
	plain := appTaskProcAttr(&exec.Cmd{}, syscall.SysProcAttr{Setpgid: true}, credential)
	if plain.Credential != credential || plain.Unshareflags != 0 || !plain.Setpgid {
		t.Fatalf("plain command attrs = %+v", plain)
	}
	helper := &exec.Cmd{SysProcAttr: &syscall.SysProcAttr{Unshareflags: syscall.CLONE_NEWNS}}
	debug := appTaskProcAttr(helper, syscall.SysProcAttr{Setsid: true, Setctty: true}, credential)
	if debug.Credential != nil || debug.Unshareflags&syscall.CLONE_NEWNS == 0 || !debug.Setsid || !debug.Setctty {
		t.Fatalf("debug helper attrs = %+v", debug)
	}
}

// Without tools cloned from the base (an older base, or none), a debug
// session ends with a stable code the CLI turns into a redeploy hint.
func TestPrepareAppTaskDebugCommandWithoutTools(t *testing.T) {
	saved := appTaskDebugTools
	appTaskDebugTools = -1
	t.Cleanup(func() { appTaskDebugTools = saved })
	_, _, failure := prepareAppTaskDebugCommand(apptaskproto.Request{Command: []string{api.AppTaskDebugCommand, "sh"}}, appTaskRuntime{})
	if failure == nil || failure.FailureCode != "debug_tools_unavailable" {
		t.Fatalf("failure = %+v", failure)
	}
}
