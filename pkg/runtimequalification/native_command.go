package runtimequalification

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/onebox-faas/faas/pkg/api"
)

type cappedOutput struct {
	bytes.Buffer
	limit  int
	cancel context.CancelFunc
}

func (w *cappedOutput) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.Len() {
		available := w.limit - w.Len()
		n, err := w.Buffer.Write(p[:available])
		w.cancel()
		return n, errors.Join(err, fmt.Errorf("native command output exceeds bound: %w", ErrEvidence))
	}
	return w.Buffer.Write(p)
}

// Every environment is supplied explicitly. Child tests/builds never inherit
// operator database/storage credentials, signing paths, GOFLAGS or proxy hooks.
func nativeCommand(ctx context.Context, binary string, args []string, directory string, environment []string) (CommandResult, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(runCtx, binary, args...)
	cmd.Dir = directory
	cmd.Env = environment
	cmd.WaitDelay = api.RuntimeQualificationCommandWaitDelay
	configureNativeCommand(cmd)
	out := &cappedOutput{limit: api.RuntimeQualificationLogMaxBytes, cancel: cancel}
	stderr := &cappedOutput{limit: api.RuntimeQualificationLogMaxBytes, cancel: cancel}
	cmd.Stdout = out
	cmd.Stderr = stderr
	err := cmd.Run()
	result := CommandResult{ExitCode: -1, Stdout: out.Bytes(), Stderr: stderr.Bytes()}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if runCtx.Err() != nil {
		return result, fmt.Errorf("native command canceled or exceeded output budget: %w", runCtx.Err())
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("run native command: %w", err)
	}
	return result, nil
}
