package fcvm

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
)

// forkScrubPaths are the drive1 paths that can hold secret values from the
// capture's original boot (ADR-732): the sealed env projection guest-init
// loads at start, and the ADR-505 reload projection directory. Sidecar env
// overrides are removed per sidecar (scrubSidecarEnvs).
var forkScrubPaths = []string{
	secretsEnvPath,
	"upper/tmp/gregale-secret-reload",
}

// scrubForkSecrets removes forkScrubPaths from the instance's private drive1
// copy. A path that is absent is not an error: many apps never wrote one.
func (v *JailerVMM) scrubForkSecrets(ctx context.Context, owner nativeLaunchRecord, instance string) error {
	drive1, err := v.resolveDriveImage(instance)
	if err != nil {
		return err
	}
	return v.driveStagingSession(ctx, owner, instance, drive1, "faas-vmm-fork-scrub-", scrubDriveSecrets)
}

// scrubDriveSecrets is the mounted-drive half of scrubForkSecrets, split out
// so it is testable against a plain directory.
func scrubDriveSecrets(mountRoot string) error {
	for _, path := range forkScrubPaths {
		target, err := stagedDrivePath(mountRoot, path)
		if err != nil {
			return err
		}
		root, rel, err := openDriveRoot(mountRoot, target)
		if err != nil {
			return err
		}
		err = root.RemoveAll(rel)
		_ = root.Close()
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	return scrubSidecarEnvs(mountRoot)
}

// scrubSidecarEnvs removes each sidecar's env overrides
// (workloads/<name>/env.json). vmmd writes them to the main workload's drive1
// with the sidecar's unsealed env and app secrets, so a restarted sidecar in
// the fork would reload them like secrets.env. The sidecar's workload.json
// beside them is its non-secret contract and stays. Only real directories
// are sidecars: a link the tenant planted under workloads is skipped, and
// os.Root keeps every path inside the drive.
func scrubSidecarEnvs(mountRoot string) error {
	base, err := stagedDrivePath(mountRoot, workloadEnvPath)
	if err != nil {
		return err
	}
	root, rel, err := openDriveRoot(mountRoot, base)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	entries, err := fs.ReadDir(root.FS(), rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", workloadEnvPath, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		env := filepath.Join(rel, entry.Name(), "env.json")
		if err := root.RemoveAll(env); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s/%s/env.json: %w", workloadEnvPath, entry.Name(), err)
		}
	}
	return nil
}
