// Legacy OCI healthcheck DGRAM decoder (ADR-139). The unsolicited wire has
// no boot challenge and is not production readiness evidence. ADR-643 uses
// the fresh host-initiated STREAM protocol in image_readiness.go instead.
// The decoder remains available for wire compatibility and telemetry tests.

package fcvm

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// VsockHealthcheckPort mirrors guest/init/healthcheck_linux.go's
// VsockHealthcheckPort. The guest-init and vmmd packages cannot
// share compile-time symbols — they're separate binaries at
// different stages of the boot chain — so the literal must be
// hand-synced. A drift here is a silent "no healthcheck reports"
// failure mode for telemetry; DGRAM reports never authorize readiness.
const VsockHealthcheckPort uint32 = 1029

// VsockHealthcheckMaxOutput mirrors guest/init/healthcheck_linux.go's
// VsockHealthcheckMaxOutput. Used to bound the receiver's read
// buffer so a malicious / runaway guest can't OOM the host.
const VsockHealthcheckMaxOutput = 4096

// HealthcheckStatus mirrors guest/init/healthcheck_linux.go's
// status enum (0x00=starting, 0x01=pass, 0x02=fail). The host
// decoder reads these bytes verbatim; renumbering requires a
// vmmdpb wire bump.
const (
	HealthcheckStatusStarting byte = 0x00
	HealthcheckStatusPass     byte = 0x01
	HealthcheckStatusFail     byte = 0x02
)

// HealthcheckMsgType is the 4-byte big-endian discriminator
// guest-init stamps as the first word of every report. Distinct
// from framework_ready (0x04), sidecar_init_exit (0x02), and
// characterization (0x03) so a future port-multiplexing scheme
// can fan frames to the right decoder without ambiguity.
const HealthcheckMsgType uint32 = 0x05

// ErrHealthcheckFrameTooShort is returned when the DGRAM payload
// is smaller than the 13-byte header. The caller treats this as
// a transient frame error and continues the recv loop.
var ErrHealthcheckFrameTooShort = errors.New("fcvm: healthcheck frame < 13 bytes")

// ErrHealthcheckMsgType is returned when the leading 4 bytes do
// not match HealthcheckMsgType. This is a defensive guard
// against a future port-multiplexing bug; today every frame on
// port 1029 is a healthcheck report.
var ErrHealthcheckMsgType = errors.New("fcvm: healthcheck frame msg-type discriminator mismatch")

// HealthcheckReport is the typed view of one DGRAM frame. The
// host's vmmd decodes the same struct (mirrored in
// guest/init/healthcheck_linux.go).
type HealthcheckReport struct {
	Seq      uint32 // monotonic; 0-based
	Status   byte   // HealthcheckStatus*
	Output   []byte // ≤ VsockHealthcheckMaxOutput bytes
	TsUnixMs int64  // guest-side stamp; 0 if the guest didn't stamp
}

// decodeHealthcheckReport decodes one DGRAM frame. Pure-Go so the
// unit test can exercise the wire shape without an AF_VSOCK
// socket. Returns ErrHealthcheckFrameTooShort /
// ErrHealthcheckMsgType for the documented transient errors so
// the caller can decide whether to bail or continue.
func decodeHealthcheckReport(b []byte) (HealthcheckReport, error) {
	const hdrLen = 13 // 4B msg-type + 4B seq + 1B status + 4B olen
	if len(b) < hdrLen {
		return HealthcheckReport{}, ErrHealthcheckFrameTooShort
	}
	if got := binary.BigEndian.Uint32(b[0:4]); got != HealthcheckMsgType {
		return HealthcheckReport{}, fmt.Errorf("%w: got 0x%08x", ErrHealthcheckMsgType, got)
	}
	seq := binary.BigEndian.Uint32(b[4:8])
	status := b[8]
	olen := binary.BigEndian.Uint32(b[9:13])
	if olen > VsockHealthcheckMaxOutput {
		// Truncate rather than error — a runaway guest's output
		// over the cap shouldn't break the host's recv loop. The
		// tail bytes (the most-recent output) carry the diagnostic.
		olen = VsockHealthcheckMaxOutput
	}
	if uint32(len(b)-hdrLen) < olen {
		return HealthcheckReport{}, fmt.Errorf("fcvm: healthcheck frame truncated (want %d, have %d)", olen, len(b)-hdrLen)
	}
	out := make([]byte, olen)
	copy(out, b[hdrLen:hdrLen+olen])
	return HealthcheckReport{
		Seq:    seq,
		Status: status,
		Output: out,
	}, nil
}

// healthcheckRecvLoop reads DGRAM frames off vsock 1029 and
// pushes decoded HealthcheckReports onto reports until ctx is
// done or sock returns EOF. The caller owns sock + the lifetime
// of the loop (typically one per VM lifecycle).
//
// Abstracted behind an interface for testability — the real
// implementation dials unix.Recvfrom on AF_VSOCK; the unit test
// substitutes an io.Reader that yields canned frames.
type healthcheckFrameReader interface {
	Read(ctx context.Context) ([]byte, error)
}

// drainHealthcheckFrames is the testable core of the receiver.
// It reads frames until ctx is cancelled, dropping malformed
// frames with a log-friendly error. Returns the count of valid
// frames seen + the first 'pass' report (zero-valued if none).
func drainHealthcheckFrames(ctx context.Context, r healthcheckFrameReader) (int, HealthcheckReport, error) {
	var firstPass HealthcheckReport
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return count, firstPass, err
		}
		buf, err := r.Read(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return count, firstPass, nil
			}
			return count, firstPass, err
		}
		report, derr := decodeHealthcheckReport(buf)
		if derr != nil {
			// Defensive: drop malformed frames and keep going.
			// A single corrupt DGRAM shouldn't break the loop;
			// the next frame may be valid.
			continue
		}
		count++
		if report.Status == HealthcheckStatusPass && firstPass.Seq == 0 && count == 1 {
			// Pin only the first pass. Subsequent passes
			// indicate the workload stayed healthy — the
			// first-pass stamp is what waitReady gates on.
			firstPass = report
		} else if report.Status == HealthcheckStatusPass && firstPass.Seq == 0 {
			firstPass = report
		}
	}
}
