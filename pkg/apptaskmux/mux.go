// Package apptaskmux multiplexes TCP connections over the stdin/stdout byte
// streams of one interactive app-task session (ADR-958). `gregale app
// port-forward` sends Open/Data/Close frames to the guest's relay built-in,
// which dials the target from inside the app's network and answers with
// Data/Close frames.
//
// Frame: type (1 byte) | stream id (4 bytes, big-endian) | length (4 bytes,
// big-endian) | payload. Open and EOF carry no payload; Close may carry a
// short reason. EOF half-closes one direction (the sender will write no
// more); Close aborts the stream in both directions.
package apptaskmux

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

const (
	FrameOpen  byte = 1
	FrameData  byte = 2
	FrameClose byte = 3
	FrameEOF   byte = 4

	// MaxPayload bounds one frame; larger writes are split.
	MaxPayload = 32 * 1024
	// MaxStreams bounds concurrently open streams in one session.
	MaxStreams = 64

	headerBytes = 9
)

var ErrMalformed = errors.New("apptaskmux: malformed frame")

// Frame is one decoded mux frame.
type Frame struct {
	Type    byte
	Stream  uint32
	Payload []byte
}

// Writer serializes frames onto one byte stream.
type Writer struct {
	mu sync.Mutex
	w  io.Writer
}

func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// Open announces a new stream.
func (w *Writer) Open(stream uint32) error { return w.write(FrameOpen, stream, nil) }

// EOF tells the peer this side will send no more data on stream.
func (w *Writer) EOF(stream uint32) error { return w.write(FrameEOF, stream, nil) }

// Close aborts a stream in both directions.
func (w *Writer) Close(stream uint32, reason string) error {
	if len(reason) > 256 {
		reason = reason[:256]
	}
	return w.write(FrameClose, stream, []byte(reason))
}

// Data sends payload on a stream, split into MaxPayload frames.
func (w *Writer) Data(stream uint32, payload []byte) error {
	for len(payload) > 0 {
		n := min(len(payload), MaxPayload)
		if err := w.write(FrameData, stream, payload[:n]); err != nil {
			return err
		}
		payload = payload[n:]
	}
	return nil
}

func (w *Writer) write(kind byte, stream uint32, payload []byte) error {
	var header [headerBytes]byte
	header[0] = kind
	binary.BigEndian.PutUint32(header[1:5], stream)
	binary.BigEndian.PutUint32(header[5:9], uint32(len(payload)))
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.w.Write(header[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.w.Write(payload)
	return err
}

// Reader decodes frames from one byte stream, regardless of how the stream
// was chunked in transit.
type Reader struct {
	r *bufio.Reader
}

func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReaderSize(r, MaxPayload+headerBytes)}
}

// Next returns the next frame, or io.EOF at a clean end of stream.
func (r *Reader) Next() (Frame, error) {
	var header [headerBytes]byte
	if _, err := io.ReadFull(r.r, header[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return Frame{}, fmt.Errorf("%w: truncated header", ErrMalformed)
		}
		return Frame{}, err
	}
	frame := Frame{Type: header[0], Stream: binary.BigEndian.Uint32(header[1:5])}
	length := binary.BigEndian.Uint32(header[5:9])
	switch {
	case frame.Type != FrameOpen && frame.Type != FrameData && frame.Type != FrameClose && frame.Type != FrameEOF:
		return Frame{}, fmt.Errorf("%w: type %d", ErrMalformed, frame.Type)
	case length > MaxPayload:
		return Frame{}, fmt.Errorf("%w: %d-byte payload", ErrMalformed, length)
	case (frame.Type == FrameOpen || frame.Type == FrameEOF) && length != 0:
		return Frame{}, fmt.Errorf("%w: open or eof with payload", ErrMalformed)
	}
	if length > 0 {
		frame.Payload = make([]byte, length)
		if _, err := io.ReadFull(r.r, frame.Payload); err != nil {
			return Frame{}, fmt.Errorf("%w: truncated payload", ErrMalformed)
		}
	}
	return frame, nil
}
