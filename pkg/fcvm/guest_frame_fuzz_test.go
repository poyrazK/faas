// adr: 121
package fcvm

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

// countingReader records how many bytes a decoder pulled from guest input.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// Job exit frames are guest bytes read by root vmmd. Whatever the guest
// sends, the decoder must not panic, must read at most one bounded frame, and
// may only return a payload the host validator accepts.
func FuzzReadJobExitEnvelope(f *testing.F) {
	frame := func(msgType, length uint32, body string) []byte {
		b := make([]byte, 8, 8+len(body))
		binary.BigEndian.PutUint32(b[:4], msgType)
		binary.BigEndian.PutUint32(b[4:], length)
		return append(b, body...)
	}
	valid := `{"exit_code":0,"error_class":"","signal":0,"finished_at_unix_nano":1,"lease_token":"00000000-0000-0000-0000-000000000000"}`
	f.Add(frame(uint32(VsockJobExitMsgType), uint32(len(valid)), valid))
	f.Add(frame(uint32(VsockJobExitMsgType), 1<<31, valid))
	f.Add(frame(uint32(VsockJobExitMsgType), 3, "{}x"))
	f.Add(frame(9, 2, "{}"))
	f.Add([]byte{0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		in := &countingReader{r: bytes.NewReader(data)}
		payload, err := readJobExitEnvelope(in)
		if in.n > 8+VsockJobExitMaxBody {
			t.Fatalf("decoder read %d bytes; one frame is at most %d", in.n, 8+VsockJobExitMaxBody)
		}
		if err == nil {
			if verr := validateJobExitPayload(payload); verr != nil {
				t.Fatalf("decoder returned a payload the validator rejects: %v", verr)
			}
		}
	})
}

// Healthcheck reports are guest datagrams. Decoding must not panic, and the
// output it keeps is capped whatever length the frame claims.
func FuzzDecodeHealthcheckReport(f *testing.F) {
	frame := func(olen uint32, out string) []byte {
		b := make([]byte, 13, 13+len(out))
		binary.BigEndian.PutUint32(b[:4], HealthcheckMsgType)
		binary.BigEndian.PutUint32(b[4:8], 7)
		b[8] = 1
		binary.BigEndian.PutUint32(b[9:13], olen)
		return append(b, out...)
	}
	f.Add(frame(2, "ok"))
	f.Add(frame(1<<30, "short"))
	f.Add(frame(VsockHealthcheckMaxOutput+10, string(make([]byte, VsockHealthcheckMaxOutput+10))))
	f.Add([]byte{1, 2, 3})
	f.Fuzz(func(t *testing.T, data []byte) {
		report, err := decodeHealthcheckReport(data)
		if err != nil {
			return
		}
		if len(report.Output) > VsockHealthcheckMaxOutput {
			t.Fatalf("kept %d output bytes; cap is %d", len(report.Output), VsockHealthcheckMaxOutput)
		}
		if len(data) < 13 {
			t.Fatalf("decoded a %d-byte frame shorter than the header", len(data))
		}
	})
}
