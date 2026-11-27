package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Run environment objects have two unprivileged writers with the same primary
// faas group. Preserve group traversal and GC authority even under umask 0077;
// all other key families retain their existing permissions.
func (l *LocalStorageBackend) prepareRunEnvironmentDirectory(key string) error {
	if err := os.MkdirAll(l.root, 0755); err != nil {
		return err
	}
	root, err := os.OpenRoot(l.root)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	parts := strings.Split(filepath.ToSlash(filepath.Dir(key)), "/")
	for i := range parts {
		rel := strings.Join(parts[:i+1], "/")
		if err := root.Mkdir(rel, 0770); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := root.Lstat(rel)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("storage: environment directory is not owned storage")
		}
		if info.Mode().Perm()&0070 != 0070 {
			if err := root.Chmod(rel, 0770|os.ModeSetgid); err != nil {
				return err
			}
		}
	}
	return nil
}
func (l *LocalStorageBackend) pruneRunEnvironmentDirectories(key string) {
	root, err := os.OpenRoot(l.root)
	if err != nil {
		return
	}
	defer func() { _ = root.Close() }()
	dir := filepath.ToSlash(filepath.Dir(key))
	for dir != "run-environments" && strings.HasPrefix(dir, "run-environments/") {
		if err := root.Remove(dir); err != nil {
			return
		}
		dir = filepath.ToSlash(filepath.Dir(dir))
	}
}
