//go:build linux

package main

// Interactive app tasks (ADR-958): one process attached to the host client's
// stdin, output, and window size, either through a pseudo-terminal or plain
// pipes. The process gets the same environment, identity, and loopback
// bindings as a batch task.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"golang.org/x/sys/unix"
)

const (
	appTaskPtsDir             = "/dev/pts"
	appTaskPtsMountOptions    = "newinstance,ptmxmode=0666,mode=0620"
	appTaskDefaultTerm        = "xterm-256color"
	appTaskOutputDrainWindow  = 200 * time.Millisecond
	appTaskTTYEndOfTransmit   = 0x04
	appTaskHangupGrace        = 2 * time.Second
	appTaskInteractiveExitErr = "command exited unsuccessfully"
)

func appTaskInteractiveHandler(log *slog.Logger) apptaskproto.InteractiveHandler {
	return func(ctx context.Context, session *apptaskproto.InteractiveSession) (apptaskproto.Result, error) {
		runtime, failure := loadAppTaskRuntime(log)
		if failure != nil {
			return *failure, nil
		}
		cmd, credential, failure := prepareAppTaskCommand(session.Request, runtime.manifest, runtime.secrets, runtime.apiEnv)
		if failure != nil {
			return *failure, nil
		}
		if session.Request.TTY {
			return runAppTaskTTY(ctx, session, cmd, credential, log)
		}
		return runAppTaskPipes(ctx, session, cmd, credential)
	}
}

func runAppTaskTTY(ctx context.Context, session *apptaskproto.InteractiveSession, cmd *exec.Cmd, credential *syscall.Credential, log *slog.Logger) (apptaskproto.Result, error) {
	master, slave, err := openAppTaskPTY(credential)
	if err != nil {
		log.Warn("app task terminal unavailable", "err", err)
		return appTaskInfraFailure("tty_unavailable", "a terminal could not be allocated", 126), nil
	}
	defer func() { _ = master.Close() }()
	if err := setAppTaskPTYSize(master, session.Request.Rows, session.Request.Cols); err != nil {
		_ = slave.Close()
		return appTaskInfraFailure("tty_unavailable", "the terminal size could not be set", 126), nil
	}
	cmd.Env = withDefaultTerm(cmd.Env)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	// The slave is the child's fd 0; Setsid + Setctty make it the controlling
	// terminal of a new session whose process group is the child's pid.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0, Credential: credential}
	startErr := cmd.Start()
	_ = slave.Close()
	if startErr != nil {
		return appTaskStartFailure(startErr), nil
	}

	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		// Reading the master returns EIO once every slave descriptor is
		// closed, which ends the copy.
		_, _ = io.Copy(session.Stdout, master)
	}()
	go applyAppTaskInput(session.Input, func(event apptaskproto.InputEvent) error {
		switch event.Kind {
		case apptaskproto.InputStdin:
			_, err := master.Write(event.Data)
			return err
		case apptaskproto.InputResize:
			return setAppTaskPTYSize(master, event.Rows, event.Cols)
		case apptaskproto.InputStdinClose:
			_, err := master.Write([]byte{appTaskTTYEndOfTransmit})
			return err
		}
		return nil
	})

	result, err := superviseAppTaskProcess(ctx, cmd, syscall.SIGHUP)
	timer := time.NewTimer(appTaskOutputDrainWindow)
	defer timer.Stop()
	select {
	case <-outputDone:
	case <-timer.C:
		// A background process still holds the terminal; stop forwarding.
		_ = master.Close()
		<-outputDone
	}
	return result, err
}

func runAppTaskPipes(ctx context.Context, session *apptaskproto.InteractiveSession, cmd *exec.Cmd, credential *syscall.Credential) (apptaskproto.Result, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return appTaskInfraFailure("command_start_failed", "command could not be started", 126), nil
	}
	cmd.Stdout = session.Stdout
	cmd.Stderr = session.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: credential}
	if err := cmd.Start(); err != nil {
		return appTaskStartFailure(err), nil
	}
	go applyAppTaskInput(session.Input, func(event apptaskproto.InputEvent) error {
		switch event.Kind {
		case apptaskproto.InputStdin:
			_, err := stdin.Write(event.Data)
			return err
		case apptaskproto.InputStdinClose:
			return stdin.Close()
		}
		return nil
	})
	return superviseAppTaskProcess(ctx, cmd, syscall.SIGTERM)
}

// applyAppTaskInput forwards host events until the host stops sending. A
// failed write means the process stopped reading; later events are dropped.
func applyAppTaskInput(input <-chan apptaskproto.InputEvent, apply func(apptaskproto.InputEvent) error) {
	failed := false
	for event := range input {
		if failed {
			continue
		}
		if err := apply(event); err != nil {
			failed = true
		}
	}
}

// superviseAppTaskProcess waits for the process. When ctx ends first (host
// hang-up or the session time limit), the whole process group receives
// hangup, then SIGKILL after a grace period.
func superviseAppTaskProcess(ctx context.Context, cmd *exec.Cmd, hangup syscall.Signal) (apptaskproto.Result, error) {
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	select {
	case waitErr := <-waitCh:
		result := appTaskResultFromWait(cmd, waitErr)
		if result.Status == apptaskproto.StatusFailed {
			result.FailureMessage = appTaskInteractiveExitErr
		}
		return result, nil
	case <-ctx.Done():
		pgid := cmd.Process.Pid
		_ = signalJobProcessGroup(pgid, hangup)
		timer := time.NewTimer(appTaskHangupGrace)
		defer timer.Stop()
		select {
		case <-waitCh:
		case <-timer.C:
			_ = signalJobProcessGroup(pgid, syscall.SIGKILL)
			<-waitCh
		}
		return apptaskproto.Result{}, ctx.Err()
	}
}

// openAppTaskPTY allocates a terminal pair from a private devpts instance.
// The slave is owned by the task identity so programs that reopen their
// terminal by name keep working after the credential drop.
func openAppTaskPTY(credential *syscall.Credential) (*os.File, *os.File, error) {
	if err := ensureAppTaskDevpts(); err != nil {
		return nil, nil, err
	}
	master, err := openAppTaskPTYMaster()
	if err != nil {
		return nil, nil, err
	}
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		_ = master.Close()
		return nil, nil, fmt.Errorf("unlock pty: %w", err)
	}
	index, err := unix.IoctlGetUint32(fd, unix.TIOCGPTN)
	if err != nil {
		_ = master.Close()
		return nil, nil, fmt.Errorf("pty index: %w", err)
	}
	name := fmt.Sprintf("%s/%d", appTaskPtsDir, index)
	if credential != nil {
		if err := os.Chown(name, int(credential.Uid), int(credential.Gid)); err != nil {
			_ = master.Close()
			return nil, nil, fmt.Errorf("chown pty: %w", err)
		}
	}
	//nolint:forbidigo // kernel-allocated pty slave named by TIOCGPTN, not a customer path
	slave, err := os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		return nil, nil, fmt.Errorf("open pty slave: %w", err)
	}
	return master, slave, nil
}

func openAppTaskPTYMaster() (*os.File, error) {
	var lastErr error
	for _, path := range []string{appTaskPtsDir + "/ptmx", "/dev/ptmx"} {
		//nolint:forbidigo // fixed kernel pty multiplexer path
		master, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
		if err == nil {
			return master, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("open pty master: %w", lastErr)
}

// ensureAppTaskDevpts mounts a private devpts instance on first use. Serving
// guests never mount one; only interactive TTY tasks need it.
func ensureAppTaskDevpts() error {
	if info, err := os.Stat(appTaskPtsDir + "/ptmx"); err == nil && info.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	if err := os.MkdirAll(appTaskPtsDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", appTaskPtsDir, err)
	}
	err := syscall.Mount("devpts", appTaskPtsDir, "devpts", syscall.MS_NOSUID|syscall.MS_NOEXEC, appTaskPtsMountOptions)
	if err != nil && !errors.Is(err, syscall.EBUSY) {
		return fmt.Errorf("mount devpts: %w", err)
	}
	return nil
}

func setAppTaskPTYSize(master *os.File, rows, cols uint16) error {
	return unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols})
}

func withDefaultTerm(env []string) []string {
	for _, item := range env {
		if strings.HasPrefix(item, "TERM=") {
			return env
		}
	}
	return append(env, "TERM="+appTaskDefaultTerm)
}
