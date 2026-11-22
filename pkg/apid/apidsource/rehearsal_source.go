package apidsource

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/storage"
)

// RemoveRehearsalSource removes the shared source object of a deterministic
// rehearsal build once the build is complete or its run has been retired.
func RemoveRehearsalSource(ctx context.Context, buildID string) error {
	id, err := uuid.Parse(buildID)
	if err != nil || id.String() != buildID || id.Version() != 5 {
		return errors.New("invalid rehearsal build identity")
	}
	backend, err := sourceBackendFromEnv(ctx)
	if err != nil || backend == nil {
		return err
	}
	return backend.Delete(ctx, "sources/"+buildID+".tar.gz")
}

// RehearsalSourceExists observes the same object identity used by deletion.
// A backend without an existence probe cannot certify archive cleanup.
func RehearsalSourceExists(ctx context.Context, buildID string) (bool, error) {
	id, err := uuid.Parse(buildID)
	if err != nil || id.String() != buildID || id.Version() != 5 {
		return false, errors.New("invalid rehearsal build identity")
	}
	backend, err := sourceBackendFromEnv(ctx)
	if err != nil || backend == nil {
		return false, err
	}
	exists, supported, err := storage.Exists(ctx, backend, "sources/"+buildID+".tar.gz")
	if err != nil {
		return false, err
	}
	if !supported {
		return false, errors.New("source backend cannot verify rehearsal cleanup")
	}
	return exists, nil
}
