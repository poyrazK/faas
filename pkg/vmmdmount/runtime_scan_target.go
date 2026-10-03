package vmmdmount

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const RuntimeScanTargetPrefix = "imaged-runtime-scan-"

type RuntimeScanTarget struct {
	parent, root     *os.Root
	parentPath, name string
	parentInfo, info os.FileInfo
}

func OpenRuntimeScanTarget(path string) (*RuntimeScanTarget, error) {
	return openRuntimeScanTarget(path, OverlayStagingRoot)
}

func openRuntimeScanTarget(path, parent string) (*RuntimeScanTarget, error) {
	if filepath.Clean(path) != path || filepath.Dir(path) != parent || !strings.HasPrefix(filepath.Base(path), RuntimeScanTargetPrefix) {
		return nil, ErrInvalidOverlayPath
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() {
		return nil, errors.Join(ErrInvalidOverlayPath, err)
	}
	pinned, err := os.OpenRoot(parent)
	if err != nil {
		return nil, err
	}
	target := &RuntimeScanTarget{parent: pinned, parentPath: parent, name: filepath.Base(path), parentInfo: info}
	if err := target.open(); err != nil {
		return nil, errors.Join(err, target.Close())
	}
	return target, nil
}

func (t *RuntimeScanTarget) open() error {
	opened, err := t.parent.Lstat(".")
	if err != nil || !os.SameFile(t.parentInfo, opened) {
		return errors.Join(ErrInvalidOverlayPath, err)
	}
	t.info, err = t.parent.Lstat(t.name)
	if err != nil || !t.info.IsDir() || t.info.Mode().Perm()&0o077 != 0 {
		return errors.Join(ErrInvalidOverlayPath, err)
	}
	t.root, err = t.parent.OpenRoot(t.name)
	if err != nil {
		return err
	}
	return errors.Join(t.Verify(), checkEmptyRuntimeScanTarget(t.root))
}

func checkEmptyRuntimeScanTarget(root *os.Root) (retErr error) {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, f.Close()) }()
	entries, err := f.ReadDir(1)
	if len(entries) != 0 || err != nil && !errors.Is(err, io.EOF) {
		return errors.Join(ErrInvalidOverlayPath, err)
	}
	return nil
}

func (t *RuntimeScanTarget) Verify() error {
	parent, err := os.Lstat(t.parentPath)
	if err != nil || !os.SameFile(t.parentInfo, parent) {
		return errors.Join(ErrInvalidOverlayPath, err)
	}
	path, err := t.parent.Lstat(t.name)
	if err != nil || !path.IsDir() || !os.SameFile(t.info, path) || !sameRuntimeScanTargetMetadata(t.info, path) {
		return errors.Join(ErrInvalidOverlayPath, err)
	}
	opened, err := t.root.Lstat(".")
	if err != nil || !os.SameFile(t.info, opened) {
		return errors.Join(ErrInvalidOverlayPath, err)
	}
	return nil
}

func (t *RuntimeScanTarget) CreateView(name string) (*os.Root, error) {
	if filepath.Base(name) != name || name == "." || name == "" {
		return nil, ErrInvalidOverlayPath
	}
	if err := t.root.Mkdir(name, 0o700); err != nil {
		return nil, err
	}
	if err := ownRuntimeScanView(t.root, name); err != nil {
		return nil, err
	}
	return t.root.OpenRoot(name)
}

func (t *RuntimeScanTarget) Close() error {
	var err error
	if t.root != nil {
		err = t.root.Close()
	}
	if t.parent != nil {
		err = errors.Join(err, t.parent.Close())
	}
	return err
}
