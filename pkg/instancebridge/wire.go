// Package instancebridge is the session protocol of an instance's bridge
// (connection-survival ADR §2, phase P1b). One data socket per instance
// carries every kind of traffic: HTTP as H2C, as before, and each raw
// upgrade, TCP or UDP session on its own unix stream connection that
// starts with a hello line. One connection per session gives exact
// backpressure and confines a failure to one session; it also removes the
// process spawn every such connection paid.
//
// A session connection:
//
//	client → bridge  "FAAS-IBRIDGE/1 <kind> <port>\n", then the request
//	                 bytes (raw, tcp) or udpwire frames (udp); the client
//	                 half-closes its write side at the end of the request.
//	bridge → client  "OK\n" once the guest socket is dialed, or
//	                 "ERR <message>\n"; then frames: 'D' + uint32 length +
//	                 bytes, ending with one 'E' + code byte + uint16 length
//	                 + message. Code 0 is a clean end.
//
// The end frame carries what a per-connection bridge's exit status and
// stderr carried, so a guest that hangs up mid-request still surfaces as
// an error rather than as a clean, truncated response.
package instancebridge

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// Hello is the protocol name and major version a session starts with.
const Hello = "FAAS-IBRIDGE/1"

// Session kinds.
const (
	KindRaw = "raw"
	KindTCP = "tcp"
	KindUDP = "udp"
)

// End codes, as the per-connection bridges' exit codes.
const (
	CodeOK   = 0
	CodeDial = 3 // the guest socket could not be dialed
	CodeIO   = 4 // reading or writing the guest socket failed
)

const (
	maxHelloLine   = 64
	maxReadyLine   = 512
	maxFrameBytes  = 64 * 1024
	maxEndMessage  = 1024
	frameData      = 'D'
	frameEnd       = 'E'
	frameHeaderLen = 5
)

// IsHello reports whether a connection whose first bytes are peek starts a
// session rather than an HTTP connection. peek must hold len(Hello) bytes.
func IsHello(peek []byte) bool {
	return len(peek) >= len(Hello) && string(peek[:len(Hello)]) == Hello
}

// WriteHello starts a session of kind to the guest's port.
func WriteHello(w io.Writer, kind string, port uint16) error {
	if !validKind(kind) || port == 0 {
		return fmt.Errorf("instancebridge: invalid session %q port %d", kind, port)
	}
	_, err := io.WriteString(w, Hello+" "+kind+" "+strconv.Itoa(int(port))+"\n")
	return err
}

// ReadHello reads a session's hello.
func ReadHello(r *bufio.Reader) (kind string, port uint16, err error) {
	line, err := readLine(r, maxHelloLine)
	if err != nil {
		return "", 0, err
	}
	fields := strings.Split(line, " ")
	if len(fields) != 3 || fields[0] != Hello || !validKind(fields[1]) {
		return "", 0, fmt.Errorf("instancebridge: unsupported hello %q", line)
	}
	p, err := strconv.ParseUint(fields[2], 10, 16)
	if err != nil || p == 0 || strconv.FormatUint(p, 10) != fields[2] {
		return "", 0, fmt.Errorf("instancebridge: invalid port in hello %q", line)
	}
	return fields[1], uint16(p), nil
}

func validKind(kind string) bool {
	return kind == KindRaw || kind == KindTCP || kind == KindUDP
}

// WriteReady answers a hello: OK, or the dial error.
func WriteReady(w io.Writer, dialErr error) error {
	if dialErr == nil {
		_, err := io.WriteString(w, "OK\n")
		return err
	}
	_, err := io.WriteString(w, "ERR "+oneLine(dialErr.Error(), maxReadyLine-5)+"\n")
	return err
}

// DialError is the bridge's report that it could not dial the guest.
type DialError struct{ Message string }

func (e *DialError) Error() string { return "guest socket dial failed: " + e.Message }

// ReadReady reads the bridge's answer to a hello: nil, or a *DialError.
func ReadReady(r *bufio.Reader) error {
	line, err := readLine(r, maxReadyLine)
	if err != nil {
		return err
	}
	switch {
	case line == "OK":
		return nil
	case strings.HasPrefix(line, "ERR "):
		return &DialError{Message: strings.TrimPrefix(line, "ERR ")}
	}
	return fmt.Errorf("instancebridge: unexpected ready line %q", line)
}

func readLine(r *bufio.Reader, limit int) (string, error) {
	var b []byte
	for len(b) <= limit {
		c, err := r.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return "", err
		}
		if c == '\n' {
			return string(b), nil
		}
		b = append(b, c)
	}
	return "", errors.New("instancebridge: line too long")
}

// oneLine keeps a message on one line and at most n bytes.
func oneLine(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
	if len(s) > n {
		s = s[:n]
	}
	return s
}

// FrameWriter frames the bridge's output to the client. It is safe for
// concurrent use; End is the last call.
type FrameWriter struct {
	mu    sync.Mutex
	w     io.Writer
	ended bool
}

// NewFrameWriter frames output onto w.
func NewFrameWriter(w io.Writer) *FrameWriter { return &FrameWriter{w: w} }

// Write sends p as data frames.
func (f *FrameWriter) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ended {
		return 0, errors.New("instancebridge: write after end")
	}
	written := 0
	for len(p) > 0 {
		n := min(len(p), maxFrameBytes)
		var head [frameHeaderLen]byte
		head[0] = frameData
		binary.BigEndian.PutUint32(head[1:], uint32(n))
		if _, err := f.w.Write(head[:]); err != nil {
			f.ended = true // a partial frame: nothing more may follow it
			return written, err
		}
		if _, err := f.w.Write(p[:n]); err != nil {
			f.ended = true
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

// End sends the end frame with code and message. After a failed write it
// sends nothing: the client then sees the connection end without one.
func (f *FrameWriter) End(code byte, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ended {
		return errors.New("instancebridge: session output already ended")
	}
	f.ended = true
	message = oneLine(message, maxEndMessage)
	b := make([]byte, 4+len(message))
	b[0], b[1] = frameEnd, code
	binary.BigEndian.PutUint16(b[2:], uint16(len(message)))
	copy(b[4:], message)
	_, err := f.w.Write(b)
	return err
}

// SessionError is a session that the bridge ended with a nonzero code.
type SessionError struct {
	Code    byte
	Message string
}

func (e *SessionError) Error() string {
	return fmt.Sprintf("instance bridge session ended with code %d: %s", e.Code, e.Message)
}

// FrameReader reads the bridge's framed output. Read returns io.EOF at a
// clean end frame, the *SessionError of a failed one, and
// io.ErrUnexpectedEOF when the connection ends without one.
type FrameReader struct {
	r    *bufio.Reader
	left uint32
	err  error
}

// NewFrameReader reads frames from r.
func NewFrameReader(r *bufio.Reader) *FrameReader { return &FrameReader{r: r} }

func (f *FrameReader) Read(p []byte) (int, error) {
	for f.left == 0 {
		if f.err != nil {
			return 0, f.err
		}
		if err := f.next(); err != nil {
			f.err = err
			return 0, err
		}
	}
	n := min(len(p), int(f.left))
	n, err := f.r.Read(p[:n])
	f.left -= uint32(n)
	if err != nil && !errors.Is(err, io.EOF) {
		f.err = err
		return n, err
	}
	if errors.Is(err, io.EOF) && f.left > 0 {
		f.err = io.ErrUnexpectedEOF
		return n, f.err
	}
	return n, nil
}

// next reads a frame header: a data frame sets left, an end frame sets err.
func (f *FrameReader) next() error {
	kind, err := f.r.ReadByte()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	switch kind {
	case frameData:
		var n [4]byte
		if _, err := io.ReadFull(f.r, n[:]); err != nil {
			return io.ErrUnexpectedEOF
		}
		size := binary.BigEndian.Uint32(n[:])
		if size == 0 || size > maxFrameBytes {
			return fmt.Errorf("instancebridge: data frame of %d bytes", size)
		}
		f.left = size
		return nil
	case frameEnd:
		var head [3]byte
		if _, err := io.ReadFull(f.r, head[:]); err != nil {
			return io.ErrUnexpectedEOF
		}
		size := binary.BigEndian.Uint16(head[1:])
		if size > maxEndMessage {
			return fmt.Errorf("instancebridge: end message of %d bytes", size)
		}
		msg := make([]byte, size)
		if _, err := io.ReadFull(f.r, msg); err != nil {
			return io.ErrUnexpectedEOF
		}
		if head[0] == CodeOK {
			return io.EOF
		}
		return &SessionError{Code: head[0], Message: string(msg)}
	}
	return fmt.Errorf("instancebridge: unknown frame %q", kind)
}
