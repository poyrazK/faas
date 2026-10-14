package resumetimingproto

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	want := Timings{
		ReadUs: 40, EntropyUs: 2100, HWRNGUs: 1600, ClockUs: 100, UUIDMarkerUs: 5400,
		UserspaceReseedUs: 120000, ReseedClients: 2, ReseedSlowestUs: 118000, ReseedSlowestRuntime: "node",
		ProfilesUs: 10, AfterRestoreUs: 0, TotalUs: 130000,
	}
	frame, err := AppendFrame([]byte{0, 0x03}, want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(frame, []byte{0, 0x03}) {
		t.Fatalf("AppendFrame overwrote the prefix: %v", frame[:2])
	}
	got, err := ReadFrame(bytes.NewReader(frame[2:]))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

func TestReadFrameRejectsMalformed(t *testing.T) {
	lengthOnly := func(n uint32) []byte { return binary.BigEndian.AppendUint32(nil, n) }
	cases := []struct {
		name  string
		frame []byte
		want  string
	}{
		{name: "empty", frame: nil, want: "read length"},
		{name: "zero length", frame: lengthOnly(0), want: "outside"},
		{name: "oversized", frame: lengthOnly(MaxBodyBytes + 1), want: "outside"},
		{name: "truncated body", frame: append(lengthOnly(10), `{"tot`...), want: "read body"},
		{name: "not json", frame: append(lengthOnly(3), "abc"...), want: "decode"},
		{name: "negative total", frame: append(lengthOnly(15), `{"total_us":-1}`...), want: "negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadFrame(bytes.NewReader(tc.frame))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ReadFrame err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestCapTimingsDoesNotOverlapReseedCapability(t *testing.T) {
	const reseedCapability = 0x01 // ADR-687, guest/init VsockResumeCapUserspaceReseed
	if CapTimings&reseedCapability != 0 {
		t.Fatalf("CapTimings %#x overlaps the reseed capability bit", CapTimings)
	}
}
