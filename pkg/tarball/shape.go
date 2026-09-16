package tarball

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// ShapeErrorKind identifies which source-archive contract failed without
// coupling this low-level package to an HTTP or API error vocabulary.
type ShapeErrorKind string

const (
	ShapeOpen       ShapeErrorKind = "open"
	ShapeNotGzip    ShapeErrorKind = "not_gzip"
	ShapeBadTar     ShapeErrorKind = "bad_tar"
	ShapeUnsafePath ShapeErrorKind = "unsafe_path"
	ShapeUnsafeLink ShapeErrorKind = "unsafe_link"
	ShapeTooMany    ShapeErrorKind = "too_many_entries"
	ShapeEmpty      ShapeErrorKind = "empty"
)

// ShapeError is returned by ValidateShape. Detail is safe to surface to the
// customer and Err retains the decoder failure for errors.Is/As callers.
type ShapeError struct {
	Kind   ShapeErrorKind
	Detail string
	Err    error
}

func (e *ShapeError) Error() string {
	if e == nil {
		return ""
	}
	if e.Detail != "" {
		return e.Detail
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Kind)
}

func (e *ShapeError) Unwrap() error { return e.Err }

// ValidateShape verifies the source archive contract without extracting or
// executing customer content. It rejects malformed gzip/tar streams, empty
// archives, path escapes, escaping link targets, and entry-count overflow.
//
//nolint:forbidigo // callers first apply their customer-path ownership/symlink policy; the server passes a private spool file.
func ValidateShape(path string, maxEntries int) error {
	f, err := os.Open(path)
	if err != nil {
		return &ShapeError{Kind: ShapeOpen, Detail: err.Error(), Err: err}
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return &ShapeError{Kind: ShapeNotGzip, Detail: "source must be tar.gz", Err: err}
	}
	defer func() { _ = zr.Close() }()

	reader := tar.NewReader(zr)
	count := 0
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			if count == 0 {
				return &ShapeError{Kind: ShapeEmpty, Detail: "source archive is empty"}
			}
			return nil
		}
		if nextErr != nil {
			return &ShapeError{Kind: ShapeBadTar, Detail: nextErr.Error(), Err: nextErr}
		}
		if EscapesRoot(header.Name) {
			return &ShapeError{Kind: ShapeUnsafePath, Detail: "absolute paths or '..' entries are rejected"}
		}
		if header.Typeflag == tar.TypeSymlink || header.Typeflag == tar.TypeLink {
			if EscapesRoot(header.Linkname) {
				return &ShapeError{Kind: ShapeUnsafeLink, Detail: "symlink/hardlink with absolute or '..' target rejected"}
			}
		}
		count++
		if maxEntries > 0 && count > maxEntries {
			return &ShapeError{Kind: ShapeTooMany, Detail: fmt.Sprintf("too many files (>%d)", maxEntries)}
		}
	}
}

// EscapesRoot reports whether a tar path is absolute or contains a parent
// component. Tar paths always use forward slashes, independent of host OS.
func EscapesRoot(value string) bool {
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "/") {
		return true
	}
	for _, component := range strings.Split(value, "/") {
		if component == ".." {
			return true
		}
	}
	return false
}
