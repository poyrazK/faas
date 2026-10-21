// Package scanview verifies a complete, bounded filesystem view for Grype.
// It does not approve producer artifacts, native runtime admission or rollout.
package scanview

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
)

const Version = 1

var (
	ErrInvalid = errors.New("scanview: unsupported filesystem view")
	ErrLimit   = errors.New("scanview: filesystem view exceeds safety bound")
	ErrChanged = errors.New("scanview: filesystem view changed")
)

// Tree binds paths, types, regular-file bytes and raw symlink targets. The
// separate projection digest binds what those links resolve to inside the guest.
// Mode/owner metadata is intentionally outside this scanner-only identity:
// projection files are readable copies, not bootable runtime artifacts.
type Tree struct {
	Version          uint32
	Digest           string
	ProjectionDigest string
	Entries          int
	Bytes            int64
}

type viewLimits struct {
	entries, manifest int
	bytes             int64
}

func defaultLimits() viewLimits {
	return viewLimits{api.ApplicationStandardRuntimeScanMaxEntries, api.ApplicationStandardRuntimeScanMaxManifestBytes, api.ApplicationStandardRuntimeScanMaxBytes}
}

type treeWalk struct {
	root          *os.Root
	info          fs.FileInfo
	limits        viewLimits
	hash          hash.Hash
	rawHash       hash.Hash
	result        Tree
	manifestBytes int
}

func Snapshot(ctx context.Context, dir string) (Tree, error) {
	root, err := openDirectory(dir)
	if err != nil {
		return Tree{}, err
	}
	defer func() { _ = root.Close() }()
	return snapshotRoot(ctx, root, defaultLimits())
}

func openDirectory(dir string) (*os.Root, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalid
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	opened, err := root.Lstat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = root.Close()
		return nil, errors.Join(ErrChanged, err)
	}
	return root, nil
}

func snapshotRoot(ctx context.Context, root *os.Root, limits viewLimits) (Tree, error) {
	info, err := root.Lstat(".")
	if err != nil {
		return Tree{}, err
	}
	w := &treeWalk{root: root, info: info, limits: limits, hash: sha256.New(), rawHash: sha256.New(), result: Tree{Version: Version}}
	_, _ = w.hash.Write([]byte("gregale.runtime-scan-projection.v1\x00"))
	_, _ = w.rawHash.Write([]byte("gregale.runtime-scan-tree.v1\x00"))
	err = walkBounded(ctx, root, limits, func(name string, entry fs.DirEntry) error {
		return w.record(ctx, name, entry)
	})
	if err != nil {
		return Tree{}, err
	}
	w.result.Digest = hex.EncodeToString(w.rawHash.Sum(nil))
	w.result.ProjectionDigest = hex.EncodeToString(w.hash.Sum(nil))
	return w.result, nil
}

func (w *treeWalk) record(ctx context.Context, name string, entry fs.DirEntry) error {
	if len(name) > api.ApplicationStandardScanMaxPathBytes || w.result.Entries >= w.limits.entries {
		return ErrLimit
	}
	info, err := entry.Info()
	if err != nil {
		return err
	}
	w.result.Entries++
	w.manifestBytes += len(name)
	if w.manifestBytes > w.limits.manifest {
		return ErrLimit
	}
	w.field(name)
	switch {
	case info.IsDir():
		if !sameDirectoryDevice(w.info, info) {
			return ErrInvalid
		}
		w.field("directory")
	case info.Mode().IsRegular():
		return w.regular(ctx, name, info)
	case info.Mode()&os.ModeSymlink != 0:
		return w.link(ctx, name)
	default:
		return ErrInvalid // Devices, sockets and FIFOs are never opened.
	}
	return nil
}

func (w *treeWalk) regular(ctx context.Context, name string, info fs.FileInfo) error {
	if info.Size() < 0 || info.Size() > w.limits.bytes-w.result.Bytes {
		return ErrLimit
	}
	digest, err := readRegular(ctx, w.root, name, info, io.Discard)
	if err != nil {
		return err
	}
	w.result.Bytes += info.Size()
	w.field("file")
	w.field(digest)
	writeNumber(w.hash, uint64(info.Size()))
	writeNumber(w.rawHash, uint64(info.Size()))
	return nil
}

func (w *treeWalk) link(ctx context.Context, name string) error {
	raw, err := w.root.Readlink(name)
	if err != nil {
		return err
	}
	target, err := virtualLink(ctx, w.root, name, raw)
	if err != nil {
		return err
	}
	w.manifestBytes += len(raw) + len(target)
	if w.manifestBytes > w.limits.manifest {
		return ErrLimit
	}
	w.field("symlink")
	writeField(w.hash, target)
	writeField(w.rawHash, raw)
	return nil
}

func (w *treeWalk) field(value string) { writeField(w.hash, value); writeField(w.rawHash, value) }

func readRegular(ctx context.Context, root *os.Root, name string, info fs.FileInfo, dst io.Writer) (string, error) {
	f, err := openRegular(root, name)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	before, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() || !os.SameFile(info, before) || before.Size() != info.Size() || !before.ModTime().Equal(info.ModTime()) {
		return "", ErrChanged
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), io.LimitReader(contextReader{ctx, f}, info.Size()+1))
	if err != nil {
		return "", err
	}
	after, err := f.Stat()
	if err != nil {
		return "", err
	}
	if n != info.Size() || !os.SameFile(before, after) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return "", ErrChanged
	}
	return hex.EncodeToString(h.Sum(nil)), ctx.Err()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
func writeField(h hash.Hash, value string) {
	writeNumber(h, uint64(len(value)))
	_, _ = h.Write([]byte(value))
}
func writeNumber(h hash.Hash, value uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], value)
	_, _ = h.Write(b[:])
}

func relativeLink(name, target string) (string, error) {
	resolved := target[1:]
	if resolved == "" {
		resolved = "."
	}
	return filepath.Rel(filepath.FromSlash(path.Dir(name)), filepath.FromSlash(resolved))
}
