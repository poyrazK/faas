//go:build !linux

package fcvm

import (
	"context"
	"errors"
)

func newNativeSnapshotMemoryBackend() nativeSnapshotMemoryBackend { return nil }

func (j *nativeHostHelperJournal) removeSnapshotMemoryCgroup(_ context.Context, owner nativeLaunchRecord) (bool, error) {
	record, err := j.snapshotMemoryRecord(owner)
	if err != nil {
		return true, err
	}
	if record == nil || record.SnapshotOutput.Memory == nil {
		return false, nil
	}
	return true, errors.New("native snapshot memory: original Linux cgroup disposal unavailable")
}
