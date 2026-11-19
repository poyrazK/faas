// Package agentproxy is the agent egress gateway's politeness proxy
// (ADR-20261117185346 §4): a transparent TCP proxy that names each agent
// connection's destination from its TLS SNI or plain-HTTP Host header,
// without decrypting, and holds it to per-VM, per-account-domain and
// fleet-wide per-domain concurrent-connection caps.
package agentproxy

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
)

const (
	// tlsHandshakeMax bounds a reassembled ClientHello; a hello with
	// post-quantum key shares is a few KiB.
	tlsHandshakeMax = 64 << 10
	// httpHeadMax bounds a plain-HTTP request head read for its Host.
	httpHeadMax = 8 << 10
)

var (
	errNoSNI  = errors.New("TLS ClientHello names no server")
	errNoHost = errors.New("HTTP request has no Host header")
)

// peekTLS reads a TLS ClientHello from r and returns its server name and
// every byte read, which the caller replays upstream.
func peekTLS(r io.Reader) (string, []byte, error) {
	var raw, hs []byte
	for {
		var hdr [5]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return "", raw, fmt.Errorf("read TLS record: %w", err)
		}
		raw = append(raw, hdr[:]...)
		if hdr[0] != 0x16 {
			return "", raw, errors.New("not a TLS handshake")
		}
		n := int(binary.BigEndian.Uint16(hdr[3:5]))
		if n == 0 || n > 1<<14+2048 || len(hs)+n > tlsHandshakeMax {
			return "", raw, errors.New("TLS record length out of range")
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			return "", raw, fmt.Errorf("read TLS record: %w", err)
		}
		raw = append(raw, body...)
		hs = append(hs, body...)
		if len(hs) < 4 {
			continue
		}
		if hs[0] != 0x01 {
			return "", raw, errors.New("first handshake message is not a ClientHello")
		}
		want := 4 + (int(hs[1])<<16 | int(hs[2])<<8 | int(hs[3]))
		if want > tlsHandshakeMax {
			return "", raw, errors.New("ClientHello too large")
		}
		if len(hs) >= want {
			name, err := clientHelloSNI(hs[4:want])
			return name, raw, err
		}
	}
}

// clientHelloSNI is the host_name of a ClientHello body's server_name
// extension (RFC 6066 §3).
func clientHelloSNI(b []byte) (string, error) {
	c := cursor{b: b}
	c.skip(2 + 32)       // legacy_version, random
	c.skip(int(c.u8()))  // legacy_session_id
	c.skip(int(c.u16())) // cipher_suites
	c.skip(int(c.u8()))  // legacy_compression_methods
	exts := cursor{b: c.take(int(c.u16()))}
	if c.bad || exts.bad {
		return "", errors.New("malformed ClientHello")
	}
	for len(exts.b) > 0 && !exts.bad {
		typ, data := exts.u16(), exts.take(int(exts.u16()))
		if typ != 0 {
			continue
		}
		list := cursor{b: data}
		names := cursor{b: list.take(int(list.u16()))}
		for len(names.b) > 0 && !names.bad {
			kind, name := names.u8(), names.take(int(names.u16()))
			if kind == 0 && !names.bad {
				return string(name), nil
			}
		}
		return "", errNoSNI
	}
	if exts.bad {
		return "", errors.New("malformed ClientHello extensions")
	}
	return "", errNoSNI
}

// peekHTTP reads a plain-HTTP request head from r and returns its Host,
// without the port, and every byte read.
func peekHTTP(r io.Reader) (string, []byte, error) {
	var raw []byte
	buf := make([]byte, 2048)
	for !bytes.Contains(raw, []byte("\r\n\r\n")) {
		if len(raw) >= httpHeadMax {
			return "", raw, errors.New("HTTP request head too large")
		}
		n, err := r.Read(buf[:min(len(buf), httpHeadMax-len(raw))])
		raw = append(raw, buf[:n]...)
		if err != nil && !bytes.Contains(raw, []byte("\r\n\r\n")) {
			return "", raw, fmt.Errorf("read HTTP request head: %w", err)
		}
	}
	head, _, _ := bytes.Cut(raw, []byte("\r\n\r\n"))
	lines := strings.Split(string(head), "\r\n")
	for _, line := range lines[1:] {
		key, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "host") {
			continue
		}
		host := strings.TrimSpace(value)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		return host, raw, nil
	}
	return "", raw, errNoHost
}

// cursor reads big-endian TLS fields; any overrun sets bad.
type cursor struct {
	b   []byte
	bad bool
}

func (c *cursor) take(n int) []byte {
	if c.bad || n < 0 || n > len(c.b) {
		c.bad = true
		return nil
	}
	out := c.b[:n]
	c.b = c.b[n:]
	return out
}

func (c *cursor) skip(n int) { c.take(n) }

func (c *cursor) u8() uint8 {
	if b := c.take(1); b != nil {
		return b[0]
	}
	return 0
}

func (c *cursor) u16() uint16 {
	if b := c.take(2); b != nil {
		return binary.BigEndian.Uint16(b)
	}
	return 0
}
