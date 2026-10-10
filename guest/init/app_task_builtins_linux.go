//go:build linux

package main

// Interactive session built-ins (ADR-958): copy-out and port-forward run
// without a tty over the session's stdin/stdout.

import (
	"context"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/apptaskmux"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

const appTaskPortForwardDialTimeout = 10 * time.Second

// runAppTaskCopyOut re-executes guest-init as the app user to tar source to
// stdout, so the copy can read exactly what the app user can.
func runAppTaskCopyOut(ctx context.Context, session *apptaskproto.InteractiveSession, runtime appTaskRuntime, source string) (apptaskproto.Result, error) {
	if !filepath.IsAbs(source) {
		source = filepath.Join(runtime.manifest.EffectiveWorkingDir(), source)
	}
	credential, err := processCredential("", runtime.manifest.EffectiveUser())
	if err != nil {
		return appTaskInfraFailure("command_identity_invalid", "command identity could not be resolved", 126), nil //nolint:nilerr // identity errors are terminal protocol results
	}
	cmd := &exec.Cmd{
		// /proc/self/exe stays executable after pivot_root even though the
		// original guest-init path is gone.
		Path:   "/proc/self/exe",
		Args:   []string{appTaskCopyOutHelperArg0, source},
		Env:    []string{"PATH=/usr/bin:/bin"},
		Dir:    "/",
		Stdout: session.Stdout,
		Stderr: session.Stderr,
		SysProcAttr: &syscall.SysProcAttr{
			Setpgid: true, Credential: execProcessCredential(credential),
		},
	}
	if err := cmd.Start(); err != nil {
		return appTaskInfraFailure("copy_unavailable", "the copy helper could not be started", 126), nil //nolint:nilerr // start errors are terminal protocol results
	}
	go applyAppTaskInput(session.Input, func(apptaskproto.InputEvent) error { return nil })
	return superviseAppTaskProcess(ctx, cmd, syscall.SIGTERM)
}

// runAppTaskPortForward relays apptaskmux streams from stdin to target,
// dialed from inside the app's network, until the client closes stdin.
func runAppTaskPortForward(ctx context.Context, session *apptaskproto.InteractiveSession, target string) (apptaskproto.Result, error) {
	frames, frameWriter := io.Pipe()
	go func() {
		defer func() { _ = frameWriter.Close() }()
		for event := range session.Input {
			switch event.Kind {
			case apptaskproto.InputStdin:
				if _, err := frameWriter.Write(event.Data); err != nil {
					return
				}
			case apptaskproto.InputStdinClose:
				return
			}
		}
	}()
	dialer := &net.Dialer{Timeout: appTaskPortForwardDialTimeout}
	err := apptaskmux.Serve(ctx, frames, session.Stdout, func(ctx context.Context) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp", target)
	})
	_ = frames.Close()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return apptaskproto.Result{}, ctxErr
	}
	if err != nil {
		return appTaskInfraFailure("port_forward_failed", "the forwarding stream was malformed", 1), nil //nolint:nilerr // relay errors are terminal protocol results
	}
	exit := 0
	return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &exit}, nil
}
