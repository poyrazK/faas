package apidsource

import (
	"context"
	"errors"

	"github.com/google/uuid"
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
