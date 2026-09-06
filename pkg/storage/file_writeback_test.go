package storage

import (
	"errors"
	"io"
	"testing"
)

type writebackProbe struct {
	bytes, flushed int
	fail           error
}

func (p *writebackProbe) Write(b []byte) (int, error) { p.bytes += len(b); return len(b), nil }
func (p *writebackProbe) Sync() error                 { p.flushed++; return p.fail }

func TestFileWritebackBoundsDirtyChunks(t *testing.T) {
	p := &writebackProbe{}
	w := &fileWritebackWriter{file: p}
	const chunk = 256 << 10
	b := make([]byte, chunk)
	for i := 0; i < 3*fileWritebackBytes/chunk+1; i++ {
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
		if w.dirty >= fileWritebackBytes {
			t.Fatalf("dirty budget not released: %d", w.dirty)
		}
	}
	if p.flushed != 3 {
		t.Fatalf("flushes=%d, want3", p.flushed)
	}
}

func TestFileWritebackReturnsFlushFailure(t *testing.T) {
	want := errors.New("disk writeback failed")
	p := &writebackProbe{fail: want}
	w := &fileWritebackWriter{file: p, dirty: fileWritebackBytes - 1}
	n, err := w.Write([]byte("x"))
	if n != 1 || !errors.Is(err, want) {
		t.Fatalf("Write=(%d,%v), want1, disk error", n, err)
	}
}

func TestFileWritebackLeavesNonFileWritersAlone(t *testing.T) {
	if boundedFileWrites(io.Discard) != io.Discard {
		t.Fatal("non-file writer wrapped")
	}
}
