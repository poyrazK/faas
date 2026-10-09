// Package crashcapturewire is the guest-init ⇄ vmmd protocol for the ADR-733
// SDK trigger: an app asks for a crash capture of its own instance from
// inside its error handler.
//
// guest-init opens one vsock stream per request to the host on Port and
// writes a Request frame. vmmd names the instance from the listener that
// accepted the stream (never from the frame), queues the capture and writes
// up to two Response frames: StatusRequested with the capture ID as soon as
// the request row exists, then the outcome (StatusCaptured, StatusFailed or
// StatusPending when the wait ran out). A refusal is a single frame.
//
// The stream stays open while schedd pauses and snapshots the instance, so
// the app's handler is captured mid-call with its state intact. In a fork
// restored from that capture the stream is reset; guest-init then reports
// StatusCaptured with InFork set.
package crashcapturewire

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Port is the host vsock port guest-init dials (Firecracker forwards it to
// the per-instance Unix listener vmmd binds).
const Port uint32 = 1032

// MaxFrame bounds every frame in both directions.
const MaxFrame = 4096

// DefaultWait and MaxWait bound how long the app's call blocks for the
// capture to finish.
const (
	DefaultWait = 15 * time.Second
	MaxWait     = 30 * time.Second
)

// Response statuses.
const (
	StatusRequested   = "requested"
	StatusCaptured    = "captured"
	StatusFailed      = "failed"
	StatusPending     = "pending"
	StatusRefused     = "refused"
	StatusNotEnabled  = "not_enabled"
	StatusUnavailable = "unavailable"
)

// Request is the app's ask, forwarded by guest-init.
type Request struct {
	Reason string `json:"reason,omitempty"`
	Route  string `json:"route,omitempty"`
	WaitMs int    `json:"wait_ms,omitempty"`
}

// Wait returns the bounded wait the request asks for.
func (r Request) Wait() time.Duration {
	if r.WaitMs <= 0 {
		return DefaultWait
	}
	return min(time.Duration(r.WaitMs)*time.Millisecond, MaxWait)
}

// Response is one frame from vmmd, and the JSON guest-init returns to the
// app (InFork is set by guest-init only).
type Response struct {
	Status    string `json:"status"`
	CaptureID string `json:"capture_id,omitempty"`
	Code      string `json:"code,omitempty"`
	InFork    bool   `json:"in_fork,omitempty"`
}

// Final reports whether no further frame follows this one.
func (r Response) Final() bool { return r.Status != StatusRequested }

// WriteFrame writes v as a length-prefixed JSON frame.
func WriteFrame(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("crash capture frame: marshal: %w", err)
	}
	if len(body) > MaxFrame {
		return fmt.Errorf("crash capture frame: %d bytes exceeds %d", len(body), MaxFrame)
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(body))) //nolint:gosec // bounded by MaxFrame
	copy(frame[4:], body)
	if _, err := w.Write(frame); err != nil {
		return fmt.Errorf("crash capture frame: write: %w", err)
	}
	return nil
}

// ReadFrame reads one length-prefixed JSON frame into v.
func ReadFrame(r io.Reader, v any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > MaxFrame {
		return fmt.Errorf("crash capture frame: invalid length %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("crash capture frame: decode: %w", err)
	}
	return nil
}
