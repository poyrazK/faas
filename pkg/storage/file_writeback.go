package storage

import (
	"fmt"
	"io"
	"os"
)

// Buffered file writes count against a daemon's cgroup, even with a small
// Go heap. Flush incrementally so a large OCI download/cache fill cannot
// fill schedd's 256 MiB limit with dirty, unreclaimable pages. The final
// remainder keeps the caller's existing publication/fsync contract.
const fileWritebackBytes = 16 << 20

type fileWritebackWriter struct {
	file interface {
		io.Writer
		Sync() error
	}
	dirty int
}

func boundedFileWrites(dst io.Writer) io.Writer {
	if file, ok := dst.(*os.File); ok {
		return &fileWritebackWriter{file: file}
	}
	return dst
}

func (w *fileWritebackWriter) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	w.dirty += n
	if err != nil {
		return n, err
	}
	if w.dirty >= fileWritebackBytes {
		if err := w.file.Sync(); err != nil {
			return n, fmt.Errorf("storage: incremental file writeback: %w", err)
		}
		w.dirty = 0
	}
	return n, nil
}
