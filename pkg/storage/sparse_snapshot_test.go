package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"testing/iotest"
)

func TestSparseSnapshotBytesAndTrailingHoles(t *testing.T) {
	mixed := make([]byte, 3*256*1024+123)
	for _, offset := range []int{0, 4095, 8192, 256*1024 - 1, 256*1024 + 1, 512 * 1024} {
		mixed[offset] = byte(offset%251 + 1)
	}
	for _, data := range [][]byte{nil, {0}, make([]byte, 1024*1024+17), mixed, bytes.Repeat([]byte{0xa7}, 256*1024+1)} {
		for _, fragmented := range []bool{false, true} {
			f, err := os.CreateTemp(t.TempDir(), "snapshot")
			if err != nil {
				t.Fatal(err)
			}
			src := io.Reader(bytes.NewReader(data))
			if fragmented {
				src = iotest.HalfReader(src)
			}
			n, err := copyArtifactContext(t.Context(), f, src, "snap/deployment/warm/mem")
			if err != nil || n != int64(len(data)) {
				t.Fatalf("copy length=%d fragmented=%v: n=%d err=%v", len(data), fragmented, n, err)
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			actual, err := io.ReadAll(f)
			if err != nil || !bytes.Equal(actual, data) {
				t.Fatalf("snapshot bytes differ: length=%d fragmented=%v err=%v", len(data), fragmented, err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

type snapshotErrorReader struct{ err error }

func (r snapshotErrorReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), r.err
}

func TestSparseSnapshotPreservesSourceFailures(t *testing.T) {
	for _, want := range []error{io.ErrUnexpectedEOF, errors.New("upstream interrupted"), fmt.Errorf("upstream read: %w", io.EOF)} {
		f, err := os.CreateTemp(t.TempDir(), "snapshot")
		if err != nil {
			t.Fatal(err)
		}
		_, got := copyArtifactContext(t.Context(), f, snapshotErrorReader{want}, "snap/deployment/mem")
		_ = f.Close()
		if !errors.Is(got, want) {
			t.Fatalf("lost source error: got %v want %v", got, want)
		}
	}
}

func TestSparseSnapshotCanceledBeforeRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f, err := os.CreateTemp(t.TempDir(), "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if n, err := copyArtifactContext(ctx, f, bytes.NewReader([]byte{1}), "snap/deployment/mem"); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled copy n=%d err=%v", n, err)
	}
}

func TestSparseSnapshotFailurePreservesPublishedArtifact(t *testing.T) {
	backend, err := NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := "snap/deployment/mem"
	original := []byte{1, 2, 3}
	if err := backend.Put(t.Context(), key, bytes.NewReader(original)); err != nil {
		t.Fatal(err)
	}
	if err := backend.Put(t.Context(), key, snapshotErrorReader{io.ErrUnexpectedEOF}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected failed publication, got %v", err)
	}
	r, err := backend.Get(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("failed write changed published artifact: %v", err)
	}
}
