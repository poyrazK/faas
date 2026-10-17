//go:build linux

package cirunner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

// RunGuest is the sole entrypoint that constructs an OS executor. It requires
// the native Jobs marker and its exact immutable CI command before any shell.
func RunGuest(ctx context.Context, args []string) (int, error) {
	if len(args) != 2 || args[0] != ci.JobsRunnerProtocol || os.Getenv("FAAS_JOB") != "1" || os.Getenv("FAAS_RUNTIME_KIND") != "job" {
		return 125, errors.New("CI runner requires a native Jobs guest")
	}
	manifest, err := ci.DecodeJobsManifest(args[1])
	if err != nil {
		return 125, err
	}
	file, err := os.Open("/etc/faas/job.json") //nolint:forbidigo // Fixed native guest marker; no customer-selected host path is opened.
	if err != nil {
		return 125, errors.New("CI guest marker unavailable")
	}
	defer func() { _ = file.Close() }()
	var marker struct {
		Kind      string   `json:"kind"`
		AccountID string   `json:"account_id"`
		RunID     string   `json:"run_id"`
		Command   []string `json:"command"`
	}
	if json.NewDecoder(io.LimitReader(file, api.CIRequestMaxBytes)).Decode(&marker) != nil || marker.Kind != "job" ||
		!sameGuestID(marker.AccountID, manifest.AccountID) || !sameGuestID(marker.RunID, manifest.AttemptID) || len(marker.Command) != 3 ||
		marker.Command[0] != ci.JobsRunnerPath || marker.Command[1] != args[0] || marker.Command[2] != args[1] {
		return 125, errors.New("CI guest marker does not match command")
	}
	proof := ci.RuntimeProof{RunID: manifest.AttemptID, InstanceID: os.Getenv("GREGALE_CI_INSTANCE_ID"), Capability: os.Getenv("GREGALE_CI_CAPABILITY")}
	transport, err := NewTransport(os.Getenv("GREGALE_CI_RUNTIME_URL"), proof, nil)
	if err != nil {
		return 125, err
	}
	result, err := (Runner{Manifest: manifest, Transport: transport, Executor: guestExecutor{}}).Run(ctx)
	if err != nil || result.ExitCode == nil {
		return 125, errTransfer
	}
	return *result.ExitCode, nil
}

func sameGuestID(a, b string) bool {
	left, err := uuid.Parse(a)
	if err != nil {
		return false
	}
	right, err := uuid.Parse(b)
	return err == nil && left == right
}

type guestExecutor struct{}

func (guestExecutor) Run(ctx context.Context, launch StepExecution, logs io.Writer) (int, error) {
	if ctx.Err() != nil {
		return 143, ctx.Err()
	}
	command := guestCommand(launch, logs)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = time.Duration(api.CIJobsStepStopGraceMillis) * time.Millisecond
	if err := command.Start(); err != nil {
		return 125, errors.New("CI step could not start")
	}
	pid := command.Process.Pid
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		grace := time.NewTimer(time.Duration(api.CIJobsStepStopGraceMillis) * time.Millisecond)
		select {
		case waitErr = <-done:
			if !grace.Stop() {
				select {
				case <-grace.C:
				default:
				}
			}
		case <-grace.C:
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			waitErr = <-done
		}
	}
	// Also kill background descendants left by a shell that exits normally.
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	if ctx.Err() != nil {
		return 143, ctx.Err()
	}
	if waitErr == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(waitErr, &exit) {
		code := exit.ExitCode()
		if code < 1 {
			code = 128 + int(exit.Sys().(syscall.WaitStatus).Signal())
		}
		return code, nil
	}
	return 125, errors.New("CI step wait failed: " + strconv.Itoa(pid))
}

func guestCommand(launch StepExecution, logs io.Writer) *exec.Cmd {
	// #nosec G204 -- explicit CI shell text runs only inside the verified Jobs guest.
	command := exec.Command("/bin/sh", "-e", "-c", launch.Step.Run)
	// Linux chdirs before descriptor remapping/exec. This descriptor is kept
	// open through Start, and CLOEXEC keeps it out of the workload. Resolving a
	// pathname here would let an earlier step replace it with an escaping link.
	command.Dir = "/proc/self/fd/" + strconv.FormatUint(uint64(launch.Directory.Fd()), 10)
	command.Env = append([]string(nil), launch.Environment...)
	command.Stdout, command.Stderr = logs, logs
	return command
}
