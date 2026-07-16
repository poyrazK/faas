// Tests for ExecRunner. We exercise:
//   - happy path: argv runs and returns nil
//   - empty argv: rejected with a clear error
//   - non-zero exit: error wraps the binary name and exit code
//   - stderr folding: stderr content is appended to the error message
//   - cancellation: ctx cancellation surfaces as the runner's error

package wire_test

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestExecRunner_Success(t *testing.T) {
	r := wire.ExecRunner{}
	if err := r.Run(context.Background(), []string{"/bin/sh", "-c", "exit 0"}); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestExecRunner_EmptyArgv(t *testing.T) {
	r := wire.ExecRunner{}
	err := r.Run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty argv")
	}
	if !strings.Contains(err.Error(), "empty command") {
		t.Errorf("error %q should mention 'empty command'", err.Error())
	}
}

func TestExecRunner_NonZeroExit(t *testing.T) {
	r := wire.ExecRunner{}
	err := r.Run(context.Background(), []string{"/bin/sh", "-c", "exit 7"})
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "/bin/sh") {
		t.Errorf("error %q should name the binary", err.Error())
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Errorf("error should wrap *exec.ExitError; got %T", err)
	}
}

func TestExecRunner_StderrFolded(t *testing.T) {
	r := wire.ExecRunner{}
	err := r.Run(context.Background(), []string{"/bin/sh", "-c", "echo boom 1>&2; exit 1"})
	if err == nil {
		t.Fatal("expected error for failing command")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("stderr content should be folded into error, got %q", err.Error())
	}
}

func TestExecRunner_ContextCancel(t *testing.T) {
	r := wire.ExecRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	// Start a command that runs forever, then cancel.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := r.Run(ctx, []string{"/bin/sh", "-c", "sleep 30"})
	if err == nil {
		t.Fatal("expected error after ctx cancel")
	}
	// Either an exit error from the killed process or a context error.
	// We don't pin a specific underlying type — the contract is "errors out".
}