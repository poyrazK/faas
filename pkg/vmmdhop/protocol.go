// Package vmmdhop carries gatewayd-internal's ForwardHTTPStream calls to vmmd
// over a light framed connection instead of gRPC (ADR-20261121202814).
//
// The connection is TLS on vmmd's gRPC port, chosen by the ALPN protocol
// ALPN. Each exchange carries the same protobuf messages the gRPC stream
// would, and vmmd serves it with the same handler and caller-policy
// interceptor, so only the transport changes:
//
//	client: preamble (version, full method, metadata)
//	        frameMessage* frameCloseSend
//	server: frameMessage* (frameStatus | frameStatusClose), a google.rpc.Status
//
// A frame is a type byte, a varint length and the payload. One exchange runs
// on a connection at a time; a connection is reused only after a clean one.
package vmmdhop

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// ALPN is the TLS application protocol that selects the hop on vmmd's port.
const ALPN = "gregale-vmmd-hop/1"

const protocolVersion = 1

// Frame types.
const (
	frameMessage   = 1 // a marshaled protobuf message, either direction
	frameCloseSend = 2 // client: no more messages in this exchange
	frameStatus    = 3 // server: the final google.rpc.Status; the connection stays open
	// frameStatusClose is frameStatus after which the server closes the
	// connection: the exchange did not end cleanly on its side (the handler
	// returned before the client's close-send, or the client broke framing).
	frameStatusClose = 4
)

// Limits (ADR-20261121202814, security requirement 3).
const (
	// MaxMessageBytes matches gRPC's default receive limit on both ends.
	MaxMessageBytes = 4 << 20
	// maxStatusBytes bounds the final status, details included.
	maxStatusBytes = 64 << 10
	// maxMetadataBytes bounds the preamble's metadata keys and values.
	maxMetadataBytes = 64 << 10
	// maxMethodBytes bounds the method name.
	maxMethodBytes = 256
	// handshakeTimeout bounds the ClientHello peek and the TLS handshake.
	handshakeTimeout = 10 * time.Second
	// serverIdleTimeout ends a connection that starts no exchange.
	serverIdleTimeout = 2 * time.Minute
	// clientIdleTimeout keeps the client's pool inside the server's
	// timeout, so the server rarely closes a connection the client reuses.
	clientIdleTimeout = 60 * time.Second
)

var errMalformed = errors.New("vmmdhop: malformed frame")

// writeFrame writes one frame. It does not flush.
func writeFrame(w *bufio.Writer, typ byte, payload []byte) error {
	if err := w.WriteByte(typ); err != nil {
		return err
	}
	var buf [binary.MaxVarintLen64]byte
	if _, err := w.Write(buf[:binary.PutUvarint(buf[:], uint64(len(payload)))]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// readFrame reads one frame whose payload is at most max bytes. A frame
// longer than max, or a truncated one, is an error: the caller closes the
// connection.
func readFrame(r *bufio.Reader, max int) (byte, []byte, error) {
	typ, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return 0, nil, noEOF(err)
	}
	if n > uint64(max) {
		return 0, nil, fmt.Errorf("%w: %d-byte payload over the %d-byte limit", errMalformed, n, max)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, noEOF(err)
	}
	return typ, payload, nil
}

func noEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

func writeString(w *bufio.Writer, s string) {
	var buf [binary.MaxVarintLen64]byte
	_, _ = w.Write(buf[:binary.PutUvarint(buf[:], uint64(len(s)))])
	_, _ = w.WriteString(s)
}

// readString reads a length-prefixed string, spending its bytes from budget.
func readString(r *bufio.Reader, budget *int) (string, error) {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return "", noEOF(err)
	}
	if n > uint64(*budget) {
		return "", fmt.Errorf("%w: field over its limit", errMalformed)
	}
	*budget -= int(n)
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", noEOF(err)
	}
	return string(buf), nil
}

// writePreamble starts an exchange: the protocol version, the full method
// and the metadata gRPC would have sent as headers. It does not flush.
func writePreamble(w *bufio.Writer, method string, md metadata.MD) error {
	if err := w.WriteByte(protocolVersion); err != nil {
		return err
	}
	writeString(w, method)
	pairs := 0
	for _, values := range md {
		pairs += len(values)
	}
	var buf [binary.MaxVarintLen64]byte
	_, _ = w.Write(buf[:binary.PutUvarint(buf[:], uint64(pairs))])
	for key, values := range md {
		for _, v := range values {
			writeString(w, key)
			writeString(w, v)
		}
	}
	_, err := w.Write(nil)
	return err
}

// readPreamble reads an exchange's start. io.EOF means the client closed an
// idle connection.
func readPreamble(r *bufio.Reader) (string, metadata.MD, error) {
	version, err := r.ReadByte()
	if err != nil {
		return "", nil, err
	}
	if version != protocolVersion {
		return "", nil, fmt.Errorf("%w: protocol version %d", errMalformed, version)
	}
	methodBudget := maxMethodBytes
	method, err := readString(r, &methodBudget)
	if err != nil {
		return "", nil, err
	}
	pairs, err := binary.ReadUvarint(r)
	if err != nil {
		return "", nil, noEOF(err)
	}
	if pairs > maxMetadataBytes/2 {
		return "", nil, fmt.Errorf("%w: %d metadata pairs", errMalformed, pairs)
	}
	budget := maxMetadataBytes
	md := make(metadata.MD, pairs)
	for range pairs {
		key, err := readString(r, &budget)
		if err != nil {
			return "", nil, err
		}
		value, err := readString(r, &budget)
		if err != nil {
			return "", nil, err
		}
		if !validMetadataKey(key) {
			return "", nil, fmt.Errorf("%w: metadata key %q", errMalformed, key)
		}
		// metadata.MD keys are lower case; gRPC lower-cases what it receives.
		key = strings.ToLower(key)
		md[key] = append(md[key], value)
	}
	return method, md, nil
}

// validMetadataKey accepts what gRPC accepts as a header key: printable
// ASCII token characters, and not a reserved pseudo-header.
func validMetadataKey(key string) bool {
	if key == "" || key[0] == ':' {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c <= ' ' || c >= 0x7f || strings.IndexByte(`"(),/:;<=>?@[\]{}`, c) >= 0 {
			return false
		}
	}
	return true
}

// decodeStatus reads the google.rpc.Status that ends an exchange.
func decodeStatus(b []byte) (*status.Status, error) {
	var st spb.Status
	if err := proto.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	return status.FromProto(&st), nil
}
