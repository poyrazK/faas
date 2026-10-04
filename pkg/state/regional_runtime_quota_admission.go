package state

// adr: 489
import (
	"context"
	"errors"
)

var ErrRegionalRuntimeQuotaFrozen = errors.New("regional runtime quota admission is frozen")

type RegionalRuntimeQuotaAdmissionReader interface {
	RegionalRuntimeQuotaHead(context.Context, string) (RegionalRuntimeQuotaHead, error)
}

// CheckRegionalRuntimeQuotaAdmission consults durable local authority. An
// absent stream preserves independent regional apps. An accepted quota stream
// is explicitly blocked until allowance enforcement and activation are wired.
// Unknown storage state never falls back to the full regional plan.
func CheckRegionalRuntimeQuotaAdmission(ctx context.Context, reader RegionalRuntimeQuotaAdmissionReader, appID string) error {
	if reader == nil {
		return ErrRegionalRuntimeQuotaFrozen
	}
	id := regionalQuotaCanonicalID(appID)
	if id == "" {
		return ErrInvalidArgument
	}
	head, err := reader.RegionalRuntimeQuotaHead(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if head.Grant.Binding.RegionalAppID != id {
		return ErrConflict
	}
	// No active allowance is currently implemented. Even an impossible enabled
	// value must not authorize execution before durable capacity checks exist.
	return ErrRegionalRuntimeQuotaFrozen
}
