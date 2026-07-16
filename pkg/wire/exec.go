package wire

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// ExecRunner runs host commands with os/exec and no shell (argv is passed
// directly, so there is no shell-injection surface). It is the production
// implementation of the small Runner interfaces that fcvm, rootfs, and other
// packages define on their consuming side. stderr is folded into the error for
// diagnosis.
type ExecRunner struct{}

// Run executes argv to completion.
func (ExecRunner) Run(ctx context.Context, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("wire: empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("%s: %w: %s", argv[0], err, bytes.TrimSpace(stderr.Bytes()))
		}
		return fmt.Errorf("%s: %w", argv[0], err)
	}
	return nil
}
