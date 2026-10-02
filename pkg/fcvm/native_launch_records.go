package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

func (j *nativeLaunchJournal) records(ctx context.Context) ([]nativeLaunchRecord, error) {
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
		if entry.Name() == "retired" || strings.HasPrefix(entry.Name(), ".launch-") || strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}
		instance, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !validNativeInstanceName(instance) || !entry.Type().IsRegular() {
			return nil, errors.New("native journal: unexpected entry in private record root")
		}
		lock, err := j.lock(ctx, instance)
		if err != nil {
			return nil, err
		}
		record, err := j.read(instance)
		err = errors.Join(err, lock.Close())
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, ctx.Err()
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
	if !record.Revoked || !record.ExitConfirmed || record.Generation != expected.Generation || record.KernelBootID != expected.KernelBootID || record.PID != expected.PID || record.StartTime != expected.StartTime || record.Authorized != expected.Authorized || !sameNativeJournalLease(record.Lease, expected.Lease) {
		return errors.New("native journal: resource acknowledgement identity changed")
	}
	record.ResourcesRemoved = true
	return j.write(record)
}

// A failed restore may cold-boot with the same still-owned network and slot,
// but only the daemon holding this exact retired generation can replace it.
// Completed prior generations remain immutable in the private archive.
func (j *nativeLaunchJournal) replace(ctx context.Context, lease Lease, expectedGeneration string, requireResourcesRemoved bool) (record nativeLaunchRecord, err error) {
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
		if archived.Generation != old.Generation || archived.KernelBootID != old.KernelBootID || archived.Authorized != old.Authorized || archived.PID != old.PID || archived.StartTime != old.StartTime || !archived.Revoked || !archived.ExitConfirmed || archived.ResourcesRemoved && !old.ResourcesRemoved || archived.Lease != old.Lease {
			return record, errors.New("native journal: prior generation archive differs from retired ownership")
		}
	}
	record = nativeLaunchRecord{Version: 1, Generation: uuid.NewString(), KernelBootID: old.KernelBootID, Lease: lease}
	return record, j.write(record)
}
