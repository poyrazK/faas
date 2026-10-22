package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/gofrs/flock"

	"github.com/onebox-faas/faas/pkg/api"
)

type commitRecoveryCheckpoint struct {
	path      string
	protected []string
	guard     *flock.Flock
	guardInfo os.FileInfo
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
	if c.guard != nil {
		info, err := os.Lstat(c.path + ".lock")
		if !c.guard.Locked() || err != nil || !info.Mode().IsRegular() || !os.SameFile(info, c.guardInfo) {
			return errors.New("recovery checkpoint lock file changed or ownership was lost; stop other writers before restarting")
		}
	}
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

func (c *commitRecoveryCheckpoint) save(status any) error {
	if c.guard == nil || !c.guard.Locked() {
		return errors.New("recovery checkpoint ownership is not held")
	}
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

// lock holds a stable sidecar inode across checkpoint replacements. Never unlink
// that sidecar: another process could otherwise acquire a different lock inode.
func (c *commitRecoveryCheckpoint) lock(statusInputs ...string) error {
	lockPath := c.path + ".lock"
	info, err := os.Lstat(lockPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && (!info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0)) {
		return errors.New("recovery checkpoint lock must be a private regular file, not a symlink")
	}
	protected := append([]string{c.path}, c.protected...)
	for _, input := range statusInputs {
		if input == "" {
			continue
		}
		resolved, resolveErr := filepath.EvalSymlinks(input)
		if resolveErr != nil {
			return resolveErr
		}
		resolved, resolveErr = filepath.Abs(resolved)
		if resolveErr != nil {
			return resolveErr
		}
		protected = append(protected, resolved)
	}
	for _, input := range protected {
		if input == lockPath {
			return errors.New("recovery checkpoint lock must not overwrite an input")
		}
		target, statErr := os.Stat(input)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if info != nil && target != nil && os.SameFile(info, target) {
			return errors.New("recovery checkpoint lock must not alias an input")
		}
	}
	if stdout, statErr := os.Stdout.Stat(); statErr == nil && info != nil && os.SameFile(info, stdout) {
		return errors.New("recovery checkpoint lock must be separate from stdout")
	}
	guard := flock.New(lockPath, flock.SetPermissions(0600))
	locked, err := guard.TryLock()
	if err != nil || !locked {
		_ = guard.Close()
		if err != nil {
			return fmt.Errorf("acquire recovery checkpoint ownership: %w", err)
		}
		return errors.New("recovery checkpoint is owned by another Gregale watch; use a different path or wait for it to exit")
	}
	info, err = os.Lstat(lockPath)
	if err != nil || !info.Mode().IsRegular() {
		_ = guard.Close()
		return errors.New("recovery checkpoint lock changed while acquiring ownership")
	}
	c.guard, c.guardInfo = guard, info
	if err = c.validate(); err != nil {
		_ = guard.Close()
		return err
	}
	return nil
}

func (c *commitRecoveryCheckpoint) close() error {
	if c.guard == nil {
		return nil
	}
	return c.guard.Close()
}
