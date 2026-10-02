package imaged

// adr: 430

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/onebox-faas/faas/pkg/api"
)

// Exceeding either stream refuses the result instead of retaining a truncated
// JSON report. Subprocess diagnostics may contain customer data; do not echo them.
type scanCommandBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *scanCommandBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.buffer.Len() {
		return 0, fmt.Errorf("scan command output exceeds bound")
	}
	return b.buffer.Write(p)
}

func runBoundedScanCommand(ctx context.Context, stdoutLimit int, bin string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = api.ApplicationStandardRuntimeCleanupTimeout
	stdout := &scanCommandBuffer{limit: stdoutLimit}
	stderr := &scanCommandBuffer{limit: api.ApplicationStandardScanMaxErrorBytes}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		return nil, nil, fmt.Errorf("scan command %s failed: %w", bin, err)
	}
	return stdout.buffer.Bytes(), stderr.buffer.Bytes(), nil
}

var _ io.Writer = (*scanCommandBuffer)(nil)
