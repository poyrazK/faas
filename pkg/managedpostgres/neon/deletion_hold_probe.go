package neon

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.DeletionHoldProber = (*Provider)(nil)

// Deletion hold probe timings, as for the compute cap probe.
const (
	deletionHoldProbeSuspendWait = 3 * time.Minute
	deletionHoldProbeRefuseWait  = 30 * time.Second
	deletionHoldProbeResumeWait  = 2 * time.Minute
)

// ProbeDeletionHold runs only in operator qualification, on a disposable
// resource. Neon documents the endpoint's disabled flag only in its API
// schema, so the delete grace period's hold (ADR-20261029174950) is checked
// live: once held, the compute suspends and a new connection is refused;
// once released, the next connection wakes it. Infrastructure failures
// return an error; a behaviour that does not match leaves its evidence field
// false. The hold is always released before returning.
func (p *Provider) ProbeDeletionHold(ctx context.Context, providerResourceID string, material managedpostgres.CredentialMaterial) (managedpostgres.DeletionHoldEvidence, error) {
	var evidence managedpostgres.DeletionHoldEvidence
	dsn, err := probeDSN(material)
	if err != nil {
		return evidence, err
	}
	if err := p.HoldForDeletion(ctx, providerResourceID); err != nil {
		return evidence, err
	}
	released := false
	defer func() {
		if !released {
			_ = p.ReleaseDeletionHold(context.WithoutCancel(ctx), providerResourceID)
		}
	}()
	evidence.SuspendedWhileHeld = p.awaitComputeState(ctx, providerResourceID, managedpostgres.ComputeStateSuspended, deletionHoldProbeSuspendWait)
	refuseCtx, cancel := context.WithTimeout(ctx, deletionHoldProbeRefuseWait)
	evidence.RefusedWhileHeld = probeQuery(refuseCtx, dsn) != nil
	cancel()
	if err := p.ReleaseDeletionHold(ctx, providerResourceID); err != nil {
		return evidence, err
	}
	released = true
	evidence.ResumedAfterRelease = awaitQuery(ctx, dsn, deletionHoldProbeResumeWait)
	return evidence, nil
}
