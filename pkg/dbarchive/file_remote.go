package dbarchive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FileRemote is a Remote over a directory, for tests and single-host
// development; production uses S3Remote.
type FileRemote struct {
	Dir string
}

var _ Remote = (*FileRemote)(nil)

func (r *FileRemote) path(key string) (string, error) {
	clean := filepath.Clean(key)
	if key == "" || filepath.IsAbs(key) || clean != key || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("dbarchive: invalid key %q", key)
	}
	return filepath.Join(r.Dir, clean), nil
}

func (r *FileRemote) Put(_ context.Context, key string, body io.ReadSeeker, size int64) error {
	path, err := r.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".put-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	n, err := io.Copy(tmp, body)
	if err == nil && n != size {
		err = io.ErrUnexpectedEOF
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (r *FileRemote) Get(_ context.Context, key string, w io.Writer) error {
	path, err := r.path(key)
	if err != nil {
		return err
	}
	f, err := os.Open(path) //nolint:gosec,forbidigo // a validated key under the remote's directory
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(w, f)
	return err
}

func (r *FileRemote) Delete(_ context.Context, key string) error {
	path, err := r.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (r *FileRemote) List(_ context.Context, prefix string) ([]string, error) {
	var keys []string
	err := filepath.WalkDir(r.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".put-") {
			return nil
		}
		rel, err := filepath.Rel(r.Dir, path)
		if err != nil {
			return err
		}
		if key := filepath.ToSlash(rel); strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
		return nil
	})
	return keys, err
}
