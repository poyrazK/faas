package neon

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.DeletionHoldProber = (*Provider)(nil)

// Deletion hold probe timings, as for the compute cap probe. Neon keeps
// refusing a released endpoint for a while ("The endpoint has been
// disabled", measured 2026-11-22: the first connection after release
// refused, later ones accepted, but the qualification's next probes, seconds
// later, were refused again on all three proxy addresses). The probe counts
// a release only once connections have been accepted for
// deletionHoldProbeSettle in a row, so later probes do not meet that window.
const (
	deletionHoldProbeSuspendWait = 3 * time.Minute
	deletionHoldProbeRefuseWait  = 30 * time.Second
	deletionHoldProbeResumeWait  = 2 * time.Minute
	deletionHoldProbeSettle      = time.Minute
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
	// Qualification starts with a disposable lifecycle resource. Freeze its
	// dataset once, then use it for both hold and cleanup even if the default
	// branch changes during the probe.
	observed, err := p.Inspect(ctx, providerResourceID)
	if err != nil || observed.DataResourceID == "" {
		if err == nil {
			err = managedpostgres.ErrUnavailable
		}
		return evidence, err
	}
	providerResourceID = observed.DataResourceID
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
	evidence.ResumedAfterRelease = awaitSteady(ctx, func(ctx context.Context) error { return probeQuery(ctx, dsn) },
		deletionHoldProbeSettle, deletionHoldProbeResumeWait+deletionHoldProbeSettle, computeCapProbePoll)
	return evidence, nil
}

// awaitSteady reports whether query has succeeded on every attempt for
// settle, a failure restarting the run, before wait runs out.
func awaitSteady(ctx context.Context, query func(context.Context) error, settle, wait, poll time.Duration) bool {
	deadline := time.Now().Add(wait)
	var since time.Time
	for {
		attempt, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := query(attempt)
		cancel()
		now := time.Now()
		switch {
		case err != nil:
			since = time.Time{}
		case since.IsZero():
			since = now
		}
		if !since.IsZero() && now.Sub(since) >= settle {
			return true
		}
		if now.After(deadline) || sleepContext(ctx, poll) != nil {
			return false
		}
	}
}
