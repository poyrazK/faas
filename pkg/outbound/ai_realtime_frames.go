package outbound

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"

	"github.com/onebox-faas/faas/pkg/safetext"
)

// WebSocket framing (RFC 6455) as far as a transparent realtime proxy needs
// it (ADR 20261019072722): outboundd reads the provider's frames to meter
// them and forwards each one byte for byte, and writes its own text and
// close frames to the caller.

const (
	wsOpContinuation byte = 0x0
	wsOpText         byte = 0x1
	wsOpBinary       byte = 0x2
	wsOpClose        byte = 0x8
	wsOpPing         byte = 0x9
	wsOpPong         byte = 0xA
)

// wsClosePolicyViolation is the close code for a session outboundd ends.
const wsClosePolicyViolation = 1008

var (
	errWSFrameTooLarge = errors.New("websocket frame exceeds the realtime message bound")
	errWSCompressed    = errors.New("websocket frame is compressed")
	errWSProtocol      = errors.New("websocket frame is malformed")
)

// wsFrame is one frame as it arrived: its raw header and payload, which are
// forwarded unchanged, and the fields the proxy reads.
type wsFrame struct {
	fin     bool
	opcode  byte
	header  []byte
	payload []byte
}

// readWSFrame reads one frame of at most maxPayload payload bytes. A frame
// with reserved bits set means a negotiated extension, such as compression,
// whose payload the proxy cannot read.
func readWSFrame(r *bufio.Reader, maxPayload int64) (wsFrame, error) {
	var head [2]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return wsFrame{}, err
	}
	frame := wsFrame{fin: head[0]&0x80 != 0, opcode: head[0] & 0x0F, header: append(make([]byte, 0, 14), head[:]...)}
	if head[0]&0x70 != 0 {
		return wsFrame{}, errWSCompressed
	}
	length := int64(head[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return wsFrame{}, err
		}
		frame.header = append(frame.header, ext[:]...)
		length = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return wsFrame{}, err
		}
		frame.header = append(frame.header, ext[:]...)
		raw := binary.BigEndian.Uint64(ext[:])
		if raw > uint64(maxPayload) {
			return wsFrame{}, errWSFrameTooLarge
		}
		length = int64(raw)
	}
	if length > maxPayload {
		return wsFrame{}, errWSFrameTooLarge
	}
	if frame.opcode >= wsOpClose && (length > 125 || !frame.fin) {
		return wsFrame{}, errWSProtocol
	}
	if head[1]&0x80 != 0 {
		var mask [4]byte
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return wsFrame{}, err
		}
		frame.header = append(frame.header, mask[:]...)
	}
	frame.payload = make([]byte, length)
	if _, err := io.ReadFull(r, frame.payload); err != nil {
		return wsFrame{}, err
	}
	return frame, nil
}

// wsServerFrame builds an unmasked frame, as a server sends it.
func wsServerFrame(opcode byte, payload []byte) []byte {
	frame := []byte{0x80 | opcode}
	switch n := len(payload); {
	case n < 126:
		frame = append(frame, byte(n))
	case n <= 0xFFFF:
		frame = append(frame, 126, byte(n>>8), byte(n))
	default:
		frame = append(frame, 127)
		frame = binary.BigEndian.AppendUint64(frame, uint64(n))
	}
	return append(frame, payload...)
}

// wsCloseFrame builds a server close frame with a code and a reason cut to
// the 123 bytes a control frame can carry. RFC 6455 requires the reason to be
// valid UTF-8, so the cut never splits a rune.
func wsCloseFrame(code int, reason string) []byte {
	payload := binary.BigEndian.AppendUint16(nil, uint16(code))
	return wsServerFrame(wsOpClose, append(payload, safetext.Truncate(reason, 123)...))
}

// wsMessage reassembles the text messages of one direction. Binary and
// control frames pass through it unread.
type wsMessage struct {
	open    bool
	text    bool
	buf     []byte
	maxSize int64
}

// add takes one data frame and returns a complete text message, if this
// frame completes one.
func (m *wsMessage) add(frame wsFrame) ([]byte, error) {
	switch frame.opcode {
	case wsOpText, wsOpBinary:
		if m.open {
			return nil, errWSProtocol
		}
		m.open, m.text, m.buf = !frame.fin, frame.opcode == wsOpText, m.buf[:0]
	case wsOpContinuation:
		if !m.open {
			return nil, errWSProtocol
		}
		m.open = !frame.fin
	default:
		return nil, nil
	}
	if !m.text {
		return nil, nil
	}
	if int64(len(m.buf)+len(frame.payload)) > m.maxSize {
		return nil, errWSFrameTooLarge
	}
	m.buf = append(m.buf, frame.payload...)
	if m.open {
		return nil, nil
	}
	return m.buf, nil
}
