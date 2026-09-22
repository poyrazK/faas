package deploycontroller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/onebox-faas/faas/pkg/releasebundle"
	"github.com/onebox-faas/faas/pkg/releaseinstall"
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
	// The release directory is already installed on the host, so a prior
	// activation may have written the operator-owned KGV baseline sidecar
	// beside the signed bundle. Apply the same installed-release policy the
	// rest of this controller uses; the strict walk would reject a rerun.
	if err := verifyInstalledRelease(releaseRoot, manifest); err != nil {
		return fmt.Errorf("deploycontroller: verify release %q: %w", releaseID, err)
	}

	previous, err := readCurrentTarget(c.config.CurrentPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deploycontroller: read current release: %w", err)
	}
	if previous == releaseRoot {
		// The immutable bundle was verified above before we trusted the
		// current pointer. Treat an exact active release as converged so a CD
		// rerun can continue with its independent post-activation gates after
		// one of those gates failed on the first attempt.
		return nil
	}
	rollbackTarget := previous
	// Prefer the active release as the rollback target. An operator hotfix can
	// legitimately make that immutable directory fail verification, though,
	// and refusing every future signed release leaves the host permanently
	// wedged on the hotfix. In that case, continue only when another retained
	// release verifies as a complete rollback target. The active directory is
	// never rewritten or trusted, and rollback re-verifies the fallback before
	// activating it.
	if previous != "" {
		if _, statErr := os.Stat(previous); statErr == nil {
			previousManifest, readErr := releasebundle.Read(previous)
			currentErr := readErr
			if currentErr == nil {
				currentErr = verifyInstalledRelease(previous, previousManifest)
			}
			if currentErr != nil {
				fallback, fallbackErr := newestVerifiedRollback(c.config.ReleasesRoot, releaseRoot, previous)
				if fallbackErr != nil {
					return fmt.Errorf("deploycontroller: current release is not rollback-capable and no verified retained fallback exists: %w", errors.Join(currentErr, fallbackErr))
				}
				rollbackTarget = fallback
			}
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("deploycontroller: stat current release: %w", statErr)
		}
	}

	if err := c.runtime.Preflight(ctx, manifest, releaseRoot); err != nil {
		return fmt.Errorf("deploycontroller: preflight %q: %w", releaseID, err)
	}
	if err := c.runtime.Migrate(ctx, manifest, releaseRoot, rollbackTarget); err != nil {
		return fmt.Errorf("deploycontroller: migrate %q: %w", releaseID, err)
	}
	if err := c.runtime.Activate(ctx, releaseRoot); err != nil {
		return c.rollback(ctx, releaseID, rollbackTarget, fmt.Errorf("activate: %w", err))
	}
	if err := activatePointer(c.config.CurrentPath, releaseRoot); err != nil {
		return c.rollback(ctx, releaseID, rollbackTarget, fmt.Errorf("publish: %w", err))
	}
	if err := c.runtime.Restart(ctx, manifest); err != nil {
		return c.rollback(ctx, releaseID, rollbackTarget, err)
	}
	if err := c.runtime.Healthy(ctx, manifest); err != nil {
		return c.rollback(ctx, releaseID, rollbackTarget, err)
	}
	if _, err := releaseretention.Prune(c.config.ReleasesRoot, c.config.CurrentPath, releaseretention.DefaultKeepPrevious); err != nil {
		return fmt.Errorf("deploycontroller: release %q is healthy but retention failed: %w", releaseID, err)
	}
	return nil
}

type rollbackCandidate struct {
	path    string
	modTime int64
}

// newestVerifiedRollback finds the most recently modified complete release
// other than the candidate and the drifted active directory. Retention keeps a
// bounded set of these directories specifically for recovery.
func newestVerifiedRollback(releasesRoot string, excluded ...string) (string, error) {
	excludedPaths := make(map[string]struct{}, len(excluded))
	for _, path := range excluded {
		excludedPaths[filepath.Clean(path)] = struct{}{}
	}

	entries, err := os.ReadDir(releasesRoot)
	if err != nil {
		return "", fmt.Errorf("read releases root: %w", err)
	}
	candidates := make([]rollbackCandidate, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(releasesRoot, entry.Name())
		if _, skip := excludedPaths[filepath.Clean(path)]; skip {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		candidates = append(candidates, rollbackCandidate{path: path, modTime: info.ModTime().UnixNano()})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].modTime == candidates[j].modTime {
			return candidates[i].path > candidates[j].path
		}
		return candidates[i].modTime > candidates[j].modTime
	})

	for _, candidate := range candidates {
		manifest, readErr := releasebundle.Read(candidate.path)
		if readErr != nil || manifest.ReleaseID != filepath.Base(candidate.path) {
			continue
		}
		if verifyErr := verifyInstalledRelease(candidate.path, manifest); verifyErr == nil {
			return candidate.path, nil
		}
	}
	return "", errors.New("no complete retained release")
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

// Rollback activates the newest verified retained release other than the one
// currently active, and reports which release it activated.
//
// Deploy is already transactional across its own steps: activate, publish,
// restart and health each unwind to the previous release. What it cannot see
// is the CD pipeline's POST-activation gates — the liveness, public-customer-
// path and metering probes that run after Deploy has already returned
// success. Deploy's own comment anticipates one of those gates failing and
// offers only "rerun CD"; this is the other half of that story, so a gate
// failure can put the fleet back instead of leaving a bad release serving.
//
// The lock is the same one Deploy takes, so a rollback cannot interleave with
// a deployment. The target is re-verified before activation: a retained
// directory that has drifted is never activated just because it is the
// newest.
func (c *Controller) Rollback(ctx context.Context) (string, error) {
	lock, err := acquireLock(c.config.LockPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = lock.Close() }()

	current, err := readCurrentTarget(c.config.CurrentPath)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("deploycontroller: rollback: read current release: %w", err)
	}
	if current == "" {
		return "", errors.New("deploycontroller: rollback: no release is active")
	}

	target, err := newestVerifiedRollback(c.config.ReleasesRoot, current)
	if err != nil {
		return "", fmt.Errorf("deploycontroller: rollback: no verified retained release to roll back to: %w", err)
	}
	if err := c.activateRelease(ctx, target); err != nil {
		return "", fmt.Errorf("deploycontroller: rollback to %q: %w", filepath.Base(target), err)
	}
	return target, nil
}

// activateRelease runs the activate → publish → restart → health sequence
// against an already-retained release directory, verifying it first. Shared by
// the standalone Rollback verb and Deploy's internal unwind so the two cannot
// drift in what "activating a release" means.
func (c *Controller) activateRelease(ctx context.Context, target string) error {
	manifest, err := releasebundle.Read(target)
	if err != nil {
		return fmt.Errorf("read release: %w", err)
	}
	if err := verifyInstalledRelease(target, manifest); err != nil {
		return fmt.Errorf("verify release: %w", err)
	}
	if err := c.runtime.Activate(ctx, target); err != nil {
		return fmt.Errorf("activate: %w", err)
	}
	if err := activatePointer(c.config.CurrentPath, target); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	if err := c.runtime.Restart(ctx, manifest); err != nil {
		return fmt.Errorf("restart: %w", err)
	}
	if err := c.runtime.Healthy(ctx, manifest); err != nil {
		return fmt.Errorf("unhealthy: %w", err)
	}
	return nil
}

func (c *Controller) rollback(ctx context.Context, releaseID, previous string, cause error) error {
	if previous == "" {
		return fmt.Errorf("deploycontroller: release %q failed without previous release: %w", releaseID, cause)
	}
	if err := c.activateRelease(ctx, previous); err != nil {
		return fmt.Errorf("deploycontroller: release %q failed; rollback: %w: %w", releaseID, err, cause)
	}
	return fmt.Errorf("deploycontroller: release %q rolled back: %w", releaseID, cause)
}

// verifyInstalledRelease permits the mutable CVE baseline that the installer
// records after activating an otherwise immutable release. Candidate releases
// still use strict releasebundle.Verify before activation.
func verifyInstalledRelease(root string, manifest releasebundle.Manifest) error {
	return releasebundle.VerifyWithAllowedFiles(root, manifest, releaseinstall.SBOMBaselineName)
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
