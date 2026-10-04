package vmmdgrpc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// namespaceBridgeReadiness owns the readiness descriptor until it returns.
// Closing it on cancellation interrupts a helper that never writes or exits;
// limiting the reader also bounds a helper that writes without a newline.
func namespaceBridgeReadiness(parent context.Context, ready *os.File) (string, error) {
	ctx, cancel := context.WithTimeout(parent, api.NamespaceBridgeReadinessTimeout)
	defer cancel()
	defer func() { _ = ready.Close() }()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	stop := context.AfterFunc(ctx, func() { _ = ready.Close() })
	defer stop()
	line, err := bufio.NewReader(io.LimitReader(ready, int64(api.NamespaceBridgeReadinessMaxBytes)+1)).ReadString('\n')
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if len(line) > api.NamespaceBridgeReadinessMaxBytes {
		return "", fmt.Errorf("readiness record exceeds %d bytes", api.NamespaceBridgeReadinessMaxBytes)
	}
	return line, err
}
