package internal

import (
	"context"
	"net"

	"github.com/onebox-faas/faas/pkg/executionproto"
)

// ExecutionHandler is the guest-init/runtime seam for a language executor.
// Implementations invoke the selected interpreter exactly once and must write
// only bounded diagnostic bytes through stdout/stderr. Source and input must
// not be retained after the handler returns.
type ExecutionHandler = executionproto.Handler

// ServeExecution runs one disposable guest execution on conn. It is a thin
// guest-facing wrapper kept in the runner package so each runtime image can
// wire its interpreter without reimplementing framing, deadline propagation,
// or output accounting. The caller should exit the guest after this returns.
func ServeExecution(ctx context.Context, conn net.Conn, handler ExecutionHandler) error {
	return executionproto.Serve(ctx, conn, handler)
}
