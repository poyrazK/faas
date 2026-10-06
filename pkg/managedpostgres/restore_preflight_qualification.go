package managedpostgres

import (
	"context"
	"time"
)

// RestorePreflightEvidence proves source pinning and admission limits for the
// actual restore data probe. It does not certify the entire retained interval.
type RestorePreflightEvidence struct {
	SourceIdentityPinned bool `json:"source_identity_pinned"`
	LimitsObserved       bool `json:"limits_observed"`
	ValidPointAccepted   bool `json:"valid_point_accepted"`
	InvalidPointRejected bool `json:"invalid_point_rejected"`
}

func (e RestorePreflightEvidence) Validate() error {
	if !e.SourceIdentityPinned || !e.LimitsObserved || !e.ValidPointAccepted || !e.InvalidPointRejected {
		return ErrUnavailable
	}
	return nil
}

func qualifyRestorePreflight(ctx context.Context, provider Provider, source ObservedDatabase, point time.Time) (RestorePreflightEvidence, error) {
	var evidence RestorePreflightEvidence
	observer, ok := provider.(RestoreSourceObserver)
	if !ok || !validDataResourceID(source.DataResourceID) || source.ProviderResourceID == "" {
		return evidence, ErrUnsupported
	}
	observed, err := observer.ObserveRestoreSource(ctx, RestoreSourceDefinition{Spec: source.Spec,
		ProviderResourceID: source.ProviderResourceID, DataResourceID: source.DataResourceID})
	if err != nil {
		return evidence, err
	}
	status := recoveryStatusFromObservation(Database{Spec: source.Spec, ProviderResourceID: source.ProviderResourceID, DataResourceID: source.DataResourceID}, observed, time.Now().UTC())
	if err := status.admits(point); err != nil {
		return evidence, err
	}
	evidence.SourceIdentityPinned, evidence.LimitsObserved, evidence.ValidPointAccepted = true, true, true
	evidence.InvalidPointRejected = status.admits(status.EarliestPossibleTime.Add(-time.Nanosecond)) != nil
	return evidence, evidence.Validate()
}
