package firecracker

import (
	"context"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// ResolveRestoreSpec reads the dataset's persisted major history. Reservation
// then freezes the selected major; retries do not depend on today's history.
func (p *Provider) ResolveRestoreSpec(ctx context.Context, source managedpostgres.RestoreSourceDefinition, point time.Time) (managedpostgres.Spec, error) {
	if point.IsZero() {
		return managedpostgres.Spec{}, managedpostgres.ErrInvalid
	}
	major, history, err := p.readRestoreMajorHistory(ctx, source)
	if err != nil {
		return managedpostgres.Spec{}, err
	}
	spec := source.Spec
	spec.PostgresMajor = major
	for _, span := range history {
		if point.Before(span.Until) {
			spec.PostgresMajor = span.PostgresMajor
			break
		}
	}
	if err := p.supports(spec); err != nil {
		return managedpostgres.Spec{}, err
	}
	return spec, nil
}

// RestoreMajorHistory returns the scheduler's validated major boundaries.
func (p *Provider) RestoreMajorHistory(ctx context.Context, source managedpostgres.RestoreSourceDefinition) ([]managedpostgres.RestoreMajorSpan, error) {
	major, history, err := p.readRestoreMajorHistory(ctx, source)
	if err != nil {
		return nil, err
	}
	if major != source.Spec.PostgresMajor {
		return nil, managedpostgres.ErrConflict
	}
	return history, nil
}

func (p *Provider) readRestoreMajorHistory(ctx context.Context, source managedpostgres.RestoreSourceDefinition) (int, []managedpostgres.RestoreMajorSpan, error) {
	if source.ProviderResourceID == "" || source.DataResourceID != source.ProviderResourceID || source.Spec.Validate() != nil {
		return 0, nil, managedpostgres.ErrInvalid
	}
	rt, err := p.scheduler.GetDatabase(ctx, &scheddpb.DatabaseRef{DatabaseId: source.DataResourceID})
	if err != nil {
		return 0, nil, providerError(err)
	}
	if rt.GetDatabaseId() != source.DataResourceID || rt.GetState() == "upgrading" || rt.GetState() == "deleting" {
		return 0, nil, managedpostgres.ErrConflict
	}
	if !rt.GetMajorHistoryComplete() && (rt.GetUpgrade() != nil || len(rt.GetMajorHistory()) != 0) {
		// N-1 reports the last upgrade, but not its historical boundaries.
		return 0, nil, managedpostgres.ErrUnavailable
	}
	history := make([]managedpostgres.RestoreMajorSpan, 0, len(rt.GetMajorHistory()))
	for _, span := range rt.GetMajorHistory() {
		if span.GetUntil() == nil || span.GetUntil().CheckValid() != nil || span.GetPostgresMajor() < 12 ||
			span.GetPostgresMajor() >= rt.GetPostgresMajor() {
			return 0, nil, managedpostgres.ErrUnavailable
		}
		major, until := int(span.GetPostgresMajor()), span.GetUntil().AsTime()
		if n := len(history); n > 0 && (major <= history[n-1].PostgresMajor || !until.After(history[n-1].Until)) {
			return 0, nil, managedpostgres.ErrUnavailable
		}
		history = append(history, managedpostgres.RestoreMajorSpan{PostgresMajor: major, Until: until})
	}
	return int(rt.GetPostgresMajor()), history, nil
}
