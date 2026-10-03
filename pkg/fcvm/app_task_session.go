package fcvm

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

const (
	appTaskDialWindow  = 5 * time.Second
	appTaskDialStep    = 20 * time.Millisecond
	appTaskDialTimeout = 200 * time.Millisecond
)

// AppTaskSession owns one connected task guest. It cannot restore or boot a
// VM; that split keeps command dispatch behind the scheduler's running fence.
type AppTaskSession struct {
	conn      net.Conn
	client    *apptaskproto.Client
	destroyVM func(context.Context) error
	destroyMu sync.Mutex
	destroyed bool
}

func NewAppTaskSession(conn net.Conn, destroyVM ...func(context.Context) error) (*AppTaskSession, error) {
	client, err := apptaskproto.NewClient(conn)
	if err != nil {
		return nil, err
	}
	var destroy func(context.Context) error
	if len(destroyVM) > 0 {
		destroy = destroyVM[0]
	}
	return &AppTaskSession{conn: conn, client: client, destroyVM: destroy}, nil
}

func (s *AppTaskSession) Execute(ctx context.Context, req apptaskproto.Request) (apptaskproto.Result, error) {
	return s.ExecuteWithOutput(ctx, req, nil)
}

func (s *AppTaskSession) ExecuteWithOutput(ctx context.Context, req apptaskproto.Request, receive apptaskproto.OutputReceiver) (apptaskproto.Result, error) {
	var zero apptaskproto.Result
	if s == nil || s.client == nil {
		return zero, fmt.Errorf("fcvm: nil app task session")
	}
	s.destroyMu.Lock()
	destroyed := s.destroyed
	s.destroyMu.Unlock()
	if destroyed {
		return zero, fmt.Errorf("fcvm: app task session destroyed")
	}
	return s.client.ExecuteWithOutput(ctx, req, receive)
}

func (s *AppTaskSession) Destroy(ctx context.Context) error {
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
		if err := s.destroyVM(ctx); err != nil {
			return err
		}
	}
	return closeErr
}

type AppTaskDialer interface {
	DialAppTask(context.Context, Lease) (*AppTaskSession, error)
}

// DialAppTask connects only after WakeAppTask has completed. The first
// protocol request sent over the returned session is therefore the exact
// point at which customer code becomes executable.
func (v *JailerVMM) DialAppTask(ctx context.Context, lease Lease) (*AppTaskSession, error) {
	if v == nil {
		return nil, fmt.Errorf("fcvm: dial app task: nil vmm")
	}
	if lease.Instance == "" {
		return nil, fmt.Errorf("fcvm: dial app task: empty instance")
	}
	if v.chrootBase == "" || v.fcName == "" {
		return nil, fmt.Errorf("fcvm: dial app task: vmm chroot is not configured")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(appTaskDialWindow)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	sock := v.vsockUDSSock(lease.Instance)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dialCtx, cancel := context.WithDeadline(ctx, minTime(deadline, time.Now().Add(appTaskDialTimeout)))
		conn, err := (&net.Dialer{}).DialContext(dialCtx, "unix", sock)
		cancel()
		if err == nil {
			if err := completeAppTaskConnect(ctx, conn); err == nil {
				return NewAppTaskSession(conn, func(destroyCtx context.Context) error {
					return v.Kill(destroyCtx, lease)
				})
			} else {
				lastErr = err
				_ = conn.Close()
			}
		} else {
			lastErr = err
		}
		wait := appTaskDialStep
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
	return nil, fmt.Errorf("fcvm: dial app task vsock uds %s: %w", sock, lastErr)
}

func completeAppTaskConnect(ctx context.Context, conn net.Conn) error {
	deadline := time.Now().Add(appTaskDialTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	if err := writeExecutionConnect(conn, apptaskproto.VsockPort); err != nil {
		return fmt.Errorf("write CONNECT %d: %w", apptaskproto.VsockPort, err)
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
