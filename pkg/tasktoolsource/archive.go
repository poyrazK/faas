package tasktoolsource

import (
	"archive/tar"
	"bytes"
	"compress/gzip"

	"github.com/onebox-faas/faas/pkg/api"
)

// PackReviewedArchive produces deterministic builder input from the exact
// authored tree that was reviewed. Files and directories stay in memory.
func PackReviewedArchive(entries []api.HostedTaskSourceEntry, digest string) ([]byte, error) {
	got, err := Digest(entries)
	if err != nil || got != digest {
		return nil, ErrInvalid
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range orderedEntries(entries) {
		header := tar.Header{
			Name: entry.Path, Mode: int64(entry.Mode), Size: int64(len(entry.Content)),
			Typeflag: tar.TypeReg, Format: tar.FormatPAX,
		}
		if entry.Directory {
			header.Typeflag = tar.TypeDir
		}
		if err := tw.WriteHeader(&header); err != nil {
			return nil, ErrInvalid
		}
		if !entry.Directory {
			if _, err := tw.Write(entry.Content); err != nil {
				return nil, ErrInvalid
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, ErrInvalid
	}
	if err := gz.Close(); err != nil {
		return nil, ErrInvalid
	}
	return buf.Bytes(), nil
}
