package managedpostgres

import (
	"context"
	"time"
)

func qualificationPoll(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Second
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func qualifyProviderReady(ctx context.Context, provider Provider, resourceID string, interval time.Duration) (ObservedDatabase, error) {
	return qualifyProviderReadyWithRestore(ctx, provider, resourceID, interval, nil)
}

func qualifyProviderReadyWithRestore(ctx context.Context, provider Provider, resourceID string, interval time.Duration, restore *RestoreRequest) (ObservedDatabase, error) {
	for {
		var observed ObservedDatabase
		var err error
		if inspector, ok := provider.(RestoreInspector); ok && restore != nil {
			observed, err = inspector.InspectRestore(ctx, resourceID, *restore)
		} else {
			observed, err = provider.Inspect(ctx, resourceID)
		}
		if err != nil {
			return observed, err
		}
		if observed.ProviderResourceID != resourceID {
			return observed, ErrUnavailable
		}
		switch observed.Status {
		case ProviderStatusReady:
			if restore != nil {
				if err := validateCloneRestoreObservation(Database{RestoreSourceResourceID: restore.SourceResourceID, RestorePointInTime: restore.PointInTime}, observed); err != nil {
					return observed, err
				}
			}
			return observed, nil
		case ProviderStatusPending:
		default:
			return observed, ErrUnavailable
		}
		if err := qualificationPoll(ctx, interval); err != nil {
			return observed, err
		}
	}
}

func qualifyProviderDelete(ctx context.Context, provider Provider, interval time.Duration, request DeleteRequest) (DeleteResult, error) {
	for {
		result, err := provider.Delete(ctx, request)
		if err != nil || result.Done {
			return result, err
		}
		if err := qualificationPoll(ctx, interval); err != nil {
			return result, err
		}
	}
}
