package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// copyArtifactContext preserves zero-filled snapshot pages as file holes. dst
// must be a fresh, empty temporary file. Publication still uses the caller's
// fsync and atomic rename, and the returned size counts logical bytes for the
// existing cache budget. Readers see exactly the original snapshot bytes.
func copyArtifactContext(ctx context.Context, dst *os.File, src io.Reader, key string) (int64, error) {
	if !strings.HasPrefix(key, "snap/") || !strings.HasSuffix(key, "/mem") {
		return copyContext(ctx, dst, src)
	}
	const quantum = 256 * 1024
	const page = 4096
	buf := make([]byte, quantum)
	zero := make([]byte, page)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		n, readErr := readSnapshotQuantum(ctx, src, buf)
		for start := 0; start < n; {
			end := min(start+page, n)
			isZero := bytes.Equal(buf[start:end], zero[:end-start])
			for end < n {
				next := min(end+page, n)
				if bytes.Equal(buf[end:next], zero[:next-end]) != isZero {
					break
				}
				end = next
			}
			if isZero {
				if _, err := dst.Seek(int64(end-start), io.SeekCurrent); err != nil {
					return written, fmt.Errorf("seek snapshot hole: %w", err)
				}
				written += int64(end - start)
			} else {
				w, err := dst.Write(buf[start:end])
				written += int64(w)
				if err != nil {
					return written, err
				}
				if w != end-start {
					return written, io.ErrShortWrite
				}
			}
			start = end
		}
		if readErr == io.EOF { //nolint:errorlint // Reader must return EOF itself; a wrapped source failure must prevent publication.
			// Seek does not extend a file when the snapshot ends with zeros.
			if err := dst.Truncate(written); err != nil {
				return written, fmt.Errorf("size sparse snapshot: %w", err)
			}
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

// Fill a quantum without hiding a source error that accompanies its last bytes.
// io.ReadFull would turn both a short EOF and an upstream ErrUnexpectedEOF into
// the same result; an interrupted snapshot stream must never be published.
func readSnapshotQuantum(ctx context.Context, src io.Reader, buf []byte) (int, error) {
	n := 0
	emptyReads := 0
	for n < len(buf) {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		r, err := src.Read(buf[n:])
		n += r
		if err != nil {
			return n, err
		}
		if r == 0 {
			emptyReads++
			if emptyReads >= 100 {
				return n, io.ErrNoProgress
			}
		} else {
			emptyReads = 0
		}
	}
	return n, nil
}
