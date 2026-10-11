//go:build !linux

package fcvm

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/jailsetup"
)

func (j *nativeHostHelperJournal) prepareSnapshotOutputInputs(context.Context, nativeLaunchRecord) (*nativeSnapshotOutputInputs, error) {
	return nil, errors.New("native snapshot handoff: requires qualified Linux namespace descriptors")
}
func nativeSnapshotOutputInputsRemoved(context.Context, jailsetup.SnapshotOutputScope) error {
	return errors.New("native snapshot handoff: Linux input retirement proof unavailable")
}
