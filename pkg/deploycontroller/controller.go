package deploycontroller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/onebox-faas/faas/pkg/releasebundle"
	"github.com/onebox-faas/faas/pkg/releaseretention"
)

type Runtime interface {
	Preflight(context.Context, releasebundle.Manifest, string) error
	Migrate(context.Context, releasebundle.Manifest, string, string) error
	Activate(context.Context, string) error
	Restart(context.Context, releasebundle.Manifest) error
	Healthy(context.Context, releasebundle.Manifest) error
}

type Config struct {
	ReleasesRoot string
	CurrentPath  string
	LockPath     string
}

type Controller struct {
	config  Config
	runtime Runtime
}

func New(config Config, runtime Runtime) (*Controller, error) {
	if config.ReleasesRoot == "" || config.CurrentPath == "" || config.LockPath == "" {
		return nil, errors.New("deploycontroller: incomplete config")
	}
	if runtime == nil {
		return nil, errors.New("deploycontroller: nil runtime")
	}
	return &Controller{config: config, runtime: runtime}, nil
}

func (c *Controller) Deploy(ctx context.Context, releaseID string) error {
	lock, err := acquireLock(c.config.LockPath)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()

	releaseRoot := filepath.Join(c.config.ReleasesRoot, releaseID)
	manifest, err := releasebundle.Read(releaseRoot)
	if err != nil {
		return fmt.Errorf("deploycontroller: read release %q: %w", releaseID, err)
	}
	if manifest.ReleaseID != releaseID {
		return fmt.Errorf("deploycontroller: release id %q does not match manifest %q", releaseID, manifest.ReleaseID)
	}
	if err := releasebundle.Verify(releaseRoot, manifest); err != nil {
		return fmt.Errorf("deploycontroller: verify release %q: %w", releaseID, err)
	}

	previous, err := readCurrentTarget(c.config.CurrentPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deploycontroller: read current release: %w", err)
	}
	if previous == releaseRoot {
		return errors.New("deploycontroller: release is already active")
	}
	// Never replace a usable release when the current pointer names a
	// directory that cannot be verified for rollback. A dangling legacy
	// pointer is tolerated for first installation; an existing but incomplete
	// release is a hard preflight error so activation cannot leave the host
	// without a safe recovery target.
	if previous != "" {
		if _, statErr := os.Stat(previous); statErr == nil {
			previousManifest, readErr := releasebundle.Read(previous)
			if readErr != nil {
				return fmt.Errorf("deploycontroller: current release is not rollback-capable: %w", readErr)
			}
			if verifyErr := releasebundle.Verify(previous, previousManifest); verifyErr != nil {
				return fmt.Errorf("deploycontroller: current release is not rollback-capable: %w", verifyErr)
			}
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("deploycontroller: stat current release: %w", statErr)
		}
	}

	if err := c.runtime.Preflight(ctx, manifest, releaseRoot); err != nil {
		return fmt.Errorf("deploycontroller: preflight %q: %w", releaseID, err)
	}
	if err := c.runtime.Migrate(ctx, manifest, releaseRoot, previous); err != nil {
		return fmt.Errorf("deploycontroller: migrate %q: %w", releaseID, err)
	}
	if err := c.runtime.Activate(ctx, releaseRoot); err != nil {
		return c.rollback(ctx, releaseID, previous, fmt.Errorf("activate: %w", err))
	}
	if err := activatePointer(c.config.CurrentPath, releaseRoot); err != nil {
		return c.rollback(ctx, releaseID, previous, fmt.Errorf("publish: %w", err))
	}
	if err := c.runtime.Restart(ctx, manifest); err != nil {
		return c.rollback(ctx, releaseID, previous, err)
	}
	if err := c.runtime.Healthy(ctx, manifest); err != nil {
		return c.rollback(ctx, releaseID, previous, err)
	}
	if _, err := releaseretention.Prune(c.config.ReleasesRoot, c.config.CurrentPath, releaseretention.DefaultKeepPrevious); err != nil {
		return fmt.Errorf("deploycontroller: release %q is healthy but retention failed: %w", releaseID, err)
	}
	return nil
}

// readCurrentTarget normalizes a relative current symlink against the
// symlink's directory. Older installers published `current -> releases/<id>`
// while the controller now writes an absolute target; without this
// normalization rollback tries to read `releases/<id>/manifest.json` from
// the process working directory instead of `/opt/faas/releases/<id>`.
func readCurrentTarget(path string) (string, error) {
	target, err := os.Readlink(path)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return filepath.Clean(target), nil
}

func (c *Controller) rollback(ctx context.Context, releaseID, previous string, cause error) error {
	if previous == "" {
		return fmt.Errorf("deploycontroller: release %q failed without previous release: %w", releaseID, cause)
	}
	previousManifest, err := releasebundle.Read(previous)
	if err != nil {
		return fmt.Errorf("deploycontroller: release %q failed; read rollback release: %w: %w", releaseID, err, cause)
	}
	if err := releasebundle.Verify(previous, previousManifest); err != nil {
		return fmt.Errorf("deploycontroller: release %q failed; verify rollback release: %w: %w", releaseID, err, cause)
	}
	if err := c.runtime.Activate(ctx, previous); err != nil {
		return fmt.Errorf("deploycontroller: release %q failed; activate rollback: %w: %w", releaseID, err, cause)
	}
	if err := activatePointer(c.config.CurrentPath, previous); err != nil {
		return fmt.Errorf("deploycontroller: release %q failed; publish rollback: %w: %w", releaseID, err, cause)
	}
	if err := c.runtime.Restart(ctx, previousManifest); err != nil {
		return fmt.Errorf("deploycontroller: release %q failed; restart rollback: %w: %w", releaseID, err, cause)
	}
	if err := c.runtime.Healthy(ctx, previousManifest); err != nil {
		return fmt.Errorf("deploycontroller: release %q failed; rollback unhealthy: %w: %w", releaseID, err, cause)
	}
	return fmt.Errorf("deploycontroller: release %q rolled back: %w", releaseID, cause)
}

type fileLock struct {
	file *os.File
}

func acquireLock(path string) (*fileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("deploycontroller: create lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("deploycontroller: open lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("deploycontroller: another deployment is active")
		}
		return nil, fmt.Errorf("deploycontroller: acquire lock: %w", err)
	}
	return &fileLock{file: file}, nil
}

func activatePointer(path, releaseRoot string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create pointer directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".current-*.tmp")
	if err != nil {
		return fmt.Errorf("create pointer temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Remove(tmpPath); err != nil {
		return err
	}
	if err := os.Symlink(releaseRoot, tmpPath); err != nil {
		return fmt.Errorf("create pointer symlink: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("publish pointer: %w", err)
	}
	return nil
}

func (l *fileLock) Close() error {
	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
		_ = l.file.Close()
		return err
	}
	return l.file.Close()
}
