package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type envPullFile struct {
	path string
	info os.FileInfo
	data []byte
}

func (s envPullFile) exists() bool { return s.info != nil }

func readEnvPullFile(path string) (envPullFile, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return envPullFile{}, err
	}
	snapshot := envPullFile{path: abs}
	pre, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, err
	}
	if pre.Mode()&os.ModeSymlink != 0 {
		return snapshot, errors.New("env pull destination must not be a symlink")
	}
	if !pre.Mode().IsRegular() {
		return snapshot, errors.New("env pull destination must be a regular file")
	}
	f, err := openCustomerFile(abs)
	if err != nil {
		return snapshot, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		return snapshot, err
	}
	info, err := f.Stat()
	if err != nil {
		return snapshot, err
	}
	after, err := os.Lstat(abs)
	if err != nil {
		return snapshot, err
	}
	if !sameEnvPullFile(pre, info) || !sameEnvPullFile(info, after) || int64(len(data)) != info.Size() {
		return snapshot, errors.New("env pull destination changed while reading; retry with the current file")
	}
	snapshot.info, snapshot.data = info, data
	return snapshot, nil
}

func sameEnvPullFile(a, b os.FileInfo) bool {
	return a.Mode().IsRegular() && b.Mode().IsRegular() && os.SameFile(a, b) &&
		a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime())
}

type stagedEnvPullFile struct {
	before envPullFile
	temp   string
}

func stageEnvPullFile(before envPullFile, contents []byte) (*stagedEnvPullFile, error) {
	f, err := os.CreateTemp(filepath.Dir(before.path), ".gregale-env-pull-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create private temporary file: %w", err)
	}
	staged := &stagedEnvPullFile{before: before, temp: f.Name()}
	complete := false
	defer func() {
		_ = f.Close()
		if !complete {
			staged.cleanup()
		}
	}()
	if _, err := f.Write(contents); err != nil {
		return nil, fmt.Errorf("write temporary file: %w", err)
	}
	if err := f.Sync(); err != nil {
		return nil, fmt.Errorf("sync temporary file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close temporary file: %w", err)
	}
	complete = true
	return staged, nil
}

func (s *stagedEnvPullFile) cleanup() { _ = os.Remove(s.temp) }

func (s *stagedEnvPullFile) commit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := readEnvPullFile(s.before.path)
	if err != nil {
		return fmt.Errorf("recheck destination: %w", err)
	}
	if current.exists() != s.before.exists() || current.exists() &&
		(!sameEnvPullFile(current.info, s.before.info) || !bytes.Equal(current.data, s.before.data)) {
		return errors.New("env pull destination changed since planning; no output file was replaced, retry with the current file")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.before.exists() {
		// Publish without replacement if another process creates the destination
		// after our final check. The staged inode already contains the full file.
		if err := os.Link(s.temp, s.before.path); err != nil {
			return fmt.Errorf("publish new .env without replacement: %w", err)
		}
	} else if err := os.Rename(s.temp, s.before.path); err != nil {
		return fmt.Errorf("replace .env: %w", err)
	}
	return nil
}
