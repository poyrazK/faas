package scanview

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// Copy hands off readable files and root-confined links from a protected view.
// Runtime approval needs an already composed native filesystem; copying an
// unmerged app upper remains component evidence. Exact source snapshots fence
// the copy and identify its rewritten tree. No bootable image is made.
func Copy(ctx context.Context, source, target string, expected Tree) (actual Tree, err error) {
	src, err := openDirectory(source)
	if err != nil {
		return Tree{}, err
	}
	defer src.Close()
	dst, err := openDirectory(target)
	if err != nil {
		return Tree{}, err
	}
	defer dst.Close()
	c, err := newProjectionCopy(ctx, src, dst, expected)
	if err != nil {
		return Tree{}, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, c.cleanup())
		}
	}()
	if err = c.walk(ctx); err != nil {
		return Tree{}, err
	}
	return c.verify(ctx, expected)
}

func newProjectionCopy(ctx context.Context, src, dst *os.Root, expected Tree) (*projectionCopy, error) {
	if err := requireEmptyDirectory(dst); err != nil {
		return nil, err
	}
	before, err := snapshotRoot(ctx, src, defaultLimits())
	if err != nil {
		return nil, err
	}
	if before != expected {
		return nil, ErrChanged
	}
	info, err := src.Lstat(".")
	if err != nil {
		return nil, err
	}
	missing, err := randomMissingTarget()
	if err != nil {
		return nil, err
	}
	return &projectionCopy{source: src, target: dst, info: info, entries: 1, manifest: 1, missing: missing}, nil
}

func requireEmptyDirectory(root *os.Root) error {
	f, err := openRegular(root, ".")
	if err != nil {
		return err
	}
	defer f.Close()
	entries, err := f.ReadDir(1)
	if len(entries) != 0 {
		return ErrInvalid
	}
	if err == io.EOF {
		return nil
	}
	return errors.Join(ErrInvalid, err)
}

func (c *projectionCopy) walk(ctx context.Context) error {
	return walkBounded(ctx, c.source, defaultLimits(), func(name string, entry fs.DirEntry) error {
		if name == "." {
			return nil
		}
		return c.node(ctx, name, entry)
	})
}

func (c *projectionCopy) verify(ctx context.Context, expected Tree) (Tree, error) {
	after, err := snapshotRoot(ctx, c.source, defaultLimits())
	if err != nil {
		return Tree{}, err
	}
	if after != expected {
		return Tree{}, ErrChanged
	}
	actual, err := snapshotRoot(ctx, c.target, defaultLimits())
	if err != nil {
		return Tree{}, err
	}
	if actual.ProjectionDigest != expected.ProjectionDigest || actual.Entries != expected.Entries || actual.Bytes != expected.Bytes {
		return Tree{}, ErrChanged
	}
	return actual, ctx.Err()
}

type projectionCopy struct {
	source, target    *os.Root
	created           []string
	missing           string
	bytes             int64
	info              fs.FileInfo
	entries, manifest int
}

func (c *projectionCopy) node(ctx context.Context, name string, entry fs.DirEntry) error {
	if len(name) > api.ApplicationStandardScanMaxPathBytes || c.entries >= defaultLimits().entries {
		return ErrLimit
	}
	c.entries++
	c.manifest += len(name)
	if c.manifest > defaultLimits().manifest {
		return ErrLimit
	}
	info, err := entry.Info()
	if err != nil {
		return err
	}
	switch {
	case info.IsDir():
		if !sameDirectoryDevice(c.info, info) {
			return ErrInvalid
		}
		err = c.target.Mkdir(name, 0o755)
	case info.Mode().IsRegular():
		err = c.file(ctx, name, info)
	case info.Mode()&os.ModeSymlink != 0:
		err = c.link(ctx, name)
	default:
		err = ErrInvalid
	}
	if err == nil && !info.Mode().IsRegular() {
		c.created = append(c.created, name)
	}
	return err
}

func (c *projectionCopy) link(ctx context.Context, name string) error {
	raw, err := c.source.Readlink(name)
	if err != nil {
		return err
	}
	target, err := virtualLink(ctx, c.source, name, raw)
	if err != nil {
		return err
	}
	c.manifest += len(raw) + len(target)
	if c.manifest > defaultLimits().manifest {
		return ErrLimit
	}
	if target == brokenLink {
		target = c.missing
	}
	target, err = relativeLink(name, target)
	if err != nil {
		return err
	}
	return c.target.Symlink(target, name)
}

func (c *projectionCopy) file(ctx context.Context, name string, info fs.FileInfo) error {
	if info.Size() < 0 || info.Size() > defaultLimits().bytes-c.bytes {
		return ErrLimit
	}
	out, err := c.target.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	c.created = append(c.created, name)
	_, readErr := readRegular(ctx, c.source, name, info, out)
	err = errors.Join(readErr, out.Sync(), out.Close())
	if err == nil {
		c.bytes += info.Size()
	}
	return err
}

func (c *projectionCopy) cleanup() error {
	var err error
	for i := len(c.created) - 1; i >= 0; i-- {
		if e := c.target.Remove(c.created[i]); e != nil && !errors.Is(e, fs.ErrNotExist) {
			err = errors.Join(err, e)
		}
	}
	return err
}

func randomMissingTarget() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "/.faas-scan-missing-" + hex.EncodeToString(b[:]), nil
}
