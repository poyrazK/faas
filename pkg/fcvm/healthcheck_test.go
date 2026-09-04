//go:build !metal

// Healthcheck wire-format decoder unit tests
// (M-2 / ADR-139 §Decision 1, //code-review PR #1202 finding #3).
//
// Pure-Go tests so they run on any host (no AF_VSOCK). The
// decoder mirrors guest/init/healthcheck_linux.go's wire shape;
// these tests pin every byte of the frame layout so a future
// drift surfaces as a unit-test failure rather than a silent
// "no healthcheck reports" in production.

package fcvm

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// fakeFrameReader yields canned DGRAM frames for the
// drainHealthcheckFrames test. Each Read returns one frame from
// the queue; when the queue is empty it returns io.EOF.
type fakeFrameReader struct {
	frames [][]byte
	idx    int
}

func (f *fakeFrameReader) Read(ctx context.Context) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if f.idx >= len(f.frames) {
		return nil, io.EOF
	}
	frame := f.frames[f.idx]
	f.idx++
	return frame, nil
}

// encodeTestFrame stamps a HealthcheckReport frame in the same
// shape guest-init's sendHealthcheckReport produces (mirrors
// guest/init/healthcheck_linux.go's encodeHealthcheckReport).
func encodeTestFrame(seq uint32, status byte, output []byte) []byte {
	const hdrLen = 13
	buf := make([]byte, 0, hdrLen+len(output))
	hdr := make([]byte, hdrLen)
	binary.BigEndian.PutUint32(hdr[0:4], HealthcheckMsgType)
	binary.BigEndian.PutUint32(hdr[4:8], seq)
	hdr[8] = status
	binary.BigEndian.PutUint32(hdr[9:13], uint32(len(output)))
	buf = append(buf, hdr...)
	buf = append(buf, output...)
	return buf
}

func TestDecodeHealthcheckReport_Happy(t *testing.T) {
	out := []byte("ok\n")
	frame := encodeTestFrame(7, HealthcheckStatusPass, out)
	report, err := decodeHealthcheckReport(frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if report.Seq != 7 {
		t.Errorf("seq = %d, want 7", report.Seq)
	}
	if report.Status != HealthcheckStatusPass {
		t.Errorf("status = %d, want %d", report.Status, HealthcheckStatusPass)
	}
	if !bytes.Equal(report.Output, out) {
		t.Errorf("output = %q, want %q", report.Output, out)
	}
}

func TestDecodeHealthcheckReport_TruncatedFrame(t *testing.T) {
	short := make([]byte, 12) // < 13-byte header
	if _, err := decodeHealthcheckReport(short); !errors.Is(err, ErrHealthcheckFrameTooShort) {
		t.Errorf("err = %v, want ErrHealthcheckFrameTooShort", err)
	}
}

func TestDecodeHealthcheckReport_BadMsgType(t *testing.T) {
	frame := encodeTestFrame(0, HealthcheckStatusPass, nil)
	binary.BigEndian.PutUint32(frame[0:4], 0xDEADBEEF)
	if _, err := decodeHealthcheckReport(frame); !errors.Is(err, ErrHealthcheckMsgType) {
		t.Errorf("err = %v, want ErrHealthcheckMsgType", err)
	}
}

func TestDecodeHealthcheckReport_TruncatedPayload(t *testing.T) {
	// Header says 100 bytes payload but we only ship 3.
	hdr := make([]byte, 13)
	binary.BigEndian.PutUint32(hdr[0:4], HealthcheckMsgType)
	hdr[8] = HealthcheckStatusPass
	binary.BigEndian.PutUint32(hdr[9:13], 100)
	frame := append(hdr, []byte("abc")...)
	if _, err := decodeHealthcheckReport(frame); err == nil {
		t.Errorf("decode of truncated payload returned nil err")
	}
}

func TestDecodeHealthcheckReport_OverCapOutputTruncated(t *testing.T) {
	// Header claims > VsockHealthcheckMaxOutput bytes. The decoder
	// must clamp olen rather than error out — the tail bytes are
	// the most-recent output (most useful for diagnostics).
	over := make([]byte, VsockHealthcheckMaxOutput+100)
	for i := range over {
		over[i] = 'x'
	}
	hdr := make([]byte, 13)
	binary.BigEndian.PutUint32(hdr[0:4], HealthcheckMsgType)
	hdr[8] = HealthcheckStatusPass
	binary.BigEndian.PutUint32(hdr[9:13], uint32(len(over)))
	frame := append(hdr, over...)
	report, err := decodeHealthcheckReport(frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if uint32(len(report.Output)) != VsockHealthcheckMaxOutput {
		t.Errorf("output len = %d, want %d (clamped)", len(report.Output), VsockHealthcheckMaxOutput)
	}
}

func TestDrainHealthcheckFrames_FirstPassPinned(t *testing.T) {
	r := &fakeFrameReader{frames: [][]byte{
		encodeTestFrame(0, HealthcheckStatusStarting, []byte("warming up")),
		encodeTestFrame(1, HealthcheckStatusFail, []byte("still booting")),
		encodeTestFrame(2, HealthcheckStatusPass, []byte("ok")),
		encodeTestFrame(3, HealthcheckStatusPass, []byte("still ok")),
	}}
	count, firstPass, err := drainHealthcheckFrames(context.Background(), r)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if count != 4 {
		t.Errorf("count = %d, want 4", count)
	}
	if firstPass.Seq != 2 {
		t.Errorf("firstPass.Seq = %d, want 2 (first pass after warmup)", firstPass.Seq)
	}
	if firstPass.Status != HealthcheckStatusPass {
		t.Errorf("firstPass.Status = %d, want %d", firstPass.Status, HealthcheckStatusPass)
	}
	if string(firstPass.Output) != "ok" {
		t.Errorf("firstPass.Output = %q, want %q", firstPass.Output, "ok")
	}
}

func TestDrainHealthcheckFrames_MalformedDropped(t *testing.T) {
	malformed := make([]byte, 5) // too short
	r := &fakeFrameReader{frames: [][]byte{
		malformed,
		encodeTestFrame(0, HealthcheckStatusPass, []byte("ok")),
	}}
	count, firstPass, err := drainHealthcheckFrames(context.Background(), r)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (malformed dropped)", count)
	}
	if firstPass.Seq != 0 {
		t.Errorf("firstPass.Seq = %d, want 0", firstPass.Seq)
	}
}

func TestDrainHealthcheckFrames_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled
	r := &fakeFrameReader{frames: [][]byte{
		encodeTestFrame(0, HealthcheckStatusPass, nil),
	}}
	_, _, err := drainHealthcheckFrames(ctx, r)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
