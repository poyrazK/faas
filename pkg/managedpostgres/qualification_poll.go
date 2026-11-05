package managedpostgres

import (
	"context"
	"errors"
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
		} else if inspector, ok := provider.(ProvisionInspector); ok && restore == nil {
			observed, err = inspector.InspectProvision(ctx, resourceID)
		} else {
			observed, err = provider.Inspect(ctx, resourceID)
		}
		if err != nil {
			return observed, err
		}
		if observed.ProviderResourceID != resourceID {
			return observed, ErrUnavailable
		}
		if restore != nil && restore.Verification == nil && observed.RestoreVerification != nil {
			verification := *observed.RestoreVerification
			restore.Verification = &verification
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

// qualifyProviderUsage reads the usage of the hour the resource was created
// in: a fresh resource has none before it. With wait, it reads once that
// hour has closed and retries while the provider has not caught up; on
// timeout it returns the provider's last error, not the context's.
func qualifyProviderUsage(ctx context.Context, provider Provider, resourceID string, created time.Time, interval time.Duration, wait bool) (Usage, error) {
	window := UsageWindow{From: created.UTC().Truncate(time.Hour)}
	window.To = window.From.Add(time.Hour)
	if !wait {
		return provider.Usage(ctx, resourceID, window)
	}
	if remaining := time.Until(window.To); remaining > 0 {
		if err := qualificationPoll(ctx, remaining); err != nil {
			return Usage{}, err
		}
	}
	if interval <= 0 {
		interval = time.Minute
	}
	for {
		usage, err := provider.Usage(ctx, resourceID, window)
		if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrUnavailable) && !errors.Is(err, ErrUsageStale) {
			return usage, err
		}
		if qualificationPoll(ctx, interval) != nil {
			return usage, err
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
