//go:build !linux

package fcvm

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/storage"
)

type unsupportedNativeSnapshotPublicationJournal struct{}

func newNativeSnapshotPublicationJournal(root, _, _ string) nativeSnapshotPublicationJournal {
	if root == "" {
		return nil
	}
	return unsupportedNativeSnapshotPublicationJournal{}
}

func (unsupportedNativeSnapshotPublicationJournal) Acquire(context.Context) error {
	return errors.New("native snapshot publication: persistent ownership requires Linux")
}

func (unsupportedNativeSnapshotPublicationJournal) Check() error {
	return errors.New("native snapshot publication: persistent ownership requires Linux")
}

func (unsupportedNativeSnapshotPublicationJournal) Begin(context.Context, nativeSnapshotPublicationIntent) (nativeSnapshotPublicationIntent, error) {
	return nativeSnapshotPublicationIntent{}, errors.New("native snapshot publication: persistent ownership requires Linux")
}

func (unsupportedNativeSnapshotPublicationJournal) Require(context.Context, nativeSnapshotPublicationIntent) error {
	return errors.New("native snapshot publication: persistent ownership requires Linux")
}

func (unsupportedNativeSnapshotPublicationJournal) RecoverPendingRetirements(context.Context, storage.StorageBackend) error {
	return errors.New("native snapshot publication: retirement recovery requires Linux")
}
