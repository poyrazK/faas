// Package resumetimingproto defines the optional timing frame guest-init
// appends to a successful resume-hook ack (ADR-022 amendment). vmmd measures
// the resume hook from the host side only, so without it the guest's share
// (kernel reseed, clock step, userspace reseed barrier) cannot be told apart
// from vsock transport and the guest simply not running yet.
//
// The frame follows the ADR-687 capability byte and is announced by
// CapTimings in that byte: a host that predates the frame reads the single
// capability byte and closes, and a guest that predates it never sets the
// bit. Neither side waits for bytes the other does not send.
package resumetimingproto

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// CapTimings is the capability bit that announces a timing frame after the
// capability byte. Bit 0x01 is the ADR-687 userspace reseed capability.
const CapTimings byte = 0x02

// MaxBodyBytes bounds the Timings JSON the host accepts.
const MaxBodyBytes = 1024

// Timings are the guest's resume-hook stages in microseconds, measured on
// the guest's monotonic clock (the hook steps the wall clock part-way
// through, so wall-clock deltas would be wrong).
type Timings struct {
	// ReadUs is from the request header arriving to the body being decoded.
	ReadUs int64 `json:"read_us"`
	// EntropyUs, HWRNGUs, ClockUs and UUIDMarkerUs are the kernel-side steps
	// of RunResumeHook, in order.
	EntropyUs    int64 `json:"entropy_us"`
	HWRNGUs      int64 `json:"hwrng_us"`
	ClockUs      int64 `json:"clock_us"`
	UUIDMarkerUs int64 `json:"uuid_marker_us"`
	// UserspaceReseedUs is the ADR-687 barrier: every registered Node and
	// Python process reseeding its generators. ReseedClients counts those
	// processes; ReseedSlowestUs and ReseedSlowestRuntime name the one the
	// barrier waited for.
	UserspaceReseedUs    int64  `json:"userspace_reseed_us"`
	ReseedClients        int    `json:"reseed_clients"`
	ReseedSlowestUs      int64  `json:"reseed_slowest_us"`
	ReseedSlowestRuntime string `json:"reseed_slowest_runtime,omitempty"`
	// ProfilesUs resumes guest profilers; AfterRestoreUs is the application's
	// after_restore callback.
	ProfilesUs     int64 `json:"profiles_us"`
	AfterRestoreUs int64 `json:"after_restore_us"`
	// TotalUs is from the request header arriving to the ack being written.
	TotalUs int64 `json:"total_us"`
}

// AppendFrame appends the frame for t: a 4-byte big-endian length and the
// Timings JSON.
func AppendFrame(dst []byte, t Timings) ([]byte, error) {
	body, err := json.Marshal(t)
	if err != nil {
		return dst, fmt.Errorf("resumetimingproto: marshal: %w", err)
	}
	if len(body) > MaxBodyBytes {
		return dst, fmt.Errorf("resumetimingproto: body %d bytes exceeds %d", len(body), MaxBodyBytes)
	}
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(body)))
	return append(dst, body...), nil
}

// ReadFrame reads one frame written by AppendFrame.
func ReadFrame(r io.Reader) (Timings, error) {
	var t Timings
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return t, fmt.Errorf("resumetimingproto: read length: %w", err)
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > MaxBodyBytes {
		return t, fmt.Errorf("resumetimingproto: body length %d outside 1..%d", n, MaxBodyBytes)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return t, fmt.Errorf("resumetimingproto: read body: %w", err)
	}
	if err := json.Unmarshal(body, &t); err != nil {
		return t, fmt.Errorf("resumetimingproto: decode: %w", err)
	}
	if t.TotalUs < 0 {
		return Timings{}, errors.New("resumetimingproto: negative total")
	}
	return t, nil
}
