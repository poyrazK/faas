package imaged

// adr: 431. Component extraction and future native overlay handoffs share the
// same bounded scanner projection. This does not confer whole-runtime approval.

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/scanview"
)

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
