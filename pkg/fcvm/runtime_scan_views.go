package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/overlaymetadata"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/runtimescan"
	"github.com/onebox-faas/faas/pkg/scanview"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
)

func composeRuntimeScanRoots(ctx context.Context, request runtimescan.Request, mounts map[string]string, overlay *vmmdmount.MountLease) (map[string]string, error) {
	roots := map[string]string{}
	for _, source := range request.Sources {
		if source.Kind == "base-image" {
			continue
		}
		root := mounts[source.Role()]
		if source.Kind == "full-rootfs" {
			if err := checkRuntimeScanFullRootfsMarker(root, true); err != nil {
				return nil, err
			}
		} else {
			if err := checkRuntimeScanFullRootfsMarker(root, false); err != nil {
				return nil, err
			}
			root = filepath.Join(root, "upper")
			info, err := os.Lstat(root)
			if err != nil || !info.IsDir() {
				return nil, errors.Join(runtimeadmission.ErrInvalid, err)
			}
		}
		if source.UsesAppOverlay() {
			lowers, err := overlaymetadata.ReadOnlyLowerDirectories(root, mounts["base"])
			if err != nil {
				return nil, err
			}
			root, err = vmmdmount.MountRuntimeScanOverlay(ctx, lowers)
			if err != nil {
				return nil, err
			}
			if err := overlay.Attach(root, vmmdmount.MountKindRuntimeScanOverlay, "", ""); err != nil {
				return nil, errors.Join(err, cleanupUnregisteredParent(ctx, root, ""))
			}
		}
		roots[source.WorkloadName] = root
	}
	return roots, nil
}

func checkRuntimeScanFullRootfsMarker(dir string, expected bool) error {
	present, err := scanview.FullRootfsMarkerPresent(context.Background(), dir)
	if err != nil {
		return err
	}
	if present != expected {
		return runtimeadmission.ErrInvalid
	}
	return nil
}

type runtimeScanProjectionTarget interface {
	CreateView(string) (*os.Root, error)
}

func projectRuntimeScanRoots(ctx context.Context, request runtimescan.Request, roots map[string]string, target runtimeScanProjectionTarget) (runtimescan.Receipt, error) {
	hash, err := runtimeadmission.HashArtifactSources(request.Sources)
	if err != nil {
		return runtimescan.Receipt{}, err
	}
	receipt := runtimescan.Receipt{Version: runtimescan.Version, InputHash: request.InputHash, SourcesHash: hash, TargetDir: request.TargetDir}
	var bytes int64
	var entries int
	for _, source := range request.Sources {
		if source.Kind == "base-image" {
			continue
		}
		tree, err := scanview.Snapshot(ctx, roots[source.WorkloadName])
		if err != nil {
			return runtimescan.Receipt{}, err
		}
		if err := checkRuntimeScanBudget(bytes, entries, tree.Bytes, tree.Entries); err != nil {
			return runtimescan.Receipt{}, err
		}
		dir, err := target.CreateView(runtimescan.ViewDirectory(source.WorkloadName))
		if err != nil {
			return runtimescan.Receipt{}, err
		}
		actual, copyErr := scanview.CopyToRoot(ctx, roots[source.WorkloadName], dir, tree)
		if err := errors.Join(copyErr, dir.Close()); err != nil {
			return runtimescan.Receipt{}, err
		}
		receipt.Views = append(receipt.Views, runtimescan.View{WorkloadName: source.WorkloadName, SourceTree: tree, ProjectionTree: actual})
		bytes += tree.Bytes
		entries += tree.Entries
	}
	return receipt, nil
}
