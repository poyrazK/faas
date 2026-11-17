//go:build linux

package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

// appTaskDebugTools is the detached, read-only clone of the runtime base's
// debug tools, taken by PID 1 before the pivot (the base is unreachable
// after it). -1 when the base has no tools or the VM is not an app-task VM.
// Boot wiring: written by PID 1 before any session starts.
var appTaskDebugTools = -1

// captureAppTaskDebugTools clones appTaskDebugBaseToolsDir from the runtime
// base as a detached mount, read-only, nosuid and nodev. Nothing is mounted
// into the app's root. A base without the tools leaves debug sessions
// unavailable; it is never a boot failure.
func captureAppTaskDebugTools(log *slog.Logger) {
	if _, err := os.Stat(appTaskDebugBaseToolsDir + "/busybox"); err != nil {
		return
	}
	fd, err := unix.OpenTree(unix.AT_FDCWD, appTaskDebugBaseToolsDir, unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC|unix.AT_RECURSIVE)
	if err != nil {
		log.Warn("debug tools unavailable: clone failed", "err", err)
		return
	}
	attr := unix.MountAttr{Attr_set: unix.MOUNT_ATTR_RDONLY | unix.MOUNT_ATTR_NOSUID | unix.MOUNT_ATTR_NODEV}
	if err := unix.MountSetattr(fd, "", unix.AT_EMPTY_PATH|unix.AT_RECURSIVE, &attr); err != nil {
		_ = unix.Close(fd)
		log.Warn("debug tools unavailable: read-only attributes failed", "err", err)
		return
	}
	appTaskDebugTools = fd
}

// releaseAppTaskDebugTools drops the clone in VMs that never run sessions.
func releaseAppTaskDebugTools() {
	if appTaskDebugTools >= 0 {
		_ = unix.Close(appTaskDebugTools)
		appTaskDebugTools = -1
	}
}

// prepareAppTaskDebugCommand builds the debug helper for a session whose
// command is api.AppTaskDebugCommand followed by the user's command. It
// carries the same environment, working directory and identity as any
// session command; the helper starts as root in a new mount namespace
// (appTaskProcAttr) and drops to the identity after attaching the tools.
func prepareAppTaskDebugCommand(req apptaskproto.Request, runtime appTaskRuntime) (*exec.Cmd, *syscall.Credential, *apptaskproto.Result) {
	if appTaskDebugTools < 0 {
		failure := appTaskInfraFailure("debug_tools_unavailable", "this deployment's runtime base has no debug tools; redeploy to get them", 126)
		return nil, nil, &failure
	}
	inner := req
	inner.Command = append([]string(nil), req.Command[1:]...)
	inner.CommandShell = false
	cmd, credential, failure := prepareAppTaskCommand(inner, runtime.manifest, runtime.secrets, runtime.apiEnv)
	if failure != nil {
		return nil, nil, failure
	}
	dup, err := unix.FcntlInt(uintptr(appTaskDebugTools), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		failure := appTaskInfraFailure("debug_tools_unavailable", "the debug tools could not be opened", 126)
		return nil, nil, &failure
	}
	id := appTaskDebugIdentity{}
	if credential != nil {
		id.UID, id.GID = credential.Uid, credential.Gid
		for _, g := range credential.Groups {
			id.Groups = append(id.Groups, int(g))
		}
	}
	helper := &exec.Cmd{
		// /proc/self/exe stays executable after pivot_root.
		Path:        "/proc/self/exe",
		Args:        encodeAppTaskDebugHelperArgs(id, inner.Command),
		Env:         appendAppTaskDebugPath(cmd.Env),
		Dir:         cmd.Dir,
		ExtraFiles:  []*os.File{os.NewFile(uintptr(dup), "gregale-debug-tools")},
		SysProcAttr: &syscall.SysProcAttr{Unshareflags: syscall.CLONE_NEWNS},
	}
	return helper, credential, nil
}

// appTaskProcAttr completes a session process's attributes. A debug helper
// (CLONE_NEWNS set by prepareAppTaskDebugCommand) keeps its own mount
// namespace and starts as root: it drops to credential itself once the
// tools are attached. Every other command starts as credential.
func appTaskProcAttr(cmd *exec.Cmd, attr syscall.SysProcAttr, credential *syscall.Credential) *syscall.SysProcAttr {
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Unshareflags&syscall.CLONE_NEWNS != 0 {
		attr.Unshareflags = cmd.SysProcAttr.Unshareflags
		return &attr
	}
	attr.Credential = credential
	return &attr
}

// runAppTaskDebugHelper runs in the re-executed child. Go has already
// unshared the mount namespace and made "/" recursively private, so the
// attach below is invisible to every other process.
func runAppTaskDebugHelper(args []string) int {
	id, command, err := decodeAppTaskDebugHelperArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregale debug:", err)
		return 126
	}
	if err := os.MkdirAll(api.AppTaskDebugToolsDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "gregale debug: tools mountpoint:", err)
		return 126
	}
	if err := unix.MoveMount(appTaskDebugToolsFD, "", unix.AT_FDCWD, api.AppTaskDebugToolsDir, unix.MOVE_MOUNT_F_EMPTY_PATH); err != nil {
		fmt.Fprintln(os.Stderr, "gregale debug: attach tools:", err)
		return 126
	}
	_ = unix.Close(appTaskDebugToolsFD)
	// syscall's Setgroups/Setgid/Setuid apply to every thread of the process.
	if err := syscall.Setgroups(id.Groups); err != nil {
		fmt.Fprintln(os.Stderr, "gregale debug: groups:", err)
		return 126
	}
	if err := syscall.Setgid(int(id.GID)); err != nil {
		fmt.Fprintln(os.Stderr, "gregale debug: gid:", err)
		return 126
	}
	if err := syscall.Setuid(int(id.UID)); err != nil {
		fmt.Fprintln(os.Stderr, "gregale debug: uid:", err)
		return 126
	}
	path, err := exec.LookPath(command[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregale debug: %s: command not found\n", command[0])
		return 127
	}
	err = syscall.Exec(path, command, os.Environ())
	fmt.Fprintln(os.Stderr, "gregale debug: exec:", err)
	return 126
}
