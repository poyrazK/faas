package fcvm

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
)

// forkScrubPaths are the drive1 paths that can hold secret values from the
// capture's original boot (ADR-732): the sealed env projection guest-init
// loads at start, and the ADR-505 reload projection directory.
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
	return nil
}
