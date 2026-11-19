// adr: 20261029171525 amendment 2
package fcvm

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
)

// A deferred dependency cache export run boots on the drive its build left:
// BuildKit's state from the build is there, so its solve spends its time on
// the export the build skipped. That drive is guest-written, so the host
// never edits it (ADR-20261025090100). vmmd gives the run its inputs here,
// in the guest disk sandbox, as it stages a job's manifest.
const (
	cacheExportManifestPath = "upper/etc/faas/build.json"
	cacheExportEntropyPath  = "upper/etc/faas/entropy.seed"
	cacheExportDonePath     = "upper/etc/faas/build-done.json"
	// cacheExportEntropyBytes matches the seed builderd stages for a build.
	cacheExportEntropyBytes = 256
)

// stageCacheExportRunForOwner turns a deferred build's drive1 into its cache
// export run's: the run's build.json, a fresh one-shot entropy seed, and no
// build result. editDrive replays the build's journal first: the builder
// powered off without unmounting drive1, and a journal replayed only at the
// guest's mount would overwrite these files with the build's own. Without the
// old build-done.json, a run that dies early leaves no result for Destroy to
// copy, so it publishes nothing.
func (v *JailerVMM) stageCacheExportRunForOwner(ctx context.Context, owner nativeLaunchRecord, instance string, manifest []byte) error {
	if v.chrootBase == "" {
		return fmt.Errorf("vmm: stageCacheExportRun: chrootBase not configured")
	}
	drive1Img := filepath.Join(v.chrootRoot(instance), layerImageName)
	if _, err := os.Stat(drive1Img); err != nil {
		return fmt.Errorf("vmm: stageCacheExportRun: %s missing at %s: %w", layerImageName, drive1Img, err)
	}
	seed := make([]byte, cacheExportEntropyBytes)
	if _, err := rand.Read(seed); err != nil {
		return fmt.Errorf("vmm: stageCacheExportRun: entropy seed: %w", err)
	}
	files := []driveFile{
		{what: "vmm: stageCacheExportRun: build.json", path: cacheExportManifestPath, want: manifest, mode: 0o600},
		{what: "vmm: stageCacheExportRun: entropy seed", path: cacheExportEntropyPath, want: seed, mode: 0o600},
	}
	return v.editDrive(ctx, owner, instance, drive1Img, func(img driveImage) error {
		return stageCacheExportFiles(ctx, img, files)
	}, nil)
}

// stageCacheExportFiles removes the build's result and writes files on an
// opened drive, in the layout the drive declares.
func stageCacheExportFiles(ctx context.Context, img driveImage, files []driveFile) error {
	paths := []string{cacheExportDonePath}
	for _, f := range files {
		paths = append(paths, f.path)
	}
	layout, err := inspectDrive(ctx, img, paths, nil)
	if err != nil {
		return fmt.Errorf("vmm: stageCacheExportRun: inspect drive: %w", err)
	}
	if err := img.RemoveAll(ctx, []string{driveImagePath(layout.fullRootfs, cacheExportDonePath)}); err != nil {
		return fmt.Errorf("vmm: stageCacheExportRun: remove build result: %w", err)
	}
	return stageDriveFiles(ctx, img, layout, files)
}
