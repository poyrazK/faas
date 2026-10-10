package fcvm

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/onebox-faas/faas/pkg/profileproto"
)

// Typed guest refusals for an on-demand profile capture (ADR-967).
var (
	ErrProfileCaptureUnsupported = errors.New("guest has no profile collectors for on-demand capture")
	ErrProfileCaptureBusy        = errors.New("a profile capture is already running on this instance")
	ErrProfileCaptureSuspended   = errors.New("instance is checkpointing")
	ErrProfileCaptureNotRunning  = errors.New("instance is not running on this host")
)

// GuestProfileCapturer is implemented by VMMs that can deliver an on-demand
// capture to guest-init's control listener.
type GuestProfileCapturer interface {
	CaptureGuestProfile(context.Context, Lease, profileproto.CaptureRequest) (profileproto.CaptureResult, error)
}

// captureReplyTimeout bounds the whole exchange: the window, the guest's
// flush grace and transport slack.
func captureReplyTimeout(req profileproto.CaptureRequest) time.Duration {
	return req.Duration() + profileproto.CaptureGrace + 5*time.Second
}

// CaptureGuestProfile arms the guest's collectors for one bounded window and
// returns the profiles they report. vmmd bounds and decodes the JSON envelope
// but never parses the pprof payloads it carries.
func (v *JailerVMM) CaptureGuestProfile(ctx context.Context, l Lease, req profileproto.CaptureRequest) (profileproto.CaptureResult, error) {
	var result profileproto.CaptureResult
	if v == nil || v.chrootBase == "" || l.Instance == "" {
		return result, fmt.Errorf("vmm: profile capture: invalid VMM or instance")
	}
	if err := req.Validate(); err != nil {
		return result, fmt.Errorf("vmm: profile capture: %w", err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return result, fmt.Errorf("vmm: profile capture encode: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, captureReplyTimeout(req))
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(callCtx, "unix", v.vsockUDSSock(l.Instance))
	if err != nil {
		return result, fmt.Errorf("vmm: profile capture dial: %w", err)
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(callCtx, func() { _ = conn.Close() })
	defer stop()
	if deadline, ok := callCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %d\n", resumeHookGuestPort); err != nil {
		return result, fmt.Errorf("vmm: profile capture CONNECT: %w", err)
	}
	if ack, err := readConnectAck(conn); err != nil || ack != "OK" {
		return result, fmt.Errorf("vmm: profile capture CONNECT reply %q: %w", ack, err)
	}
	return exchangeProfileCapture(conn, body)
}

// exchangeProfileCapture runs the framed request/reply after CONNECT.
func exchangeProfileCapture(conn io.ReadWriter, body []byte) (profileproto.CaptureResult, error) {
	var result profileproto.CaptureResult
	msg := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(msg[:4], profileproto.CaptureMessageType)
	binary.BigEndian.PutUint32(msg[4:], uint32(len(body)))
	if _, err := conn.Write(append(msg, body...)); err != nil {
		return result, fmt.Errorf("vmm: profile capture send: %w", err)
	}
	var hdr [5]byte
	if _, err := io.ReadFull(conn, hdr[:1]); err != nil {
		return result, fmt.Errorf("vmm: profile capture ack: %w", err)
	}
	switch hdr[0] {
	case profileproto.CaptureAckOK:
	case profileproto.CaptureAckBusy:
		return result, ErrProfileCaptureBusy
	case profileproto.CaptureAckSuspended:
		return result, ErrProfileCaptureSuspended
	case profileproto.CaptureAckUnsupported:
		return result, ErrProfileCaptureUnsupported
	default:
		// Guests that predate ADR-967 answer an unknown message type with a
		// single non-zero resume ack.
		return result, fmt.Errorf("%w (ack=%d)", ErrProfileCaptureUnsupported, hdr[0])
	}
	if _, err := io.ReadFull(conn, hdr[1:]); err != nil {
		return result, fmt.Errorf("vmm: profile capture length: %w", err)
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n == 0 || n > profileproto.CaptureMaxReplyBytes {
		return result, fmt.Errorf("vmm: profile capture reply length %d out of range", n)
	}
	reply := make([]byte, n)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return result, fmt.Errorf("vmm: profile capture reply: %w", err)
	}
	if err := json.Unmarshal(reply, &result); err != nil {
		return result, fmt.Errorf("vmm: profile capture decode: %w", err)
	}
	if err := result.Validate(); err != nil {
		return profileproto.CaptureResult{}, fmt.Errorf("vmm: profile capture: %w", err)
	}
	return result, nil
}

// CaptureProfile delivers an on-demand capture to one live app instance.
func (m *Manager) CaptureProfile(ctx context.Context, instance string, req profileproto.CaptureRequest) (profileproto.CaptureResult, error) {
	capturer, ok := m.vmm.(GuestProfileCapturer)
	if !ok {
		return profileproto.CaptureResult{}, ErrProfileCaptureUnsupported
	}
	m.mu.Lock()
	live := m.live[instance]
	var lease Lease
	ready := live != nil && !live.ExecutionOnly && !live.IsJob && !live.Paused
	if ready {
		lease = live.Lease
	}
	m.mu.Unlock()
	if !ready {
		return profileproto.CaptureResult{}, ErrProfileCaptureNotRunning
	}
	return capturer.CaptureGuestProfile(ctx, lease, req)
}
