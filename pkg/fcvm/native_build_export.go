package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// exportNativeBuildArtifacts copies an exited native builder's outputs through
// a journaled read-only mount of its canonical drive (ADR-20261022090000). The
// owner is the revoked, exit-confirmed record of a builder whose enrolled
// producer is this daemon incarnation; exportSession rechecks all of it under
// the instance lock.
func (v *JailerVMM) exportNativeBuildArtifacts(ctx context.Context, owner nativeLaunchRecord, exportDir string) (retErr error) {
	if !owner.Lease.IsBuilder || owner.Producer.Protocol != nativeBuilderExportProducerProtocol {
		return errors.New("native export: builder has no enrolled export producer")
	}
	if err := v.claimNativeBuildExportDestination(exportDir); err != nil {
		return err
	}
	// builderd shares the export through the parent's owner and group; hand
	// ownership back even after a partial export so its cleanup can unlink it.
	defer func() {
		if err := handoffBuildExportOwnership(exportDir); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("handoff export ownership: %w", err))
		}
	}()
	drive := filepath.Join(v.chrootRoot(owner.Lease.Instance), layerImageName)
	loops := nativeLoopMountJournal{owner: v.nativeRecovery.journal, backend: v.nativeRecovery.loopMounts}
	return loops.exportSession(ctx, owner, drive, func(mountRoot string) error {
		return copyBuildExport(mountRoot, exportDir, v.exportMax())
	})
}

// claimNativeBuildExportDestination is the native export's destination
// authority. vmmd writes as root, so the destination must be an absolute,
// clean path outside vmmd's private trees, reached through real directories
// only, and either created here or found empty. A symlinked ancestor or a
// populated directory could redirect or mix root-written output.
func (v *JailerVMM) claimNativeBuildExportDestination(exportDir string) error {
	if !filepath.IsAbs(exportDir) || filepath.Clean(exportDir) != exportDir || exportDir == string(filepath.Separator) {
		return errors.New("native export: destination must be an absolute clean path")
	}
	private := []string{v.chrootBase}
	if v.nativeRecovery != nil && v.nativeRecovery.journal != nil {
		private = append(private, v.nativeRecovery.journal.root)
	}
	for _, root := range private {
		if root != "" && pathWithin(exportDir, filepath.Clean(root)) {
			return errors.New("native export: destination is inside vmmd's private state")
		}
	}
	parent := filepath.Dir(exportDir)
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf("native export: destination parent: %w", err)
	}
	if resolved != parent {
		return errors.New("native export: destination parent must not traverse links")
	}
	info, err := os.Lstat(exportDir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.Mkdir(exportDir, 0o755); err != nil {
			return fmt.Errorf("native export: create destination: %w", err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("native export: destination: %w", err)
	case !info.IsDir():
		return errors.New("native export: destination is not a real directory")
	}
	entries, err := os.ReadDir(exportDir)
	if err != nil {
		return fmt.Errorf("native export: destination: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("native export: destination is not empty")
	}
	return nil
}

// pathWithin reports whether path is root or below it.
func pathWithin(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}
