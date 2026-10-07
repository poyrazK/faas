// Package udpwire preserves UDP message boundaries across helper pipes.
package udpwire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrDatagramTooLarge = errors.New("UDP datagram exceeds guest payload limit")

// Read reads one complete datagram. EOF is valid only between records; a
// partial header or payload is an error. A zero-length record is a datagram.
func Read(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size > api.UDPDatagramMaxBytes {
		return nil, fmt.Errorf("%w: %d", ErrDatagramTooLarge, size)
	}
	payload := make([]byte, int(size))
	if _, err := io.ReadFull(r, payload); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return payload, nil
}

// Write emits a complete record. Callers serialize concurrent writes.
func Write(w io.Writer, payload []byte) error {
	if len(payload) > api.UDPDatagramMaxBytes {
		return fmt.Errorf("%w: %d", ErrDatagramTooLarge, len(payload))
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeAll(w, header[:]); err != nil {
		return err
	}
	return writeAll(w, payload)
}

func writeAll(w io.Writer, payload []byte) error {
	for len(payload) > 0 {
		n, err := w.Write(payload)
		if n < 0 || n > len(payload) {
			return io.ErrShortWrite
		}
		payload = payload[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
