package fcvm

// adr: 641
import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func (j *nativeLaunchJournal) records(ctx context.Context) ([]nativeLaunchRecord, error) {
	records, err := j.ownerRecords(ctx, func(instance string) (nativeLaunchRecord, error) {
		return j.lockedRecord(ctx, instance)
	})
	if err != nil {
		return nil, err
	}
	if err := j.validateRecordResources(ctx, records); err != nil {
		return nil, err
	}
	return records, nil
}

// Enumeration is shared; ordinary recovery locks each owner. Fresh inventory
// instead reads live owners through their already retained observation locks.
func (j *nativeLaunchJournal) ownerRecords(ctx context.Context, readOwner func(string) (nativeLaunchRecord, error)) ([]nativeLaunchRecord, error) {
	if _, err := j.currentBootID(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(j.root, 0o700); err != nil {
		return nil, err
	}
	if err := checkNativeJournalPath(j.root, true); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(j.root)
	if err != nil {
		return nil, err
	}
	var records []nativeLaunchRecord
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.Name() == "retired" || entry.Name() == "helpers" || entry.Name() == "loop-mounts" || entry.Name() == "image-sources" || entry.Name() == "tun-binds" || entry.Name() == "qualifications" || entry.Name() == "executables" || strings.HasPrefix(entry.Name(), ".launch-") || strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}
		instance, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !validNativeInstanceName(instance) || !entry.Type().IsRegular() {
			return nil, errors.New("native journal: unexpected entry in private record root")
		}
		record, err := readOwner(instance)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, ctx.Err()
}

func (j *nativeLaunchJournal) lockedRecord(ctx context.Context, instance string) (nativeLaunchRecord, error) {
	lock, err := j.lock(ctx, instance)
	if err != nil {
		return nativeLaunchRecord{}, err
	}
	record, err := j.read(instance)
	return record, errors.Join(err, lock.Close())
}

func (j *nativeLaunchJournal) validateRecordResources(ctx context.Context, records []nativeLaunchRecord) error {
	helpers := nativeHostHelperJournal{owner: j}
	if _, err := helpers.allRecords(ctx, records); err != nil {
		return err
	}
	loops := nativeLoopMountJournal{owner: j}
	if _, err := loops.allRecords(ctx, records); err != nil {
		return err
	}
	images := nativeImageSourceJournal{owner: j}
	imageRecords, err := images.records()
	if err != nil {
		return err
	}
	if err := images.validateOwners(imageRecords, records); err != nil {
		return err
	}
	tun := nativeTunBindJournal{owner: j}
	tunRecords, err := tun.records()
	if err != nil {
		return err
	}
	if err := tun.validateOwners(tunRecords, records); err != nil {
		return err
	}
	if _, err := j.qualifications("").recoveryLeases(ctx, records); err != nil {
		return err
	}
	return ctx.Err()
}

// Complete resource acknowledgement is separate from process exit. The record
// stays available for idempotent stops and restart recovery after this write.
func (j *nativeLaunchJournal) confirmResourcesRemoved(ctx context.Context, expected nativeLaunchRecord) (err error) {
	lock, err := j.lock(ctx, expected.Lease.Instance)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	record, err := j.read(expected.Lease.Instance)
	if err != nil {
		return err
	}
	if !record.Revoked || !record.ExitConfirmed || record.Generation != expected.Generation || record.KernelBootID != expected.KernelBootID || record.PID != expected.PID || record.StartTime != expected.StartTime || record.Authorized != expected.Authorized || !sameNativeJournalLease(record.Lease, expected.Lease) || !sameNativeRegionalBinding(record, expected) {
		return errors.New("native journal: resource acknowledgement identity changed")
	}
	helpers := nativeHostHelperJournal{owner: j}
	if err := helpers.requireRetired(record); err != nil {
		return err
	}
	if err := helpers.requireDeviceNamespacesRemoved(ctx, record); err != nil {
		return err
	}
	loops := nativeLoopMountJournal{owner: j}
	if err := loops.requireRetired(record); err != nil {
		return err
	}
	images := nativeImageSourceJournal{owner: j, backend: j.imageSources}
	if err := images.require(ctx, record, true); err != nil {
		return err
	}
	tun := nativeTunBindJournal{owner: j, backend: j.tunBinds}
	if err := tun.require(record, true); err != nil {
		return err
	}
	record.ResourcesRemoved = true
	return j.write(record)
}

// A failed restore may cold-boot with the same still-owned network and slot,
// but only the daemon holding this exact retired generation can replace it.
// Completed prior generations remain immutable in the private archive.
func (j *nativeLaunchJournal) replace(ctx context.Context, lease Lease, expectedGeneration string, requireResourcesRemoved bool) (record nativeLaunchRecord, err error) {
	return j.replaceWithPlacement(ctx, lease, expectedGeneration, requireResourcesRemoved, nil)
}

func (j *nativeLaunchJournal) replaceWithPlacement(ctx context.Context, lease Lease, expectedGeneration string, requireResourcesRemoved bool, placement *runtimequota.Placement) (record nativeLaunchRecord, err error) {
	return j.replaceWithRegionalResources(ctx, lease, expectedGeneration, requireResourcesRemoved, placement, nil)
}

func (j *nativeLaunchJournal) replaceWithRegionalResources(ctx context.Context, lease Lease, expectedGeneration string, requireResourcesRemoved bool, placement *runtimequota.Placement, resources *state.RegionalRuntimeResources) (record nativeLaunchRecord, err error) {
	placement = cloneRuntimePlacement(placement)
	resources = cloneNativeRegionalResources(resources)
	if requireResourcesRemoved {
		if err := validateNativeRegionalResources(lease, placement, resources); err != nil {
			return record, err
		}
	} else if err := validateNativeRegionalResourceBinding(lease, placement, resources); err != nil {
		return record, err
	}
	qualificationLock, incoming, err := j.lockQualificationProducer(ctx, lease.Instance)
	if err != nil {
		return record, err
	}
	if qualificationLock != nil {
		defer func() { err = errors.Join(err, qualificationLock.Close()) }()
	}
	if incoming != nil {
		return record, errors.New("native qualification: original physical generation cannot be replaced")
	}
	if err := validateNativeJournalLease(lease); err != nil {
		return record, err
	}
	lock, err := j.lock(ctx, lease.Instance)
	if err != nil {
		return record, err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	old, err := j.read(lease.Instance)
	if err != nil {
		return record, err
	}
	if !old.Revoked || !old.ExitConfirmed || old.Generation != expectedGeneration || requireResourcesRemoved && !old.ResourcesRemoved || !requireResourcesRemoved && !sameNativeJournalLease(old.Lease, lease) {
		return record, errors.New("native journal: prior launch is not replaceable by this owner")
	}
	if !requireResourcesRemoved {
		if placement != nil && !sameOptionalRuntimePlacement(placement, old.RegionalRuntime) || old.RegionalRuntime != nil && !sameNativePhysicalLease(old.Lease, lease) {
			return record, errors.New("native journal: fallback cannot replace its regional binding or full lease")
		}
		placement = cloneRuntimePlacement(old.RegionalRuntime)
		if resources != nil && !sameNativeRegionalResources(resources, old.RegionalResources) {
			return record, errors.New("native journal: fallback cannot replace its original resources")
		}
		resources = cloneNativeRegionalResources(old.RegionalResources)
	} else if old.RegionalRuntime != nil && placement == nil {
		return record, errors.New("native journal: regional ownership cannot be downgraded to an unbound launch")
	} else if old.RegionalResources != nil && resources == nil {
		return record, errors.New("native journal: original resources cannot be downgraded to unknown")
	}
	if err := validateNativeRegionalPlacement(lease, placement); err != nil {
		return record, err
	}
	// Replacement authorizes a new physical producer. Refuse a changed CPU
	// policy rather than launching above the immutable original footprint.
	if err := validateNativeRegionalResources(lease, placement, resources); err != nil {
		return record, err
	}
	helpers := nativeHostHelperJournal{owner: j}
	if err := helpers.requireRetired(old); err != nil {
		return record, err
	}
	if err := helpers.requireDeviceNamespacesRemoved(ctx, old); err != nil {
		return record, err
	}
	loops := nativeLoopMountJournal{owner: j}
	if err := loops.requireRetired(old); err != nil {
		return record, err
	}
	images := nativeImageSourceJournal{owner: j, backend: j.imageSources}
	if err := images.require(ctx, old, true); err != nil {
		return record, err
	}
	tun := nativeTunBindJournal{owner: j, backend: j.tunBinds}
	if err := tun.require(old, true); err != nil {
		return record, err
	}
	archive := filepath.Join(j.root, "retired")
	if err := os.MkdirAll(archive, 0o700); err != nil {
		return record, err
	}
	if err := checkNativeJournalPath(archive, true); err != nil {
		return record, err
	}
	path := filepath.Join(archive, old.Generation+".json")
	if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
		if err := writeNativeLaunchRecord(path, old); err != nil {
			return record, fmt.Errorf("native journal: archive prior launch: %w", err)
		}
	} else if statErr != nil {
		return record, statErr
	} else {
		// A crash after archive fsync but before publishing the new current
		// record must be retryable. Never overwrite a different archived frame.
		archived, err := readNativeLaunchRecord(path, lease.Instance)
		if err != nil {
			return record, err
		}
		if archived.Generation != old.Generation || archived.KernelBootID != old.KernelBootID || archived.Authorized != old.Authorized || archived.PID != old.PID || archived.StartTime != old.StartTime || !archived.Revoked || !archived.ExitConfirmed || archived.ResourcesRemoved && !old.ResourcesRemoved || !sameNativePhysicalLease(archived.Lease, old.Lease) || !sameNativeRegionalBinding(archived, old) {
			return record, errors.New("native journal: prior generation archive differs from retired ownership")
		}
	}
	record = newNativeLaunchRecord(lease, uuid.NewString(), old.KernelBootID, placement, resources)
	return record, j.write(record)
}
