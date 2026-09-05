// Package sourcedelta builds and applies content-addressed deltas between
// Gregale source archives. Deltas are a transport optimization only: Apply
// always reconstructs a complete tar.gz before the normal deploy validators
// and build queue see it.
package sourcedelta

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

var (
	ErrBaseRevision   = errors.New("source delta base revision mismatch")
	ErrTargetRevision = errors.New("source delta target revision mismatch")
)

// Limits bounds archive work. MaxCompressedBytes may be zero when the caller
// already enforces the wire-size ceiling; MaxEntries must be positive.
type Limits struct {
	MaxEntries         int
	MaxCompressedBytes int64
	MaxExpandedBytes   int64
}

// Entry is the content-significant shape of one archive entry. Timestamps and
// ownership are intentionally excluded: neither changes the source tree seen
// by a build, while mode changes remain significant.
type Entry struct {
	Type   byte
	Mode   int64
	Size   int64
	Digest string
}

// Manifest is the canonical identity of a complete source archive.
type Manifest struct {
	Revision          string
	Entries           map[string]Entry
	RegularFiles      int
	UncompressedBytes int64
	CompressedBytes   int64
}

// Result describes the delta created for Target.
type Result struct {
	Target       Manifest
	Changed      []string
	Deleted      []string
	ChangedFiles int
	ReusedBytes  int64
	DeltaBytes   int64
}

// ValidRevision reports whether value is a lowercase SHA-256 digest.
func ValidRevision(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// Inspect validates and hashes a complete or delta archive. The caller owns
// archive and must keep it open for the duration of the call.
func Inspect(archive *os.File, limits Limits) (Manifest, error) {
	if limits.MaxEntries <= 0 {
		return Manifest{}, errors.New("source delta MaxEntries must be positive")
	}
	info, err := archive.Stat()
	if err != nil {
		return Manifest{}, fmt.Errorf("stat archive: %w", err)
	}
	if limits.MaxCompressedBytes > 0 && info.Size() > limits.MaxCompressedBytes {
		return Manifest{}, fmt.Errorf("compressed archive is %d bytes (limit %d)", info.Size(), limits.MaxCompressedBytes)
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return Manifest{}, fmt.Errorf("rewind archive: %w", err)
	}
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return Manifest{}, fmt.Errorf("open gzip stream: %w", err)
	}
	defer func() { _ = gz.Close() }()

	m := Manifest{Entries: make(map[string]Entry), CompressedBytes: info.Size()}
	tr := tar.NewReader(gz)
	for {
		hdr, nextErr := tr.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return Manifest{}, fmt.Errorf("read tar header: %w", nextErr)
		}
		name, typ, normErr := normalizeHeader(hdr)
		if normErr != nil {
			return Manifest{}, normErr
		}
		if _, duplicate := m.Entries[name]; duplicate {
			return Manifest{}, fmt.Errorf("duplicate archive entry %q", name)
		}
		if len(m.Entries) >= limits.MaxEntries {
			return Manifest{}, fmt.Errorf("archive contains more than %d entries", limits.MaxEntries)
		}
		entry := Entry{Type: typ, Mode: hdr.Mode & 0o777, Size: hdr.Size}
		if typ == tar.TypeReg {
			if hdr.Size < 0 || (limits.MaxExpandedBytes > 0 && hdr.Size > limits.MaxExpandedBytes-m.UncompressedBytes) {
				return Manifest{}, fmt.Errorf("expanded archive exceeds %d bytes", limits.MaxExpandedBytes)
			}
			h := sha256.New()
			n, copyErr := io.Copy(h, tr) //nolint:gosec // header size was checked against MaxExpandedBytes immediately above.
			if copyErr != nil {
				return Manifest{}, fmt.Errorf("hash %q: %w", name, copyErr)
			}
			if n != hdr.Size {
				return Manifest{}, fmt.Errorf("archive entry %q: read %d bytes, want %d", name, n, hdr.Size)
			}
			entry.Digest = hex.EncodeToString(h.Sum(nil))
			m.RegularFiles++
			m.UncompressedBytes += n
		}
		m.Entries[name] = entry
	}
	m.Revision = manifestRevision(m.Entries)
	return m, nil
}

// Create writes the entries that differ from base into delta and reports paths
// removed from target. The caller owns both files; delta must be writable.
func Create(base Manifest, target, delta *os.File, limits Limits) (result Result, err error) {
	targetManifest, err := Inspect(target, limits)
	if err != nil {
		return Result{}, fmt.Errorf("inspect target archive: %w", err)
	}
	changed := make(map[string]struct{})
	for name, entry := range targetManifest.Entries {
		if previous, ok := base.Entries[name]; !ok || previous != entry {
			changed[name] = struct{}{}
			result.Changed = append(result.Changed, name)
			if entry.Type == tar.TypeReg {
				result.ChangedFiles++
			}
		} else if entry.Type == tar.TypeReg {
			result.ReusedBytes += entry.Size
		}
	}
	for name := range base.Entries {
		if _, ok := targetManifest.Entries[name]; !ok {
			result.Deleted = append(result.Deleted, name)
		}
	}
	sort.Strings(result.Changed)
	sort.Strings(result.Deleted)
	if err := filterArchive(target, delta, changed, limits.MaxCompressedBytes); err != nil {
		return Result{}, err
	}
	info, err := delta.Stat()
	if err != nil {
		return Result{}, fmt.Errorf("stat source delta: %w", err)
	}
	result.Target = targetManifest
	result.DeltaBytes = info.Size()
	return result, nil
}

// Apply reconstructs a complete source archive from base + delta - deleted.
// The result is rejected unless both advertised revisions match content.
func Apply(baseFile, deltaFile, output *os.File, expectedBase, expectedTarget string, deleted []string, limits Limits) (Manifest, error) {
	base, err := Inspect(baseFile, limits)
	if err != nil || base.Revision != expectedBase {
		return Manifest{}, ErrBaseRevision
	}
	delta, err := Inspect(deltaFile, limits)
	if err != nil {
		return Manifest{}, fmt.Errorf("inspect source delta: %w", err)
	}
	removed := make(map[string]struct{}, len(deleted))
	for _, raw := range deleted {
		name, normErr := normalizeName(raw)
		if normErr != nil || name != raw {
			return Manifest{}, fmt.Errorf("invalid deleted source path %q", raw)
		}
		if _, duplicate := removed[name]; duplicate {
			return Manifest{}, fmt.Errorf("duplicate deleted source path %q", name)
		}
		if _, replaced := delta.Entries[name]; replaced {
			return Manifest{}, fmt.Errorf("source path %q is both changed and deleted", name)
		}
		removed[name] = struct{}{}
	}
	replaced := make(map[string]struct{}, len(delta.Entries))
	for name := range delta.Entries {
		replaced[name] = struct{}{}
	}
	if err := mergeArchives(baseFile, deltaFile, output, removed, replaced, limits.MaxCompressedBytes); err != nil {
		return Manifest{}, err
	}
	result, err := Inspect(output, limits)
	if err != nil {
		clearArchive(output)
		return Manifest{}, fmt.Errorf("inspect reconstructed source: %w", err)
	}
	if result.Revision != expectedTarget {
		clearArchive(output)
		return Manifest{}, ErrTargetRevision
	}
	return result, nil
}

func normalizeHeader(hdr *tar.Header) (string, byte, error) {
	typ := hdr.Typeflag
	if typ == 0 {
		typ = tar.TypeReg
	}
	if typ != tar.TypeReg && typ != tar.TypeDir {
		return "", 0, fmt.Errorf("archive entry %q has unsupported type %d", hdr.Name, typ)
	}
	name, err := normalizeName(strings.TrimSuffix(hdr.Name, "/"))
	if err != nil {
		return "", 0, fmt.Errorf("archive entry %q: %w", hdr.Name, err)
	}
	if typ == tar.TypeDir && hdr.Size != 0 {
		return "", 0, fmt.Errorf("directory archive entry %q has non-zero size", hdr.Name)
	}
	return name, typ, nil
}

func normalizeName(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, '\x00') || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
		return "", errors.New("source path must be a non-empty relative slash path")
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("source path escapes the archive root")
	}
	if clean != name {
		return "", errors.New("source path is not canonical")
	}
	return clean, nil
}

func manifestRevision(entries map[string]Entry) string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		e := entries[name]
		_, _ = io.WriteString(h, name)
		_, _ = h.Write([]byte{0, e.Type, 0})
		_, _ = io.WriteString(h, strconv.FormatInt(e.Mode, 8))
		_, _ = h.Write([]byte{0})
		_, _ = io.WriteString(h, strconv.FormatInt(e.Size, 10))
		_, _ = h.Write([]byte{0})
		_, _ = io.WriteString(h, e.Digest)
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func filterArchive(source, output *os.File, include map[string]struct{}, maxBytes int64) (err error) {
	tr, closeIn, err := openArchive(source)
	if err != nil {
		return err
	}
	defer closeIn()
	tw, closeOut, err := createArchive(output, maxBytes)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := closeOut(); err == nil {
			err = closeErr
		}
		if err != nil {
			clearArchive(output)
		}
	}()
	for {
		hdr, nextErr := tr.Next()
		if nextErr == io.EOF {
			return nil
		}
		if nextErr != nil {
			return fmt.Errorf("read source archive: %w", nextErr)
		}
		name, _, normErr := normalizeHeader(hdr)
		if normErr != nil {
			return normErr
		}
		if _, ok := include[name]; !ok {
			continue
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("write delta header %q: %w", name, err)
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return fmt.Errorf("write delta entry %q: %w", name, err)
		}
	}
}

func mergeArchives(base, delta, output *os.File, removed, replaced map[string]struct{}, maxBytes int64) (err error) {
	tw, closeOut, err := createArchive(output, maxBytes)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := closeOut(); err == nil {
			err = closeErr
		}
		if err != nil {
			clearArchive(output)
		}
	}()
	copyFrom := func(label string, archive *os.File, skip func(string) bool) error {
		tr, closeIn, openErr := openArchive(archive)
		if openErr != nil {
			return openErr
		}
		defer closeIn()
		for {
			hdr, nextErr := tr.Next()
			if nextErr == io.EOF {
				return nil
			}
			if nextErr != nil {
				return fmt.Errorf("read %s archive: %w", label, nextErr)
			}
			name, _, normErr := normalizeHeader(hdr)
			if normErr != nil {
				return normErr
			}
			if skip != nil && skip(name) {
				continue
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return fmt.Errorf("write reconstructed header %q: %w", name, err)
			}
			if _, err := io.Copy(tw, tr); err != nil {
				return fmt.Errorf("write reconstructed entry %q: %w", name, err)
			}
		}
	}
	if err := copyFrom("base", base, func(name string) bool {
		_, drop := removed[name]
		_, replace := replaced[name]
		return drop || replace
	}); err != nil {
		return err
	}
	return copyFrom("delta", delta, nil)
}

func openArchive(archive *os.File) (*tar.Reader, func(), error) {
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return nil, nil, fmt.Errorf("rewind archive: %w", err)
	}
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return nil, nil, fmt.Errorf("open gzip stream: %w", err)
	}
	return tar.NewReader(gz), func() { _ = gz.Close() }, nil
}

type cappedWriter struct {
	w   io.Writer
	max int64
	n   int64
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if w.max > 0 && w.n+int64(len(p)) > w.max {
		return 0, fmt.Errorf("compressed reconstructed source exceeds %d bytes", w.max)
	}
	n, err := w.w.Write(p)
	w.n += int64(n)
	return n, err
}

func createArchive(archive *os.File, maxBytes int64) (*tar.Writer, func() error, error) {
	if err := archive.Truncate(0); err != nil {
		return nil, nil, fmt.Errorf("truncate archive: %w", err)
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return nil, nil, fmt.Errorf("rewind archive: %w", err)
	}
	gz := gzip.NewWriter(&cappedWriter{w: archive, max: maxBytes})
	tw := tar.NewWriter(gz)
	closeAll := func() error {
		if err := tw.Close(); err != nil {
			_ = gz.Close()
			return fmt.Errorf("close tar stream: %w", err)
		}
		if err := gz.Close(); err != nil {
			return fmt.Errorf("close gzip stream: %w", err)
		}
		if err := archive.Sync(); err != nil {
			return fmt.Errorf("sync archive: %w", err)
		}
		return nil
	}
	return tw, closeAll, nil
}

func clearArchive(archive *os.File) {
	_ = archive.Truncate(0)
	_, _ = archive.Seek(0, io.SeekStart)
}
