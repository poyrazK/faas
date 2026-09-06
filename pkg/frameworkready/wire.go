// Package frameworkready defines the bounded guest-init readiness reply.
package frameworkready

import (
	"encoding/binary"
	"fmt"
	"io"
)

const Port = 1027

// Status is replayable from a snapshot. Identity always comes from the host's VM socket.
type Status struct {
	Ready    bool
	WarmupMs uint32
}

func Write(w io.Writer, s Status) error {
	var b [9]byte
	copy(b[:4], "FRD1")
	if s.Ready {
		b[4] = 1
		binary.BigEndian.PutUint32(b[5:], s.WarmupMs)
	}
	n, err := w.Write(b[:])
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	return err
}
func Read(r io.Reader) (Status, error) {
	var b [9]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return Status{}, err
	}
	if string(b[:4]) != "FRD1" || b[4] > 1 {
		return Status{}, fmt.Errorf("invalid framework-ready reply")
	}
	s := Status{Ready: b[4] == 1, WarmupMs: binary.BigEndian.Uint32(b[5:])}
	if !s.Ready && s.WarmupMs != 0 {
		return Status{}, fmt.Errorf("pending reply carries warmup")
	}
	return s, nil
}
