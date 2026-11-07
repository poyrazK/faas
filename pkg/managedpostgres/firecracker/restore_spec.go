package firecracker

import (
	"context"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.RestoreSpecResolver = (*Provider)(nil)

// ResolveRestoreSpec reads the dataset's persisted major history. Reservation
// then freezes the selected major; retries do not depend on today's history.
func (p *Provider) ResolveRestoreSpec(ctx context.Context, source managedpostgres.RestoreSourceDefinition, point time.Time) (managedpostgres.Spec, error) {
	if point.IsZero() || source.ProviderResourceID == "" || source.DataResourceID != source.ProviderResourceID || source.Spec.Validate() != nil {
		return managedpostgres.Spec{}, managedpostgres.ErrInvalid
	}
	rt, err := p.scheduler.GetDatabase(ctx, &scheddpb.DatabaseRef{DatabaseId: source.DataResourceID})
	if err != nil {
		return managedpostgres.Spec{}, providerError(err)
	}
	if rt.GetDatabaseId() != source.DataResourceID || rt.GetState() == "upgrading" || rt.GetState() == "deleting" {
		return managedpostgres.Spec{}, managedpostgres.ErrConflict
	}
	if !rt.GetMajorHistoryComplete() && (rt.GetUpgrade() != nil || len(rt.GetMajorHistory()) != 0) {
		// N-1 reports the last upgrade, but not its historical boundaries.
		// Wait for a current scheduler rather than reserve a guessed major.
		return managedpostgres.Spec{}, managedpostgres.ErrUnavailable
	}
	major := int(rt.GetPostgresMajor())
	previousMajor := 0
	var previousUntil time.Time
	selected := false
	for _, span := range rt.GetMajorHistory() {
		if span.GetUntil() == nil || span.GetUntil().CheckValid() != nil || int(span.GetPostgresMajor()) <= previousMajor ||
			span.GetPostgresMajor() >= rt.GetPostgresMajor() || !previousUntil.IsZero() && !span.GetUntil().AsTime().After(previousUntil) {
			return managedpostgres.Spec{}, managedpostgres.ErrUnavailable
		}
		until := span.GetUntil().AsTime()
		if !selected && point.Before(until) {
			major, selected = int(span.GetPostgresMajor()), true
		}
		previousMajor, previousUntil = int(span.GetPostgresMajor()), until
	}
	spec := source.Spec
	spec.PostgresMajor = major
	if err := p.supports(spec); err != nil {
		return managedpostgres.Spec{}, err
	}
	return spec, nil
}
