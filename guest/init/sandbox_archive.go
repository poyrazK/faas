package main

// Workspace persist and hydrate (ADR-20261027090013 §3): a directory of the
// workspace as one tar.gz stream, made and unpacked inside the guest. Links
// are stored and restored as links, never followed; other special files are
// skipped. Extraction stays inside its directory (os.Root) and is bounded in
// entries and unpacked bytes.

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

// archiveCreate streams rel as a tar.gz of at most req.MaxBytes.
func (s *sandboxServer) archiveCreate(req sandboxproto.Request, call *sandboxproto.Call) (any, error) {
	root, rel, err := s.openWorkspace("path", req.Path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	if info, err := root.Stat(rel); err != nil {
		return nil, fileError(rel, err)
	} else if !info.IsDir() {
		return nil, sandboxError(sandboxproto.CodeNotADirectory, "%s is not a directory", rel)
	}
	sum := sha256.New()
	out := &cappedWriter{w: io.MultiWriter(call.Data(), sum), max: req.MaxBytes}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	entries := 0
	walkErr := fs.WalkDir(root.FS(), rel, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == rel {
			return nil
		}
		archived, err := s.archiveEntry(root, tw, rel, name, entry)
		if archived {
			entries++
		}
		return err
	})
	if err := errors.Join(walkErr, tw.Close(), gz.Close()); err != nil {
		if out.over {
			return nil, sandboxError(sandboxproto.CodeTooLarge, "the archive is over %d bytes", req.MaxBytes)
		}
		return nil, fileError(rel, err)
	}
	return sandboxproto.ArchiveResult{Bytes: out.n, Entries: entries, SHA256: hex.EncodeToString(sum.Sum(nil))}, nil
}

// archiveEntry writes one entry, named relative to the archived directory.
func (s *sandboxServer) archiveEntry(root *os.Root, tw *tar.Writer, base, name string, entry fs.DirEntry) (bool, error) {
	info, err := entry.Info()
	if err != nil {
		return false, err
	}
	link := ""
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		if link, err = root.Readlink(name); err != nil {
			return false, err
		}
	case !info.IsDir() && !info.Mode().IsRegular():
		return false, nil
	}
	hdr, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return false, err
	}
	hdr.Name = strings.TrimPrefix(strings.TrimPrefix(name, base), "/")
	if base == "." {
		hdr.Name = name
	}
	if info.IsDir() {
		hdr.Name += "/"
	}
	hdr.Uname, hdr.Gname, hdr.Uid, hdr.Gid = "", "", 0, 0
	if err := tw.WriteHeader(hdr); err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return true, nil
	}
	f, err := root.Open(name)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	_, err = io.CopyN(tw, f, info.Size())
	return true, err
}

// cappedWriter fails once more than max bytes pass through it.
type cappedWriter struct {
	w    io.Writer
	max  int64
	n    int64
	over bool
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.n+int64(len(p)) > c.max {
		c.over = true
		return 0, errors.New("archive limit reached")
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// archiveExtract unpacks the uploaded tar.gz into rel, which it creates.
func (s *sandboxServer) archiveExtract(req sandboxproto.Request, call *sandboxproto.Call) (any, error) {
	root, rel, err := s.openWorkspace("path", req.Path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	if err := s.mkdirAll(root, rel); err != nil {
		return nil, err
	}
	dest, err := root.OpenRoot(rel)
	if err != nil {
		return nil, fileError(rel, err)
	}
	defer func() { _ = dest.Close() }()
	pr, pw := io.Pipe()
	uploaded := make(chan error, 1)
	go func() {
		_, err := call.ReadUpload(pw, sandboxproto.MaxArchiveBytes)
		_ = pw.CloseWithError(err)
		uploaded <- err
	}()
	result, err := s.extract(dest, pr, req.MaxEntries, req.MaxBytes)
	// Drain what the host still sends, so the upload ends cleanly.
	_, _ = io.Copy(io.Discard, pr)
	if uploadErr := <-uploaded; uploadErr != nil {
		return nil, uploadErr
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *sandboxServer) extract(dest *os.Root, archive io.Reader, maxEntries int, maxBytes int64) (sandboxproto.ArchiveResult, error) {
	var result sandboxproto.ArchiveResult
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return result, sandboxError(sandboxproto.CodeInvalidRequest, "the archive is not gzip")
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return result, nil
		}
		if err != nil {
			return result, sandboxError(sandboxproto.CodeInvalidRequest, "the archive is not a tar stream")
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if result.Entries++; result.Entries > maxEntries {
			return result, sandboxError(sandboxproto.CodeTooLarge, "the archive has more than %d entries", maxEntries)
		}
		name, ok := archiveName(hdr.Name)
		if !ok {
			return result, sandboxError(sandboxproto.CodeOutsideWorkspace, "archive entry %q leaves its directory", hdr.Name)
		}
		if err := s.extractEntry(dest, tr, hdr, name, &result, maxBytes); err != nil {
			return result, err
		}
	}
}

// archiveName is an entry's name inside the extracted directory, or false
// when it is absolute or climbs out.
func archiveName(name string) (string, bool) {
	name = strings.TrimSuffix(strings.TrimPrefix(name, "./"), "/")
	if name == "" || path.IsAbs(name) || !fs.ValidPath(name) {
		return "", false
	}
	return name, true
}

func (s *sandboxServer) extractEntry(dest *os.Root, tr *tar.Reader, hdr *tar.Header, name string, result *sandboxproto.ArchiveResult,
	maxBytes int64) error {
	parent := path.Dir(name)
	switch hdr.Typeflag {
	case tar.TypeDir:
		return s.mkdirAll(dest, name)
	case tar.TypeReg:
		if result.Bytes += hdr.Size; hdr.Size < 0 || result.Bytes > maxBytes {
			return sandboxError(sandboxproto.CodeTooLarge, "the archive unpacks to more than %d bytes", maxBytes)
		}
		if err := s.mkdirAll(dest, parent); err != nil {
			return err
		}
		mode := fs.FileMode(hdr.Mode).Perm()
		f, err := dest.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			return fileError(name, err)
		}
		_, copyErr := io.CopyN(f, tr, hdr.Size)
		if err := errors.Join(copyErr, f.Close()); err != nil {
			return fmt.Errorf("extract %s: %w", name, err)
		}
		if err := s.chown(dest, name); err != nil {
			return fileError(name, err)
		}
	case tar.TypeSymlink:
		if err := s.mkdirAll(dest, parent); err != nil {
			return err
		}
		_ = dest.Remove(name)
		if err := dest.Symlink(hdr.Linkname, name); err != nil {
			return fileError(name, err)
		}
		if err := s.chown(dest, name); err != nil {
			return fileError(name, err)
		}
	default:
		result.Entries--
	}
	return nil
}
