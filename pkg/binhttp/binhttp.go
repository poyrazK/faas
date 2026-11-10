// Package binhttp reads and writes HTTP messages in binary HTTP (RFC 9292),
// for the compute edge's hop to its own node's gatewayd-internal
// (ADR-20261110081523).
//
// A request with no content is written in the known-length form, so the
// receiver knows at once that no body follows; any other request, and every
// response, in the indeterminate-length form, whose content is a run of
// chunks that a sender may flush as it goes. Several messages follow one
// another on a connection, so none carries padding.
//
// Field names go out as the sender's header map holds them, which for
// net/http is canonical form, and are read back in canonical form. Writing
// and reading work in place on the bufio buffers: the hop exists to cost less
// than HTTP/1.1's text, so it must not allocate more than that does.
package binhttp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"strings"
)

// Framing indicators (RFC 9292 §3.3).
const (
	knownLengthRequest    = 0
	indeterminateRequest  = 2
	indeterminateResponse = 3
)

// ErrHeaderTooLarge reports a message head beyond the reader's limit.
var ErrHeaderTooLarge = errors.New("binhttp: header fields too large")

// errMalformed reports a message that is not binary HTTP.
var errMalformed = errors.New("binhttp: malformed message")

// maxVarint is the largest value a variable-length integer holds.
const maxVarint = 1<<62 - 1

// Field is one field line.
type Field struct{ Name, Value string }

// appendVarint appends v as a QUIC variable-length integer (RFC 9000 §16).
func appendVarint(b []byte, v uint64) []byte {
	switch {
	case v < 1<<6:
		return append(b, byte(v))
	case v < 1<<14:
		return append(b, byte(v>>8)|0x40, byte(v))
	case v < 1<<30:
		return append(b, byte(v>>24)|0x80, byte(v>>16), byte(v>>8), byte(v))
	default:
		return append(b, byte(v>>56)|0xc0, byte(v>>48), byte(v>>40), byte(v>>32), byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	}
}

// varintLen is the encoded size of v.
func varintLen(v uint64) int {
	switch {
	case v < 1<<6:
		return 1
	case v < 1<<14:
		return 2
	case v < 1<<30:
		return 4
	default:
		return 8
	}
}

// writeVarint writes v byte by byte: a buffer passed to Write would escape
// to the heap on every call.
func writeVarint(w *bufio.Writer, v uint64) {
	n := varintLen(v)
	var prefix byte
	switch n {
	case 1:
		_ = w.WriteByte(byte(v))
		return
	case 2:
		prefix = 0x40
	case 4:
		prefix = 0x80
	default:
		prefix = 0xc0
	}
	_ = w.WriteByte(byte(v>>(8*(n-1))) | prefix)
	for i := n - 2; i >= 0; i-- {
		_ = w.WriteByte(byte(v >> (8 * i)))
	}
}

func writeString(w *bufio.Writer, s string) {
	writeVarint(w, uint64(len(s)))
	_, _ = w.WriteString(s)
}

// readVarint reads one variable-length integer and reports its size.
func readVarint(r io.ByteReader) (uint64, int, error) {
	first, err := r.ReadByte()
	if err != nil {
		return 0, 0, err
	}
	n := 1 << (first >> 6)
	v := uint64(first & 0x3f)
	for i := 1; i < n; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, 0, noEOF(err)
		}
		v = v<<8 | uint64(b)
	}
	return v, n, nil
}

// noEOF turns an EOF inside a message into ErrUnexpectedEOF.
func noEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// RequestHead is a request's control data and header fields.
type RequestHead struct {
	Method, Scheme, Authority, Path string
	Header                          http.Header
	// NoContent writes the known-length form with empty content and no
	// trailers: the receiver knows at once that no body follows.
	NoContent bool
}

// WriteRequestHead writes h. Unless h.NoContent, the content follows as
// chunks (ChunkWriter), then the trailers (WriteTrailers). A write error is
// the bufio.Writer's, which keeps it for the next write or Flush.
func WriteRequestHead(w *bufio.Writer, h RequestHead) error {
	if h.NoContent {
		writeVarint(w, knownLengthRequest)
	} else {
		writeVarint(w, indeterminateRequest)
	}
	writeString(w, h.Method)
	writeString(w, h.Scheme)
	writeString(w, h.Authority)
	writeString(w, h.Path)
	if h.NoContent {
		writeVarint(w, uint64(fieldsLen(h.Header)))
		writeFields(w, h.Header, nil, nil)
		writeVarint(w, 0) // content
		writeVarint(w, 0) // trailers
	} else {
		writeFields(w, h.Header, nil, nil)
		writeVarint(w, 0)
	}
	_, err := w.Write(nil)
	return err
}

// ReadRequestHead reads a request's head. maxHeaderBytes bounds the control
// data and header fields together. With NoContent set, the whole message has
// been read; otherwise its content follows (ChunkReader). It returns io.EOF
// when the connection ends before a message starts.
func ReadRequestHead(r *bufio.Reader, maxHeaderBytes int) (RequestHead, error) {
	framing, _, err := readVarint(r)
	if err != nil {
		return RequestHead{}, err
	}
	if framing != knownLengthRequest && framing != indeterminateRequest {
		return RequestHead{}, fmt.Errorf("%w: framing indicator %d is not a request", errMalformed, framing)
	}
	budget := maxHeaderBytes
	var h RequestHead
	if h.Method, err = readString(r, &budget, false); err != nil {
		return RequestHead{}, err
	}
	if h.Scheme, err = readString(r, &budget, false); err != nil {
		return RequestHead{}, err
	}
	if h.Authority, err = readString(r, &budget, false); err != nil {
		return RequestHead{}, err
	}
	if h.Path, err = readString(r, &budget, false); err != nil {
		return RequestHead{}, err
	}
	if framing == indeterminateRequest {
		h.Header, err = readFields(r, &budget)
		return h, err
	}
	h.NoContent = true
	if h.Header, err = readKnownFields(r, &budget); err != nil {
		return RequestHead{}, err
	}
	for _, part := range [...]string{"content", "trailers"} {
		n, _, err := readVarint(r)
		if err != nil {
			return RequestHead{}, noEOF(err)
		}
		if n != 0 {
			return RequestHead{}, fmt.Errorf("%w: a known-length request with %s is not supported", errMalformed, part)
		}
	}
	return h, nil
}

// WriteResponseStart writes the framing indicator of a response. Each
// informational and the final response head follow (WriteResponseHead).
func WriteResponseStart(w *bufio.Writer) error {
	return w.WriteByte(indeterminateResponse)
}

// WriteResponseHead writes one response head: an informational response
// (1xx) or the final one, whose content follows as chunks. Fields of header
// that omit reports are left out (omit may be nil), and extra are added.
func WriteResponseHead(w *bufio.Writer, status int, header http.Header, omit func(header http.Header, name string) bool, extra ...Field) error {
	writeVarint(w, uint64(status))
	writeFields(w, header, omit, extra)
	writeVarint(w, 0)
	_, err := w.Write(nil)
	return err
}

// ReadResponseStart reads a response's framing indicator.
func ReadResponseStart(r *bufio.Reader) error {
	framing, _, err := readVarint(r)
	if err != nil {
		return err
	}
	if framing != indeterminateResponse {
		return fmt.Errorf("%w: framing indicator %d is not an indeterminate-length response", errMalformed, framing)
	}
	return nil
}

// ReadResponseHead reads one response head. A 1xx status is informational:
// another head follows.
func ReadResponseHead(r *bufio.Reader, maxHeaderBytes int) (int, http.Header, error) {
	status, _, err := readVarint(r)
	if err != nil {
		return 0, nil, noEOF(err)
	}
	if status < 100 || status > 999 {
		return 0, nil, fmt.Errorf("%w: status %d", errMalformed, status)
	}
	budget := maxHeaderBytes
	header, err := readFields(r, &budget)
	return int(status), header, err
}

// WriteTrailers writes the trailer field section that ends a message whose
// content was chunked.
func WriteTrailers(w *bufio.Writer, trailer http.Header) error {
	writeFields(w, trailer, nil, nil)
	writeVarint(w, 0)
	_, err := w.Write(nil)
	return err
}

// ReadTrailers reads the trailer field section after chunked content.
func ReadTrailers(r *bufio.Reader, maxBytes int) (http.Header, error) {
	budget := maxBytes
	return readFields(r, &budget)
}

// writeFields writes each field line of h that omit keeps, then extra.
func writeFields(w *bufio.Writer, h http.Header, omit func(http.Header, string) bool, extra []Field) {
	for name, values := range h {
		if omit != nil && omit(h, name) {
			continue
		}
		for _, v := range values {
			writeString(w, name)
			writeString(w, v)
		}
	}
	for _, f := range extra {
		writeString(w, f.Name)
		writeString(w, f.Value)
	}
}

// fieldsLen is the encoded size of h's field lines.
func fieldsLen(h http.Header) int {
	n := 0
	for name, values := range h {
		for _, v := range values {
			n += varintLen(uint64(len(name))) + len(name) + varintLen(uint64(len(v))) + len(v)
		}
	}
	return n
}

// readString reads a length-prefixed string, spending its prefix and bytes
// from budget. A name that is a common field comes back without allocating.
func readString(r *bufio.Reader, budget *int, name bool) (string, error) {
	n, size, err := readVarint(r)
	if err != nil {
		return "", noEOF(err)
	}
	if n > uint64(*budget) || uint64(size)+n > uint64(*budget) {
		return "", ErrHeaderTooLarge
	}
	*budget -= size + int(n)
	if n == 0 {
		return "", nil
	}
	if int(n) <= r.Size() {
		b, err := r.Peek(int(n))
		if err != nil {
			return "", noEOF(err)
		}
		var s string
		if common, ok := commonFields[string(b)]; name && ok {
			s = common
		} else {
			s = string(b)
		}
		_, _ = r.Discard(int(n))
		return s, nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", noEOF(err)
	}
	return string(buf), nil
}

// readFields reads an indeterminate-length field section: field lines up to
// a zero-length name.
func readFields(r *bufio.Reader, budget *int) (http.Header, error) {
	var h http.Header
	var values []string
	for {
		name, err := readString(r, budget, true)
		if err != nil {
			return nil, err
		}
		if name == "" {
			if h == nil {
				h = http.Header{}
			}
			return h, nil
		}
		value, err := readString(r, budget, false)
		if err != nil {
			return nil, err
		}
		if h == nil {
			h = make(http.Header, 8)
			values = make([]string, 0, 16)
		}
		if values, err = addField(h, values, name, value); err != nil {
			return nil, err
		}
	}
}

// readKnownFields reads a known-length field section, whose lines must fill
// its length exactly.
func readKnownFields(r *bufio.Reader, budget *int) (http.Header, error) {
	n, size, err := readVarint(r)
	if err != nil {
		return nil, noEOF(err)
	}
	if n > uint64(*budget) || uint64(size)+n > uint64(*budget) {
		return nil, ErrHeaderTooLarge
	}
	*budget -= size + int(n)
	left := int(n)
	h := make(http.Header, 8)
	values := make([]string, 0, 16)
	for left > 0 {
		name, err := readString(r, &left, true)
		if err != nil {
			return nil, sectionErr(err)
		}
		value, err := readString(r, &left, false)
		if err != nil {
			return nil, sectionErr(err)
		}
		if values, err = addField(h, values, name, value); err != nil {
			return nil, err
		}
	}
	return h, nil
}

// sectionErr reports a field line that overruns its known-length section.
func sectionErr(err error) error {
	if errors.Is(err, ErrHeaderTooLarge) {
		return fmt.Errorf("%w: a field line overruns its section", errMalformed)
	}
	return err
}

// addField adds a field line under its canonical name, rejecting a name or
// value net/http would not accept. A name's first value is cut from values,
// one backing array for the whole section as net/textproto uses, so each
// field does not allocate a slice of its own.
func addField(h http.Header, values []string, name, value string) ([]string, error) {
	if !validFieldName(name) {
		return values, fmt.Errorf("%w: field name %q", errMalformed, name)
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return values, fmt.Errorf("%w: field %q has a control character in its value", errMalformed, name)
	}
	key := textproto.CanonicalMIMEHeaderKey(name)
	if prev, ok := h[key]; ok {
		h[key] = append(prev, value)
		return values, nil
	}
	if len(values) == cap(values) {
		values = make([]string, 0, 16)
	}
	values = append(values, value)
	h[key] = values[len(values)-1 : len(values) : len(values)]
	return values, nil
}

func validFieldName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c <= ' ' || c >= 0x7f || strings.IndexByte(`"(),/:;<=>?@[\]{}`, c) >= 0 {
			return false
		}
	}
	return true
}

// commonFields interns the field names the hop carries on most requests and
// responses.
var commonFields = func() map[string]string {
	m := map[string]string{}
	for _, name := range []string{
		"Accept", "Accept-Encoding", "Accept-Language", "Authorization", "Cache-Control",
		"Connection", "Content-Encoding", "Content-Length", "Content-Type", "Cookie",
		"Date", "Etag", "Host", "If-Modified-Since", "If-None-Match", "Last-Modified",
		"Location", "Origin", "Referer", "Set-Cookie", "Trailer", "Traceparent", "Tracestate",
		"User-Agent", "Vary", "X-Content-Type-Options", "X-Forwarded-For", "X-Forwarded-Host",
		"X-Forwarded-Proto", "X-Real-Ip", "X-Request-Id", "X-Faas-Edge-Placement", "Grpc-Status",
	} {
		m[name] = name
	}
	return m
}()

// ChunkWriter writes content as chunks. Each Write is one chunk; Close
// writes the terminator. It does not flush the underlying writer. The zero
// value is ready after Reset.
type ChunkWriter struct {
	w      *bufio.Writer
	closed bool
}

// NewChunkWriter writes chunks to w.
func NewChunkWriter(w *bufio.Writer) *ChunkWriter { return &ChunkWriter{w: w} }

// Reset makes c write a new message's content to w.
func (c *ChunkWriter) Reset(w *bufio.Writer) { *c = ChunkWriter{w: w} }

func (c *ChunkWriter) Write(p []byte) (int, error) {
	if c.closed {
		return 0, errors.New("binhttp: write after the content's end")
	}
	if len(p) == 0 {
		return 0, nil
	}
	writeVarint(c.w, uint64(len(p)))
	return c.w.Write(p)
}

// Close writes the content terminator.
func (c *ChunkWriter) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	return c.w.WriteByte(0)
}

// ChunkReader reads chunked content up to its terminator, then io.EOF. The
// zero value is ready after Reset.
type ChunkReader struct {
	r    *bufio.Reader
	left uint64
	done bool
	err  error
}

// NewChunkReader reads chunks from r.
func NewChunkReader(r *bufio.Reader) *ChunkReader { return &ChunkReader{r: r} }

// Reset makes c read a new message's content from r.
func (c *ChunkReader) Reset(r *bufio.Reader) { *c = ChunkReader{r: r} }

func (c *ChunkReader) Read(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	if c.done {
		return 0, io.EOF
	}
	for c.left == 0 {
		n, _, err := readVarint(c.r)
		if err != nil {
			c.err = noEOF(err)
			return 0, c.err
		}
		if n == 0 {
			c.done = true
			return 0, io.EOF
		}
		if n > maxVarint {
			c.err = errMalformed
			return 0, c.err
		}
		c.left = n
	}
	if uint64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= uint64(n)
	if err != nil {
		c.err = noEOF(err)
		if n > 0 {
			return n, nil
		}
		return 0, c.err
	}
	return n, nil
}

// Done reports whether the terminator has been read.
func (c *ChunkReader) Done() bool { return c.done }
