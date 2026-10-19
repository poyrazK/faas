// Package relaywire is the preamble vmmd writes at the start of an app relay
// connection to outboundd (ADR-989). After the preamble the connection
// carries the guest's HTTP bytes unchanged, so this is the only framing
// either side owns.
package relaywire

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MaxPreambleBytes bounds the JSON preamble. A workload identity assertion
// is at most 8 KiB, so the frame leaves room for the envelope.
const MaxPreambleBytes = 12 << 10

// Preamble carries the vmmd-minted relay assertion for the connection. For a
// Run (ADR-990) it also lists the integrations granted to that Run; the
// mTLS channel from vmmd authenticates the list like the assertion itself.
type Preamble struct {
	RecorderRequestID     string   `json:"recorder_request_id,omitempty"`
	Assertion             string   `json:"assertion"`
	ExecutionIntegrations []string `json:"execution_integrations,omitempty"`
}

// Write encodes one length-prefixed preamble frame.
func Write(w io.Writer, p Preamble) error {
	if p.Assertion == "" {
		return errors.New("relay preamble: assertion is required")
	}
	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("relay preamble: encode: %w", err)
	}
	if len(body) > MaxPreambleBytes {
		return fmt.Errorf("relay preamble: %d bytes exceeds %d", len(body), MaxPreambleBytes)
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(body)))
	copy(frame[4:], body)
	if _, err := w.Write(frame); err != nil {
		return fmt.Errorf("relay preamble: write: %w", err)
	}
	return nil
}

// Read decodes exactly one preamble frame and nothing after it, so the
// caller can hand the rest of the stream to an HTTP server.
func Read(r io.Reader) (Preamble, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Preamble{}, fmt.Errorf("relay preamble: read length: %w", err)
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > MaxPreambleBytes {
		return Preamble{}, fmt.Errorf("relay preamble: invalid length %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return Preamble{}, fmt.Errorf("relay preamble: read body: %w", err)
	}
	var p Preamble
	if err := json.Unmarshal(body, &p); err != nil || p.Assertion == "" {
		return Preamble{}, errors.New("relay preamble: invalid body")
	}
	return p, nil
}
