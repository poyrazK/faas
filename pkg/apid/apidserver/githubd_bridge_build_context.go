package apidserver

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// narrowTarGz writes the entries of the .tar.gz at sourcePath that lie under
// root (a cleaned, slash-separated relative directory) to dst, with root as
// the archive root. It streams the archive: nothing is extracted to disk and
// no file is held in memory. Symlinks are kept as they are, as the build
// would have seen them in the repository; a hard link to a file outside root
// is dropped, since its target is not in the archive. hasDockerfile reports
// whether root/dockerfile is a regular file of the archive. dst is written
// through a temporary file and renamed into place only on success.
func narrowTarGz(sourcePath, root, dockerfile, dst string) (hasDockerfile bool, err error) {
	//nolint:forbidigo // sourcePath passed the staging-root allowlist.
	src, err := os.Open(sourcePath)
	if err != nil {
		return false, fmt.Errorf("open staged source: %w", err)
	}
	defer func() { _ = src.Close() }()
	gz, err := gzip.NewReader(src)
	if err != nil {
		return false, fmt.Errorf("read staged source: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return false, fmt.Errorf("create build context archive: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	zw := gzip.NewWriter(out)
	tw := tar.NewWriter(zw)
	hasDockerfile, copyErr := copyTarUnder(tar.NewReader(gz), tw, root, dockerfile)
	for _, closeErr := range []error{tw.Close(), zw.Close(), out.Close()} {
		if copyErr == nil {
			copyErr = closeErr
		}
	}
	if copyErr != nil {
		return false, copyErr
	}
	if err = os.Rename(tmp, filepath.Clean(dst)); err != nil {
		return false, fmt.Errorf("spool build context archive: %w", err)
	}
	return hasDockerfile, nil
}

func copyTarUnder(tr *tar.Reader, tw *tar.Writer, root, dockerfile string) (hasDockerfile bool, err error) {
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return hasDockerfile, nil
		}
		if err != nil {
			return false, fmt.Errorf("read staged source: %w", err)
		}
		name, ok := underRoot(h.Name, root)
		if !ok {
			continue
		}
		if h.Typeflag == tar.TypeLink {
			target, inside := underRoot(h.Linkname, root)
			if !inside {
				continue
			}
			h.Linkname = target
		}
		if h.Typeflag == tar.TypeDir {
			name += "/"
		}
		h.Name = name
		if h.Typeflag == tar.TypeReg && name == dockerfile {
			hasDockerfile = true
		}
		if err := tw.WriteHeader(h); err != nil {
			return false, fmt.Errorf("write build context archive: %w", err)
		}
		if h.Typeflag == tar.TypeReg {
			//nolint:gosec // G110: the staged archive's size was checked against the plan's limit.
			if _, err := io.Copy(tw, tr); err != nil {
				return false, fmt.Errorf("write build context archive: %w", err)
			}
		}
	}
}

// underRoot maps an archive entry name to its name relative to root, and
// reports whether it lies strictly under root.
func underRoot(name, root string) (string, bool) {
	clean := path.Clean(strings.TrimPrefix(name, "/"))
	rel, ok := strings.CutPrefix(clean, root+"/")
	if !ok || rel == "" || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}
