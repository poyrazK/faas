//go:build !linux

// Stub for non-linux builds (macOS dev boxes, CI cargo / cross-
// compile). The DGRAM recv loop is linux-only because AF_VSOCK
// is a Linux kernel feature. The cmd main() calls the
// linux-stubbed StartFrameworkReadyReceiver; on non-linux we
// return an error and the cmd main logs and continues with a
// no-op receiver (the engine's warm-capture wait in PR #470-FU-A
// falls through to init-tier on the no-signal path).

package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

// FrameworkReadyReceiver is the host-side DGRAM listener. The
// non-linux stub is a nil-receiver so cmd/vmmd/main.go's
// defer recv.Close() compiles on every platform. fd is an
// atomic.Int32 to mirror the linux type exactly (CRIT-related
// review feedback on PR #470-FU-B).
//
// emitter (issue #463 / ADR-069 / ADR-071 / PR-C) mirrors the
// linux field so the always-compiled
// (r).WithSidecarEmitter method (sidecar_events_wire.go) can
// dereference it on every build. The dispatch in
// framework_ready_recv.go is linux-only; on non-linux the
// emitter is set but never consumed.
type FrameworkReadyReceiver struct {
	fd             atomic.Int32
	log            *slog.Logger
	mgr            *fcvm.Manager
	emitter        SidecarEventEmitter
	eventPublisher GuestEventPublisher
}

// GuestEventPublisher mirrors the Linux receiver's callback type so the
// always-compiled WithEventPublisher method remains source-compatible on
// development hosts without AF_VSOCK.
type GuestEventPublisher func(context.Context, string, []byte) error

// Close is a no-op on non-linux platforms.
func (r *FrameworkReadyReceiver) Close() {
	if r != nil {
		_ = r.fd.Load()
		_ = r.log
		_ = r.mgr
		_ = r.emitter
		_ = r.eventPublisher
	}
}

// StartFrameworkReadyReceiver returns an error on non-linux
// platforms. The cmd main() soft-fails on this path so the dev
// box can still bring up vmmd without AF_VSOCK support.
func StartFrameworkReadyReceiver(_ context.Context, _ *slog.Logger, _ *fcvm.Manager, _ *fcvm.JailerVMM) (*FrameworkReadyReceiver, error) {
	return nil, fmt.Errorf("framework_ready DGRAM recv: AF_VSOCK requires Linux")
}
