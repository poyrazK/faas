package fcvm

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/executionproto"
)

const (
	executionDialWindow  = 5 * time.Second
	executionDialStep    = 20 * time.Millisecond
	executionDialTimeout = 200 * time.Millisecond
)

// ExecutionSession is the vmmd-side adapter for a single restored execution
// guest. It owns the connected vsock stream and guarantees that Destroy closes
// it exactly once. The scheduler-facing adapter can wrap this type without
// gaining access to Firecracker paths or guest internals.
type ExecutionSession struct {
	conn      net.Conn
	client    *executionproto.Client
	destroyVM func(context.Context) error
	destroyMu sync.Mutex
	destroyed bool
}

// NewExecutionSession wraps an already-connected guest stream. The caller is
// responsible for completing Firecracker's CONNECT handshake before passing
// the stream here.
func NewExecutionSession(conn net.Conn, destroyVM ...func(context.Context) error) (*ExecutionSession, error) {
	client, err := executionproto.NewClient(conn)
	if err != nil {
		return nil, err
	}
	var destroy func(context.Context) error
	if len(destroyVM) > 0 {
		destroy = destroyVM[0]
	}
	return &ExecutionSession{conn: conn, client: client, destroyVM: destroy}, nil
}

// Execute sends one request to the guest. A session cannot be reused after a
// successful, failed, or cancelled exchange.
func (s *ExecutionSession) Execute(ctx context.Context, req executionproto.Request) (executionproto.Result, error) {
	var zero executionproto.Result
	if s == nil || s.client == nil {
		return zero, fmt.Errorf("fcvm: nil execution session")
	}
	s.destroyMu.Lock()
	destroyed := s.destroyed
	s.destroyMu.Unlock()
	if destroyed {
		return zero, fmt.Errorf("fcvm: execution session destroyed")
	}
	return s.client.Execute(ctx, req)
}

// Destroy is idempotent and closes the vsock stream immediately. VM process
// teardown remains the Manager/VMM responsibility; closing the stream first
// unblocks a guest stuck in a read or a handler write.
func (s *ExecutionSession) Destroy(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.destroyMu.Lock()
	if s.destroyed {
		s.destroyMu.Unlock()
		return nil
	}
	s.destroyed = true
	s.destroyMu.Unlock()
	var closeErr error
	if s.conn != nil {
		closeErr = s.conn.Close()
	}
	if s.destroyVM != nil {
		vmErr := s.destroyVM(ctx)
		if vmErr != nil {
			return vmErr
		}
	}
	return closeErr
}

// ExecutionDialer is implemented by JailerVMM and is intentionally narrower
// than VMM. It is the seam a future schedd/vmmd client uses after Restore or
// cold boot has completed and the resume hook has run.
type ExecutionDialer interface {
	DialExecution(context.Context, Lease) (*ExecutionSession, error)
}

// DialExecution connects to the Firecracker vsock proxy for one live lease.
// The CONNECT handshake is completed before the session is returned, so the
// first byte written by Execute is always an execution protocol frame. This
// method never restores or boots a VM and therefore cannot accidentally send
// tenant source before the caller's restore/resume fence.
func (v *JailerVMM) DialExecution(ctx context.Context, l Lease) (*ExecutionSession, error) {
	if v == nil {
		return nil, fmt.Errorf("fcvm: dial execution: nil vmm")
	}
	if l.Instance == "" {
		return nil, fmt.Errorf("fcvm: dial execution: empty instance")
	}
	if v.chrootBase == "" || v.fcName == "" {
		return nil, fmt.Errorf("fcvm: dial execution: vmm chroot is not configured")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(executionDialWindow)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	sock := v.vsockUDSSock(l.Instance)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dialCtx, cancel := context.WithDeadline(ctx, minTime(deadline, time.Now().Add(executionDialTimeout)))
		conn, err := (&net.Dialer{}).DialContext(dialCtx, "unix", sock)
		cancel()
		if err == nil {
			if err := completeExecutionConnect(ctx, conn); err == nil {
				return NewExecutionSession(conn, func(destroyCtx context.Context) error {
					return v.Kill(destroyCtx, l)
				})
			} else {
				lastErr = err
				_ = conn.Close()
			}
		} else {
			lastErr = err
		}
		wait := executionDialStep
		if remaining := time.Until(deadline); remaining < wait {
			wait = remaining
		}
		if wait <= 0 {
			break
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if lastErr == nil {
		lastErr = context.DeadlineExceeded
	}
	return nil, fmt.Errorf("fcvm: dial execution vsock uds %s: %w", sock, lastErr)
}

func completeExecutionConnect(ctx context.Context, conn net.Conn) error {
	deadline := time.Now().Add(executionDialTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	if err := writeExecutionConnect(conn, executionproto.VsockPort); err != nil {
		return fmt.Errorf("write CONNECT %d: %w", executionproto.VsockPort, err)
	}
	ack, err := readConnectAck(conn)
	if err != nil {
		return fmt.Errorf("read CONNECT ack: %w", err)
	}
	if ack != "OK" {
		return fmt.Errorf("CONNECT rejected: %q", ack)
	}
	return conn.SetDeadline(time.Time{})
}

func writeExecutionConnect(conn net.Conn, port uint32) error {
	return writeAllExecution(conn, []byte(fmt.Sprintf("CONNECT %d\n", port)))
}

func writeAllExecution(w net.Conn, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(p) {
			return fmt.Errorf("short write: %d/%d", n, len(p))
		}
		p = p[n:]
	}
	return nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
