package deploycontroller

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/releasebundle"
)

type MigrationReport struct {
	ReleaseID          string
	ReleaseRoot        string
	CurrentTarget      string
	CurrentRelease     string
	HasPreviousRelease bool
	LegacyBinDir       bool
	LegacySourceDir    bool
	RequiredPaths      []PathCheck
	StaleScratchFiles  []string
	Warnings           []string
	Actions            []string
}

type PathCheck struct {
	Path   string
	Exists bool
	Reason string
}

// IsControllerStagingEntry reports whether an entry under a base
// staging root is controller-owned scratch: either a faas-base-*
// extraction dir or a faas-base-mkfs-*.ext4 mkfs temp file. Shared by
// the dry-run stale-scratch report and the host runtime cleanup
// (cmd/deployctl/runtime.go) so the two always agree on what is
// controller-owned.
func IsControllerStagingEntry(entry os.DirEntry) bool {
	name := entry.Name()
	if !strings.HasPrefix(name, "faas-base-") {
		return false
	}
	if entry.IsDir() {
		return true
	}
	return strings.HasPrefix(name, "faas-base-mkfs-") && strings.HasSuffix(name, ".ext4")
}

func DryRun(config Config, releaseID string) (MigrationReport, error) {
	if config.ReleasesRoot == "" || config.CurrentPath == "" {
		return MigrationReport{}, fmt.Errorf("deploycontroller: incomplete dry-run config")
	}
	root := filepath.Join(config.ReleasesRoot, releaseID)
	manifest, err := releasebundle.Read(root)
	if err != nil {
		return MigrationReport{}, fmt.Errorf("deploycontroller: dry-run release: %w", err)
	}
	if manifest.ReleaseID != releaseID {
		return MigrationReport{}, fmt.Errorf("deploycontroller: dry-run release id %q does not match manifest %q", releaseID, manifest.ReleaseID)
	}
	if err := releasebundle.Verify(root, manifest); err != nil {
		return MigrationReport{}, fmt.Errorf("deploycontroller: dry-run verify: %w", err)
	}

	report := MigrationReport{ReleaseID: releaseID, ReleaseRoot: root}
	current, err := os.Readlink(config.CurrentPath)
	if err == nil {
		report.CurrentTarget = current
		report.CurrentRelease = filepath.Base(filepath.Clean(current))
	} else if !os.IsNotExist(err) {
		return MigrationReport{}, fmt.Errorf("deploycontroller: dry-run current pointer: %w", err)
	}
	if report.CurrentTarget == root {
		report.Warnings = append(report.Warnings, "requested release is already active")
	}
	if report.CurrentTarget != "" && report.CurrentTarget != root {
		if previousManifest, err := releasebundle.Read(report.CurrentTarget); err == nil {
			if err := releasebundle.Verify(report.CurrentTarget, previousManifest); err == nil {
				report.HasPreviousRelease = true
			}
		}
	}
	if !report.HasPreviousRelease {
		entries, err := os.ReadDir(config.ReleasesRoot)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() || entry.Name() == releaseID || entry.Name() == report.CurrentRelease {
					continue
				}
				candidate := filepath.Join(config.ReleasesRoot, entry.Name())
				if previousManifest, err := releasebundle.Read(candidate); err == nil {
					if err := releasebundle.Verify(candidate, previousManifest); err == nil {
						report.HasPreviousRelease = true
						break
					}
				}
			}
		}
	}

	report.LegacyBinDir = pathExists("/opt/faas/bin")
	report.LegacySourceDir = pathExists("/opt/faas/src/.git")
	for _, path := range []struct {
		path   string
		reason string
	}{
		{filepath.Join(root, "bin", "migrate"), "candidate migration binary"},
		{filepath.Join(root, "bin", "deployctl"), "candidate deployment controller"},
		{filepath.Join(root, "systemd"), "candidate systemd units"},
		{filepath.Join(root, "observability"), "candidate observability assets"},
		{"/opt/faas/releases", "release storage"},
		{"/etc/systemd/system", "systemd unit directory"},
		{"/run/faas", "runtime socket directory"},
		{"/srv/fc/base", "base image storage"},
		{"/srv/fc/base-staging", "disk-backed extraction staging"},
		{"/srv/fc/scans", "base scan sidecar storage"},
		{"/dev/shm", "tmpfs overlay staging"},
	} {
		report.RequiredPaths = append(report.RequiredPaths, PathCheck{Path: path.path, Exists: pathExists(path.path), Reason: path.reason})
	}

	for _, root := range []string{"/dev/shm/faas-base-staging", "/srv/fc/base-staging"} {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !IsControllerStagingEntry(entry) {
				continue
			}
			report.StaleScratchFiles = append(report.StaleScratchFiles, filepath.Join(root, entry.Name()))
		}
	}
	sort.Strings(report.StaleScratchFiles)

	if report.LegacyBinDir && report.CurrentTarget == "" {
		report.Actions = append(report.Actions, "import the legacy /opt/faas/bin state as the first rollback baseline")
	}
	if report.LegacySourceDir {
		report.Warnings = append(report.Warnings, "legacy /opt/faas/src checkout still exists; manual source builds must be retired")
	}
	if len(report.StaleScratchFiles) > 0 {
		report.Actions = append(report.Actions, "review and remove only controller-owned stale base scratch files")
	}
	if !report.HasPreviousRelease {
		report.Warnings = append(report.Warnings, "no verified previous immutable release is available for rollback")
	}
	return report, nil
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
