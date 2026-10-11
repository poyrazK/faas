package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/onebox-faas/faas/pkg/api"
)

type commitRecoveryCheckpoint struct {
	path      string
	protected []string
}

func newCommitRecoveryCheckpoint(path string, protected ...string) (*commitRecoveryCheckpoint, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return nil, fmt.Errorf("resolve checkpoint directory: %w", err)
	}
	checkpoint := &commitRecoveryCheckpoint{path: filepath.Join(dir, filepath.Base(absolute))}
	for _, input := range protected {
		resolved, resolveErr := filepath.EvalSymlinks(input)
		if resolveErr != nil {
			return nil, resolveErr
		}
		resolved, resolveErr = filepath.Abs(resolved)
		if resolveErr != nil {
			return nil, resolveErr
		}
		checkpoint.protected = append(checkpoint.protected, resolved)
	}
	if err = checkpoint.validate(); err != nil {
		return nil, err
	}
	return checkpoint, nil
}

func (c *commitRecoveryCheckpoint) validate() error {
	info, err := os.Lstat(c.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && !info.Mode().IsRegular() {
		return errors.New("checkpoint destination must be a regular file, not a symlink")
	}
	for _, path := range c.protected {
		if path == c.path {
			return errors.New("checkpoint must not overwrite the plan or apply result")
		}
		input, statErr := os.Stat(path)
		if statErr != nil {
			return statErr
		}
		if info != nil && os.SameFile(info, input) {
			return errors.New("checkpoint must not alias the plan or apply result")
		}
	}
	if stdout, statErr := os.Stdout.Stat(); statErr == nil && info != nil && os.SameFile(info, stdout) {
		return errors.New("checkpoint must be separate from stdout")
	}
	return nil
}

func (c *commitRecoveryCheckpoint) save(status api.CommitRecoveryStatusResult) error {
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	if int64(len(data))+1 > api.CommitDeliveryReportFileMaxBytes {
		return errors.New("checkpoint exceeds recovery status file size limit")
	}
	if err = c.validate(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.path), ".commit-recovery-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	if _, err = tmp.Write(append(data, '\n')); err == nil {
		err = tmp.Sync()
	}
	if err == nil {
		err = tmp.Close()
	}
	if err == nil {
		err = c.validate()
	}
	if err == nil {
		err = os.Rename(tmp.Name(), c.path)
	}
	if err == nil && runtime.GOOS != "windows" {
		var dir *os.File
		dir, err = os.OpenFile(filepath.Dir(c.path), os.O_RDONLY, 0)
		if err == nil {
			err = dir.Sync()
			_ = dir.Close()
		}
	}
	if err != nil {
		return fmt.Errorf("persist recovery checkpoint: %w", err)
	}
	return nil
}
