package udpwire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestFramesPreserveDatagrams(t *testing.T) {
	messages := [][]byte{nil, []byte("one"), {0, 1, 0, 255}, bytes.Repeat([]byte{42}, api.UDPDatagramMaxBytes)}
	var wire bytes.Buffer
	for _, msg := range messages {
		if err := Write(&wire, msg); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range messages {
		got, err := Read(&wire)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("datagram mismatch: got %d bytes want %d", len(got), len(want))
		}
	}
	if _, err := Read(&wire); !errors.Is(err, io.EOF) {
		t.Fatalf("record boundary EOF: %v", err)
	}
}

func TestRejectInvalidFrames(t *testing.T) {
	for _, frame := range [][]byte{{0}, {0, 0, 0}, {0, 0, 0, 1}, {0, 0, 0, 2, 42}} {
		if _, err := Read(bytes.NewReader(frame)); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("truncated record %v: %v", frame, err)
		}
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], api.UDPDatagramMaxBytes+1)
	if _, err := Read(bytes.NewReader(header[:])); !errors.Is(err, ErrDatagramTooLarge) {
		t.Fatalf("oversized record: %v", err)
	}
	var output bytes.Buffer
	if err := Write(&output, make([]byte, api.UDPDatagramMaxBytes+1)); !errors.Is(err, ErrDatagramTooLarge) {
		t.Fatalf("oversized datagram: %v", err)
	}
	if output.Len() != 0 {
		t.Fatal("oversized datagram wrote partial frame")
	}
}

type shortWriter struct{ bytes.Buffer }

func (w *shortWriter) Write(p []byte) (int, error) { return w.Buffer.Write(p[:1]) }

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }
func TestFrameShortWrites(t *testing.T) {
	var w shortWriter
	if err := Write(&w, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	got, err := Read(&w.Buffer)
	if err != nil || string(got) != "hello" {
		t.Fatalf("short writes: %q %v", got, err)
	}
	if err := Write(zeroWriter{}, nil); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("zero writer: %v", err)
	}
}
