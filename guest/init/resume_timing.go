package main

import (
	"encoding/binary"
	"math"
	"time"
)

// VsockResumeTimingMarker opens an optional trailer after the resume
// capability byte: marker, version, then four uint16 BE millisecond durations
// (request read, kernel resume, userspace reseed, after_restore). Hosts that
// stop after the capability byte never read it. Mirrors pkg/fcvm.
const (
	VsockResumeTimingMarker  = 'T'
	VsockResumeTimingVersion = 1
)

// resumeTiming is the guest-side split of one resume hook, reported to vmmd
// in the optional ack trailer so the host's single ack_ms can be attributed.
type resumeTiming struct {
	read         time.Duration // vsock accept until the request is parsed
	kernel       time.Duration // entropy, reseed, clock step, UUID marker
	reseed       time.Duration // ADR-687 userspace reseed barrier
	afterRestore time.Duration // application after_restore callback
}

func clampMillis(d time.Duration) uint16 {
	ms := d.Milliseconds()
	switch {
	case ms < 0:
		return 0
	case ms > math.MaxUint16:
		return math.MaxUint16
	}
	return uint16(ms)
}

// appendTrailer appends marker, version and four uint16 BE milliseconds.
func (t resumeTiming) appendTrailer(frame []byte) []byte {
	frame = append(frame, VsockResumeTimingMarker, VsockResumeTimingVersion)
	for _, d := range []time.Duration{t.read, t.kernel, t.reseed, t.afterRestore} {
		frame = binary.BigEndian.AppendUint16(frame, clampMillis(d))
	}
	return frame
}
