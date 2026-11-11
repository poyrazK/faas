package tarball

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"testing/fstest"
)

// SelectFS reads a gzipped source archive into a small read-only file system
// rooted at the project: every directory, and the regular files keep
// accepts (by project-relative slash path). The archive's transport wrapper
// directory (RootPrefix) is stripped. A kept file larger than maxFileBytes
// keeps its first maxFileBytes+1 bytes, so a caller that refuses files above
// the cap by size still sees it as too large without holding all of it.
// maxTotalBytes bounds the content held in memory.
//
//nolint:forbidigo // callers pass apid-spooled source archives; the path is an internal artifact, not a customer path opened directly by this package.
func SelectFS(archive string, keep func(name string) bool, maxFileBytes, maxTotalBytes int64) (fs.FS, error) {
	prefix, err := RootPrefix(archive)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(archive)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	defer func() { _ = zr.Close() }()
	out := fstest.MapFS{}
	var total int64
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		name := strings.Trim(TrimLeadingDotSlash(hdr.Name), "/")
		if prefix != "" {
			trimmed := strings.TrimPrefix(name, strings.Trim(prefix, "/"))
			if trimmed == name {
				continue
			}
			name = strings.TrimPrefix(trimmed, "/")
		}
		if name == "" || !fs.ValidPath(name) {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			out[name] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}
		case tar.TypeReg:
			for dir := path.Dir(name); dir != "." && out[dir] == nil; dir = path.Dir(dir) {
				out[dir] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}
			}
			if !keep(name) {
				continue
			}
			limit := min(hdr.Size, maxFileBytes+1)
			if total+limit > maxTotalBytes {
				return nil, fmt.Errorf("selected files exceed %d bytes", maxTotalBytes)
			}
			data, err := io.ReadAll(io.LimitReader(tr, limit))
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", name, err)
			}
			total += int64(len(data))
			out[name] = &fstest.MapFile{Data: data, Mode: 0o644}
		}
	}
}
