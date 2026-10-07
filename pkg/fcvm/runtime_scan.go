package fcvm

import (
	"context"
	"errors"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/runtimescan"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
)

// MaterializeRuntimeScan keeps each drive separate and protected through a
// real read-only composition, bounded projection and all native cleanup.
func (m *Manager) MaterializeRuntimeScan(ctx context.Context, request runtimescan.Request) (receipt runtimescan.Receipt, err error) {
	request.Sources = slices.Clone(request.Sources)
	if err := request.Validate(); err != nil {
		return receipt, err
	}
	if m.storage == nil || m.parentMounts == nil {
		return receipt, runtimeadmission.ErrUnavailable
	}
	leases, reserveErr := reserveRuntimeScanMounts(ctx, m.parentMounts, request)
	var target *vmmdmount.RuntimeScanTarget
	defer func() { finishRuntimeScan(ctx, leases, target, &receipt, &err) }()
	if reserveErr != nil {
		return receipt, reserveErr
	}
	target, err = vmmdmount.OpenRuntimeScanTarget(request.TargetDir)
	if err != nil {
		return receipt, err
	}
	mounts := map[string]string{}
	for i, source := range request.Sources {
		mounts[source.Role()], err = m.mountRuntimeScanSource(ctx, source, leases[i])
		if err != nil {
			return receipt, err
		}
	}
	roots, err := composeRuntimeScanRoots(ctx, request, mounts, leases[len(leases)-1])
	if err != nil {
		return receipt, err
	}
	receipt, err = projectRuntimeScanRoots(ctx, request, roots, target)
	if err == nil {
		err = receipt.Check(request)
	}
	return receipt, err
}

func reserveRuntimeScanMounts(ctx context.Context, registry *vmmdmount.Registry, request runtimescan.Request) ([]*vmmdmount.MountLease, error) {
	count := len(request.Sources)
	for _, source := range request.Sources {
		if source.UsesAppOverlay() {
			count++
		}
	}
	leases := make([]*vmmdmount.MountLease, 0, count)
	for range count {
		lease, err := registry.ReserveMount(ctx)
		if err != nil {
			return leases, err
		}
		leases = append(leases, lease)
	}
	return leases, nil
}

func finishRuntimeScan(ctx context.Context, leases []*vmmdmount.MountLease, target *vmmdmount.RuntimeScanTarget, receipt *runtimescan.Receipt, result *error) {
	for i := len(leases) - 1; i >= 0; i-- {
		releaseParentMount(ctx, leases[i], result)
	}
	if target != nil {
		*result = errors.Join(*result, target.Verify(), target.Close())
	}
	*result = errors.Join(*result, ctx.Err())
	if *result != nil {
		*receipt = runtimescan.Receipt{}
	}
}

func checkRuntimeScanBudget(bytes int64, entries int, viewBytes int64, viewEntries int) error {
	if viewBytes > api.ApplicationStandardRuntimeScanMaxBytes-bytes || viewEntries > api.ApplicationStandardRuntimeScanMaxEntries-entries {
		return runtimeadmission.ErrInvalid
	}
	return nil
}
