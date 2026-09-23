//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"golang.org/x/sys/unix"
)

const (
	VsockAppTaskPort            uint32 = apptaskproto.VsockPort
	VsockAppTaskBindCID                = 0xffffffff
	AppTaskManifestPath                = "/etc/faas/app-task.json"
	appTaskManifestRelativePath        = "etc/faas/app-task.json"
	appTaskManifestKind                = "app_task"
	appTaskManifestVersion             = 1
	appTaskTerminationGrace            = 2 * time.Second
)

type appTaskManifest struct {
	Kind    string `json:"kind"`
	Version int    `json:"version"`
}

func validateAppTaskManifest(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var marker appTaskManifest
	if err := decoder.Decode(&marker); err != nil {
		return err
	}
	if marker.Kind != appTaskManifestKind || marker.Version != appTaskManifestVersion {
		return fmt.Errorf("kind=%q version=%d", marker.Kind, marker.Version)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing data")
		}
		return err
	}
	return nil
}

func listenAppTaskHook() (net.Listener, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("app task vsock socket: %w", err)
	}
	addr := &unix.SockaddrVM{CID: VsockAppTaskBindCID, Port: VsockAppTaskPort}
	if err := unix.Bind(fd, addr); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("app task vsock bind port %d: %w", VsockAppTaskPort, err)
	}
	if err := unix.Listen(fd, 1); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("app task vsock listen: %w", err)
	}
	f := os.NewFile(uintptr(fd), "app-task-vsock")
	ln, err := net.FileListener(f)
	_ = f.Close()
	if err != nil {
		return nil, fmt.Errorf("app task vsock listener: %w", err)
	}
	return ln, nil
}

func serveAppTaskOnce(ctx context.Context, ln net.Listener, handler apptaskproto.Handler) error {
	if ln == nil || handler == nil {
		return errors.New("app task listener is not configured")
	}
	conn, err := ln.Accept()
	if err != nil {
		return fmt.Errorf("app task vsock accept: %w", err)
	}
	defer func() { _ = conn.Close() }()
	return apptaskproto.Serve(ctx, conn, handler)
}

var poweroffAppTask = func() error {
	unix.Sync()
	return unix.Reboot(unix.LINUX_REBOOT_CMD_POWER_OFF)
}

func runAppTaskGuest(log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	// One-off commands receive the same loopback platform bindings as the app
	// process. Both proxies fail soft, matching the ordinary app boot contract.
	if err := startWorkloadIdentityProxy(log); err != nil {
		log.Warn("app task workload identity proxy unavailable", "err", err)
	}
	if err := startEventPublishProxy(log); err != nil {
		log.Warn("app task event publish proxy unavailable", "err", err)
	}
	ln, err := listenAppTaskHook()
	if err != nil {
		_ = poweroffAppTask()
		return err
	}
	defer func() { _ = ln.Close() }()
	serveErr := serveAppTaskOnce(context.Background(), ln, appTaskHandler(log))
	if serveErr != nil {
		log.Warn("app task guest exchange failed", "err", serveErr)
	}
	if powerErr := poweroffAppTask(); powerErr != nil {
		if serveErr != nil {
			return errors.Join(serveErr, fmt.Errorf("app task guest poweroff: %w", powerErr))
		}
		return fmt.Errorf("app task guest poweroff: %w", powerErr)
	}
	return serveErr
}

func appTaskHandler(log *slog.Logger) apptaskproto.Handler {
	return func(ctx context.Context, req apptaskproto.Request, stdout, stderr *apptaskproto.OutputWriter) (apptaskproto.Result, error) {
		//nolint:forbidigo // platform-owned app manifest, staged in the immutable deployment image
		f, err := os.Open(api.AppManifestPath)
		if err != nil {
			return appTaskInfraFailure("manifest_unavailable", "app manifest could not be loaded", 126), nil
		}
		manifest, manifestErr := api.ReadManifest(f)
		_ = f.Close()
		if manifestErr != nil {
			return appTaskInfraFailure("manifest_invalid", "app manifest is invalid", 126), nil
		}
		secrets, err := loadSecrets(log)
		if err != nil {
			return appTaskInfraFailure("secrets_unavailable", "scoped secrets could not be loaded", 126), nil
		}
		apiEnv, err := loadAPIEnv(log)
		if err != nil {
			return appTaskInfraFailure("environment_unavailable", "scoped environment could not be loaded", 126), nil
		}
		return executeAppTaskCommand(ctx, req, manifest, secrets, apiEnv, stdout, stderr)
	}
}

func executeAppTaskCommand(ctx context.Context, req apptaskproto.Request, manifest api.AppManifest, secrets, apiEnv map[string]string, stdout, stderr io.Writer) (apptaskproto.Result, error) {
	argv := append([]string(nil), req.Command...)
	env := BuildEnvWithSecrets(os.Environ(), manifest, secrets, apiEnv)
	env = StampWorkloadIdentityEnv(env)
	env = StampEventPublishEnv(env)
	env = StampRuntimeConfigEnv(env)
	if req.CommandShell {
		argv = []string{"/bin/sh", "-lc", argv[0]}
	} else {
		argv[0] = resolveWorkloadCommandPath("/", argv[0], env)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = manifest.EffectiveWorkingDir()
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if uid := lookupUID(manifest.EffectiveUser()); uid > 0 {
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(uid)}
	}
	if err := cmd.Start(); err != nil {
		exitCode := 126
		failureCode := "command_start_failed"
		failureMessage := "command could not be started"
		if errors.Is(err, os.ErrNotExist) {
			exitCode = 127
			failureCode = "command_not_found"
			failureMessage = "command was not found"
		}
		return appTaskInfraFailure(failureCode, failureMessage, exitCode), nil
	}

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	select {
	case waitErr := <-waitCh:
		return appTaskResultFromWait(cmd, waitErr), nil
	case <-ctx.Done():
		_ = signalJobProcessGroup(cmd.Process.Pid, syscall.SIGTERM)
		timer := time.NewTimer(appTaskTerminationGrace)
		defer timer.Stop()
		select {
		case <-waitCh:
		case <-timer.C:
			_ = signalJobProcessGroup(cmd.Process.Pid, syscall.SIGKILL)
			<-waitCh
		}
		return apptaskproto.Result{}, ctx.Err()
	}
}

func appTaskResultFromWait(cmd *exec.Cmd, waitErr error) apptaskproto.Result {
	exitCode := 0
	if cmd != nil && cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			exitCode = 128 + int(status.Signal())
		}
	}
	if waitErr == nil && exitCode == 0 {
		return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &exitCode}
	}
	return apptaskproto.Result{
		Status: apptaskproto.StatusFailed, ExitCode: &exitCode,
		FailureCode: "command_failed", FailureMessage: "command exited unsuccessfully",
	}
}

func appTaskInfraFailure(code, message string, exitCode int) apptaskproto.Result {
	return apptaskproto.Result{
		Status: apptaskproto.StatusFailed, ExitCode: &exitCode,
		FailureCode: code, FailureMessage: message,
	}
}
