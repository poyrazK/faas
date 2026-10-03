package scanview

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// fs.WalkDir reads a complete directory before invoking its visitor. Read in
// bounded batches instead, and cap the retained names before sorting them.
func walkBounded(ctx context.Context, root *os.Root, limits viewLimits, visit func(string, fs.DirEntry) error) error {
	info, err := root.Lstat(".")
	if err != nil {
		return err
	}
	if limits.entries < 1 || limits.manifest < 1 {
		return ErrLimit
	}
	w := &boundedWalk{root: root, info: info, limits: limits, visit: visit, entries: 1, manifest: 1}
	return w.node(ctx, ".", fs.FileInfoToDirEntry(info))
}

type boundedWalk struct {
	root              *os.Root
	info              fs.FileInfo
	limits            viewLimits
	visit             func(string, fs.DirEntry) error
	entries, manifest int
}

func (w *boundedWalk) node(ctx context.Context, name string, entry fs.DirEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := entry.Info()
	if err != nil {
		return err
	}
	if info.IsDir() && !sameDirectoryDevice(w.info, info) {
		return ErrInvalid
	}
	if err := w.visit(name, fs.FileInfoToDirEntry(info)); err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	entries, err := w.directory(ctx, name, info)
	if err != nil {
		return err
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	for _, child := range entries {
		if err := w.node(ctx, path.Join(name, child.Name()), child); err != nil {
			return err
		}
	}
	return nil
}

func (w *boundedWalk) directory(ctx context.Context, name string, info fs.FileInfo) ([]fs.DirEntry, error) {
	f, err := openRegular(w.root, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.IsDir() || !os.SameFile(info, opened) {
		return nil, ErrChanged
	}
	var entries []fs.DirEntry
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := f.ReadDir(api.ApplicationStandardRuntimeScanDirectoryReadBatch)
		for _, child := range batch {
			if err := w.retain(path.Join(name, child.Name())); err != nil {
				return nil, err
			}
			entries = append(entries, child)
		}
		if err == io.EOF {
			return entries, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func (w *boundedWalk) retain(name string) error {
	if len(name) > api.ApplicationStandardScanMaxPathBytes || w.entries >= w.limits.entries {
		return ErrLimit
	}
	w.entries++
	w.manifest += len(name)
	if w.manifest > w.limits.manifest {
		return ErrLimit
	}
	return nil
}
