package imaged

// adr: 435. Component extraction and future native overlay handoffs share the
// same bounded scanner projection. This does not confer whole-runtime approval.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/scanview"
)

// errScanDirInvisible marks a scan directory the scanner would not catalog.
var errScanDirInvisible = errors.New("imaged: scan directory is invisible to the scanner")

// scannerSkippedDirs are the directories Grype's cataloger (syft's path
// skipper, syft/internal/fileresolver/path_skipper.go) ignores when they are
// tmpfs, which on a Linux host they are; proc, sysfs and devtmpfs mounts,
// skipped wherever they are, cannot hold a scan directory. A scan rooted in
// one catalogs no packages and reports no findings, so a TMPDIR under
// /dev/shm turned every scan into a false clean. A variable so tests can
// mark their own directory.
var scannerSkippedDirs = []string{"/run", "/dev", "/var/run", "/var/lock", "/sys", "/proc"}

// checkScannerVisible refuses a scan directory whose real path lies inside
// a directory the scanner skips, so the scan fails closed instead.
func checkScannerVisible(dir string) error {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("imaged: resolve scan directory: %w", err)
	}
	if skipped, ok := scannerSkippedRoot(real); ok {
		return fmt.Errorf("%w: %s is inside %s; point TMPDIR and the artifact storage root at a disk-backed directory", errScanDirInvisible, real, skipped)
	}
	return nil
}

// scannerSkippedRoot reports the skipped directory that contains real, with
// syft's containment rule (equal, or a prefix followed by a separator).
func scannerSkippedRoot(real string) (string, bool) {
	real = filepath.Clean(real)
	for _, dir := range scannerSkippedDirs {
		dir = filepath.Clean(dir)
		if real == dir || strings.HasPrefix(real, dir+string(filepath.Separator)) {
			return dir, true
		}
	}
	return "", false
}

func stageBoundedGrypeView(ctx context.Context, source string) (string, scanview.Tree, func() error, error) {
	view, err := scanview.Snapshot(ctx, source)
	if err != nil {
		return "", scanview.Tree{}, func() error { return nil }, err
	}
	target, err := os.MkdirTemp(filepath.Dir(source), "imaged-scan-view-")
	if err != nil {
		return "", scanview.Tree{}, func() error { return nil }, err
	}
	cleanup := func() error { return os.RemoveAll(target) }
	// Before the copy: a view the scanner cannot see is refused, and a
	// rootfs is never copied into a RAM-backed tmpfs only to be discarded.
	if err := checkScannerVisible(target); err != nil {
		return "", scanview.Tree{}, func() error { return nil }, errors.Join(err, cleanup())
	}
	actual, err := scanview.Copy(ctx, source, target, view)
	if err != nil {
		return "", scanview.Tree{}, func() error { return nil }, errors.Join(err, cleanup())
	}
	return target, actual, cleanup, nil
}

func checkGrypeView(ctx context.Context, dir string, expected scanview.Tree) error {
	actual, err := scanview.Snapshot(ctx, dir)
	if err != nil {
		return err
	}
	if actual != expected {
		return scanview.ErrChanged
	}
	return nil
}

func runBoundedGrypeView(ctx context.Context, bin, source string) (result *ScanResult, err error) {
	dir, view, cleanup, err := stageBoundedGrypeView(ctx, source)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			result, err = nil, errors.Join(err, cleanupErr)
		}
	}()
	result, err = runGrypeDirectory(ctx, bin, dir)
	if err != nil {
		return nil, errors.Join(err, ctx.Err())
	}
	if err := errors.Join(checkGrypeView(ctx, dir, view), ctx.Err()); err != nil {
		return nil, err
	}
	return result, nil
}
