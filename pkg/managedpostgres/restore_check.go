package managedpostgres

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// CheckRestore checks the authorized source's archive. It does not reserve
// a name, a plan slot or a target VM.
func (s *Service) CheckRestore(ctx context.Context, accountID, id string, point time.Time) (api.ManagedPostgresRestoreCheck, error) {
	source, err := customerDatabase(ctx, s.store, accountID, id)
	if err != nil {
		return api.ManagedPostgresRestoreCheck{}, err
	}
	point = point.UTC().Truncate(time.Microsecond)
	earliest, latest, ok := source.RestoreRange(s.now())
	if !ok || point.Before(earliest) || !point.Before(latest) {
		return api.ManagedPostgresRestoreCheck{}, fmt.Errorf("%w: point in time is outside the source's restore window", ErrInvalid)
	}
	backend, err := s.registry.Resolve(source.BackendID, source.BackendFingerprint)
	if err != nil {
		return api.ManagedPostgresRestoreCheck{}, err
	}
	checker, ok := backend.Provider.(interface {
		CheckRestoreTarget(context.Context, string, time.Time) (api.ManagedPostgresRestoreCheck, error)
	})
	if !ok || backend.Driver != "firecracker" {
		return api.ManagedPostgresRestoreCheck{}, ErrUnsupported
	}
	result, err := checker.CheckRestoreTarget(ctx, source.ProviderResourceID, point)
	result.PointInTime = point
	return result, err
}
