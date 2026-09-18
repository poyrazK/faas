// Package extension defines the in-guest extension lifecycle contract.
//
// The contract is deliberately transport-light: guest-init sends one bounded
// JSON event over a Unix stream and receives one bounded acknowledgement. An
// extension that is absent or unavailable must never prevent the workload from
// booting, so callers can treat ErrUnavailable as a best-effort signal loss.
package extension

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// ProtocolVersion is the lifecycle wire version. Additive fields may be
	// introduced without changing this value; incompatible changes require a
	// new version and a separate compatibility decision.
	ProtocolVersion = 1

	// DefaultSocketPath is the per-guest extension hook endpoint. It is owned
	// by an extension process; guest-init only dials it.
	DefaultSocketPath = "/run/guest/extension.sock"

	// MaxEventBytes and MaxAckBytes cap each half of the request/response
	// exchange before decoding. They prevent a broken extension from pinning
	// guest-init on an unbounded allocation.
	MaxEventBytes = 16 << 10
	MaxAckBytes   = 4 << 10

	// DefaultTimeout bounds one hook call. Lifecycle notifications are
	// telemetry/control hints, never a reason to hold the workload boot path.
	DefaultTimeout = 250 * time.Millisecond
)

// ErrUnavailable means the extension socket could not be reached. It is
// intentionally distinct from malformed protocol data so callers can log a
// missing optional extension at debug level while surfacing a live extension
// that violates the contract.
var ErrUnavailable = errors.New("extension hook unavailable")

// Phase is a closed lifecycle vocabulary. PreSnapshot and Invoke are reserved
// in this first contract slice; their values are stable so future guest-init
// wiring does not need a wire migration.
type Phase string

const (
	PhaseInit        Phase = "init"
	PhasePreSnapshot Phase = "pre_snapshot"
	PhasePostRestore Phase = "post_restore"
	PhaseInvoke      Phase = "invoke"
	PhaseShutdown    Phase = "shutdown"
)

func (p Phase) Valid() bool {
	switch p {
	case PhaseInit, PhasePreSnapshot, PhasePostRestore, PhaseInvoke, PhaseShutdown:
		return true
	default:
		return false
	}
}

// Event is one lifecycle notification delivered to an extension.
type Event struct {
	Version  int               `json:"version"`
	Sequence uint64            `json:"sequence"`
	Phase    Phase             `json:"phase"`
	At       time.Time         `json:"at"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func (e Event) Validate() error {
	if e.Version != ProtocolVersion {
		return fmt.Errorf("version %d is unsupported", e.Version)
	}
	if e.Sequence == 0 {
		return errors.New("sequence is required")
	}
	if !e.Phase.Valid() {
		return fmt.Errorf("phase %q is outside the lifecycle vocabulary", e.Phase)
	}
	if e.At.IsZero() {
		return errors.New("at is required")
	}
	if len(e.Metadata) > 32 {
		return errors.New("metadata has more than 32 entries")
	}
	for key, value := range e.Metadata {
		if strings.TrimSpace(key) == "" || len(key) > 128 {
			return errors.New("metadata key is empty or exceeds 128 bytes")
		}
		if len(value) > 1024 {
			return fmt.Errorf("metadata value %q exceeds 1024 bytes", key)
		}
	}
	return nil
}

// Ack is the extension's response to an Event. A rejected hook is reported
// as an error to the dispatcher but is still bounded and cannot block boot.
type Ack struct {
	Version  int    `json:"version"`
	Sequence uint64 `json:"sequence"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
}

const (
	AckOK       = "ok"
	AckRejected = "rejected"
)

func (a Ack) ValidateFor(e Event) error {
	if a.Version != ProtocolVersion {
		return fmt.Errorf("ack version %d is unsupported", a.Version)
	}
	if a.Sequence != e.Sequence {
		return fmt.Errorf("ack sequence %d does not match event sequence %d", a.Sequence, e.Sequence)
	}
	switch a.Status {
	case AckOK:
		return nil
	case AckRejected:
		if a.Detail == "" {
			return errors.New("extension rejected event without detail")
		}
		return errors.New("extension rejected event: " + a.Detail)
	default:
		return fmt.Errorf("ack status %q is unsupported", a.Status)
	}
}

// DialContext is injectable for unit tests and for callers that need to
// account for a platform-specific Unix transport.
type DialContext func(context.Context, string, string) (net.Conn, error)

// Dispatcher sends lifecycle events to one extension endpoint. A Dispatcher
// is safe for concurrent calls; a small mutex preserves lifecycle order on
// the wire, and the timeout ensures one slow hook cannot block the guest
// indefinitely.
type Dispatcher struct {
	SocketPath string
	Timeout    time.Duration
	Now        func() time.Time
	Dial       DialContext
	mu         sync.Mutex
	sequence   atomic.Uint64
}

func NewDispatcher(socketPath string) *Dispatcher {
	if socketPath == "" {
		socketPath = DefaultSocketPath
	}
	return &Dispatcher{SocketPath: socketPath, Timeout: DefaultTimeout}
}

// Dispatch constructs and sends one event. Errors are wrapped with the phase
// so guest-init logs remain actionable without exposing customer payloads.
func (d *Dispatcher) Dispatch(ctx context.Context, phase Phase, metadata map[string]string) error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now().UTC()
	if d.Now != nil {
		now = d.Now().UTC()
	}
	event := Event{
		Version:  ProtocolVersion,
		Sequence: d.sequence.Add(1),
		Phase:    phase,
		At:       now,
		Metadata: metadata,
	}
	if err := event.Validate(); err != nil {
		return fmt.Errorf("extension %s: %w", phase, err)
	}
	if err := d.dispatchEvent(ctx, event); err != nil {
		return fmt.Errorf("extension %s: %w", phase, err)
	}
	return nil
}

// DispatchEvent sends a pre-built event. It is exported for replay tests and
// for a future host-side lifecycle bridge that already owns event sequencing.
func (d *Dispatcher) DispatchEvent(ctx context.Context, event Event) error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dispatchEvent(ctx, event)
}

func (d *Dispatcher) dispatchEvent(ctx context.Context, event Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if ctx == nil {
		return errors.New("context is required")
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dial := d.Dial
	if dial == nil {
		dialer := net.Dialer{Timeout: timeout}
		dial = dialer.DialContext
	}
	conn, err := dial(callCtx, "unix", d.SocketPath)
	if err != nil {
		return fmt.Errorf("%w: dial %s: %w", ErrUnavailable, d.SocketPath, err)
	}
	defer func() { _ = conn.Close() }()
	deadline, _ := callCtx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set deadline: %w", err)
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	if len(payload) > MaxEventBytes {
		return fmt.Errorf("event frame is %d bytes, max %d", len(payload), MaxEventBytes)
	}
	payload = append(payload, '\n')
	if n, err := conn.Write(payload); err != nil {
		return fmt.Errorf("write event: %w", err)
	} else if n != len(payload) {
		return io.ErrShortWrite
	}

	reader := bufio.NewReader(io.LimitReader(conn, MaxAckBytes+1))
	ackBytes, err := reader.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read ack: %w", err)
	}
	if len(ackBytes) > MaxAckBytes {
		return fmt.Errorf("ack frame is %d bytes, max %d", len(ackBytes), MaxAckBytes)
	}
	var ack Ack
	if err := json.Unmarshal(bytesTrimSuffixNewline(ackBytes), &ack); err != nil {
		return fmt.Errorf("decode ack: %w", err)
	}
	return ack.ValidateFor(event)
}

func bytesTrimSuffixNewline(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] == '\n' {
		return b[:len(b)-1]
	}
	return b
}

// IsUnavailable reports whether an error came from an absent/unreachable
// optional extension endpoint.
func IsUnavailable(err error) bool {
	return errors.Is(err, ErrUnavailable) || errors.Is(err, os.ErrNotExist)
}
