// Package cirunner implements the CI workload that runs inside a Jobs VM.
// Its portable tests inject a fake executor and never run repository commands.
package cirunner

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

var errSource = errors.New("CI source archive is invalid or exceeds its bounds")

func extract(ctx context.Context, source io.Reader, root *os.Root) error {
	buffered := bufio.NewReader(source)
	gz, err := gzip.NewReader(buffered)
	if err != nil {
		return errSource
	}
	defer func() { _ = gz.Close() }()
	gz.Multistream(false)
	bounded := &expandedReader{ctx: ctx, reader: gz, left: api.CIJobsExpandedSourceMaxBytes}
	archive := tar.NewReader(bounded)
	prefix := ""
	logicalBytes := int64(0)
	for count := 0; ; count++ {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || count >= api.CIJobsSourceMaxEntries {
			return errSource
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !safeSourcePath(name) {
			return errSource
		}
		parts := strings.Split(name, "/")
		if prefix == "" {
			prefix = parts[0]
		}
		if parts[0] != prefix {
			return errSource
		}
		if len(parts) == 1 {
			if header.Typeflag != tar.TypeDir {
				return errSource
			}
			continue
		}
		rel := strings.Join(parts[1:], "/")
		if header.Typeflag == tar.TypeDir {
			if err := root.MkdirAll(rel, 0o755); err != nil {
				return errSource
			}
			continue
		}
		// archive/tar normalizes legacy regular-file headers to TypeReg.
		if header.Typeflag != tar.TypeReg {
			return errSource
		}
		if header.Size < 0 || header.Size > api.CIJobsExpandedSourceMaxBytes {
			return errSource
		}
		logicalBytes += header.Size
		if logicalBytes > api.CIJobsExpandedSourceMaxBytes {
			return errSource
		}
		if err := root.MkdirAll(path.Dir(rel), 0o755); err != nil {
			return errSource
		}
		mode := os.FileMode(0o644)
		if header.Mode&0o111 != 0 {
			mode = 0o755
		}
		file, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return errSource
		}
		copied, copyErr := io.Copy(file, io.LimitReader(archive, header.Size+1))
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || copied != header.Size {
			return errSource
		}
	}
	if prefix == "" {
		return errSource
	}
	if _, err := io.Copy(io.Discard, bounded); err != nil {
		return errSource
	}
	if _, err := buffered.Peek(1); !errors.Is(err, io.EOF) {
		return errSource
	}
	return nil
}

func safeSourcePath(name string) bool {
	if len(name) == 0 || len(name) > api.CIJobsSourcePathMaxBytes || !utf8.ValidString(name) || strings.ContainsAny(name, "\\\x00\r\n") || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || part == ".git" {
			return false
		}
	}
	return true
}

type expandedReader struct {
	ctx    context.Context
	reader io.Reader
	left   int64
}

func (r *expandedReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.left < 0 {
		return 0, errSource
	}
	if int64(len(p)) > r.left+1 {
		p = p[:r.left+1]
	}
	n, err := r.reader.Read(p)
	r.left -= int64(n)
	if r.left < 0 {
		return n, errSource
	}
	return n, err
}
