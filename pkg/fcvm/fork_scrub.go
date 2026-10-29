package fcvm

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/guestdisk"
)

// forkScrubPaths are the drive1 paths that can hold secret values from the
// capture's original boot (ADR-732): the sealed env projection guest-init
// loads at start, and the ADR-505 reload projection directory. Sidecar env
// overrides are removed per sidecar (scrubDriveSecrets).
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
	return v.editDrive(ctx, owner, instance, drive1, func(img driveImage) error {
		return scrubDriveSecrets(ctx, img)
	}, nil)
}

// maxSidecarScrubEntries bounds the workloads directory listing.
const maxSidecarScrubEntries = 1024

// scrubDriveSecrets is the drive half of scrubForkSecrets. Besides
// forkScrubPaths it removes each sidecar's env overrides
// (workloads/<name>/env.json): vmmd writes them to the main workload's drive1
// with the sidecar's unsealed env and app secrets, so a restarted sidecar in
// the fork would reload them like secrets.env. The sidecar's workload.json
// beside them is its non-secret contract and stays. Only real directories
// with a valid workload name are sidecars vmmd wrote; a link or other entry
// the tenant planted under workloads is skipped.
func scrubDriveSecrets(ctx context.Context, img driveImage) error {
	layout, err := inspectDrive(ctx, img, append(append([]string(nil), forkScrubPaths...), workloadEnvPath), nil)
	if err != nil {
		return err
	}
	var paths []string
	for _, p := range forkScrubPaths {
		paths = append(paths, driveImagePath(layout.fullRootfs, p))
	}
	base := driveImagePath(layout.fullRootfs, workloadEnvPath)
	reachable := true
	for _, dir := range imageComponents(base) {
		if layout.entries[dir].Kind != guestdisk.Directory {
			reachable = false
			break
		}
	}
	if reachable {
		listing, err := img.List(ctx, []string{base}, maxSidecarScrubEntries)
		if err != nil {
			return fmt.Errorf("read %s: %w", workloadEnvPath, err)
		}
		for _, e := range listing[base] {
			if e.Kind == guestdisk.Directory && validWorkloadName(e.Name) {
				paths = append(paths, base+"/"+e.Name+"/env.json")
			}
		}
	}
	if err := img.RemoveAll(ctx, paths); err != nil {
		return fmt.Errorf("remove secrets: %w", err)
	}
	return nil
}
