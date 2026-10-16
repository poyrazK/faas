package fcvm

// adr: 595 Resume acknowledgments must belong to the original paused load.

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// RuntimeSnapshotResumeObservation contains native facts, not a publishable
// receipt until checked against the exact promotion. The load hash continues
// to describe the original paused command. Advertised capability stays gated.
type RuntimeSnapshotResumeObservation struct {
	Request             runtimeadmission.Promotion
	ArtifactConsumption runtimeadmission.ArtifactConsumption
	SnapshotConsumption runtimeadmission.SnapshotConsumption
	ResumeEvidence      runtimeadmission.SnapshotResumeEvidence
}

func (o RuntimeSnapshotResumeObservation) Check(p runtimeadmission.Promotion, now time.Time) error {
	if !o.Request.Equal(p) {
		return runtimeadmission.ErrStale
	}
	return o.ResumeEvidence.Check(p, o.ArtifactConsumption, o.SnapshotConsumption, now)
}

func (o RuntimeSnapshotResumeObservation) Clone() RuntimeSnapshotResumeObservation {
	o.Request = o.Request.Clone()
	o.ArtifactConsumption = o.ArtifactConsumption.Clone()
	return o
}

func (o RuntimeSnapshotResumeObservation) receipt(p runtimeadmission.Promotion, now time.Time) (runtimeadmission.Receipt, error) {
	if err := o.Check(p, now); err != nil {
		return runtimeadmission.Receipt{}, err
	}
	r := p.Parent.Clone()
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, o.ResumeEvidence.CompletedAtUnixNano
	r.ArtifactConsumption, r.SnapshotConsumption, r.SnapshotResumeEvidence = o.ArtifactConsumption.Clone(), o.SnapshotConsumption, o.ResumeEvidence
	if err := p.CheckReceipt(r, now); err != nil {
		return runtimeadmission.Receipt{}, err
	}
	return r, nil
}

// PromoteSnapshotVerified resumes a retained paused load at most once. It
// reobserves the same process, descriptors and private mappings around the
// actual resume and hook acknowledgments before Manager/RPC publication.
func (v *JailerVMM) PromoteSnapshotVerified(ctx context.Context, lease Lease, p runtimeadmission.Promotion) (result RuntimeSnapshotResumeObservation, err error) {
	p = p.Clone()
	if err := p.CheckSnapshotResumeRequest(time.Now()); err != nil {
		return result, err
	}
	parentHash, err := runtimeadmission.HashSnapshotResumeParent(p.Parent)
	if err != nil {
		return result, err
	}
	if lease.IsBuilder || lease.Instance != p.Binding.InstanceID || int32(lease.UID) != p.Parent.LeaseUID || lease.HostIP.String() != p.Parent.HostIP || lease.Netns != p.Parent.Netns {
		return result, runtimeadmission.ErrStale
	}
	ctx, cancel := context.WithDeadline(ctx, time.Unix(0, p.Binding.ExpiresAtUnixNano))
	defer cancel()
	ctx, flight, err := v.beginSnapshotSourceFlight(ctx, lease)
	if err != nil {
		return result, err
	}
	attempted := false
	defer func() {
		// Finish before Kill, which cancels and joins owned flights.
		flight.finish()
		if err != nil && attempted {
			result = RuntimeSnapshotResumeObservation{}
			cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
			defer cleanupCancel()
			err = errors.Join(err, v.Kill(cleanupCtx, lease))
		}
	}()
	if err := checkSnapshotResumeOwner(flight.handoff, p); err != nil {
		return result, err
	}
	historical := time.Unix(0, p.Parent.CompletedAtUnixNano)
	if _, _, err := v.observeSnapshotResumeConsumption(ctx, lease, p, historical); err != nil {
		return result, err
	}
	if err := claimSnapshotResume(ctx, flight.handoff, p); err != nil {
		return result, err
	}
	attempted = true
	command, err := v.resumeVMObserved(ctx, lease)
	if err != nil {
		return result, err
	}
	hook, err := v.triggerResumeHookObserved(ctx, lease, time.Now().UnixNano())
	if err != nil {
		return result, err
	}
	drives, snapshot, err := v.observeSnapshotResumeConsumption(ctx, lease, p, historical)
	if err != nil {
		return result, err
	}
	completed := time.Now()
	if err := errors.Join(p.CheckSnapshotResumeRequest(completed), ctx.Err()); err != nil {
		return result, err
	}
	if command.Version != 1 || hook.Version != 1 {
		return result, runtimeadmission.ErrStale
	}
	result = RuntimeSnapshotResumeObservation{Request: p.Clone(), ArtifactConsumption: drives, SnapshotConsumption: snapshot,
		ResumeEvidence: runtimeadmission.SnapshotResumeEvidence{Version: runtimeadmission.SnapshotResumeEvidenceVersion, Binding: p.Binding, ParentReceiptHash: parentHash,
			ParentBinding: p.Parent.Binding, ParentCompletedAtUnixNano: p.Parent.CompletedAtUnixNano,
			ResumeCommandHash: command.CommandHash, ResumeHookPayloadHash: hook.PayloadHash,
			CommandCompletedAtUnixNano: command.CompletedAtUnixNano, HostTimeUnixNano: hook.HostTimeUnixNano,
			HookCompletedAtUnixNano: hook.CompletedAtUnixNano, CompletedAtUnixNano: completed.UnixNano()}}
	if err := result.Check(p, completed); err != nil {
		return result, err
	}
	flight.handoff.mu.Lock()
	plan := flight.handoff.restoreLoad
	if flight.handoff.closed || plan == nil || plan.request.Binding != p.Parent.Binding || !plan.resumeAttempted || ctx.Err() != nil {
		flight.handoff.mu.Unlock()
		return result, runtimeadmission.ErrStale
	}
	plan.resumeEvidence = result.ResumeEvidence
	flight.handoff.mu.Unlock()
	return result, nil
}

func checkSnapshotResumeOwner(handoff *runtimeDriveHandoff, p runtimeadmission.Promotion) error {
	handoff.mu.Lock()
	defer handoff.mu.Unlock()
	plan := handoff.restoreLoad
	if handoff.closed || plan == nil || plan.inputs.owner != handoff || !plan.accepted || !plan.keepPaused || plan.request.Binding != p.Parent.Binding {
		return runtimeadmission.ErrStale
	}
	if plan.resumeAttempted {
		return runtimeadmission.ErrReplay
	}
	return nil
}

func claimSnapshotResume(ctx context.Context, handoff *runtimeDriveHandoff, p runtimeadmission.Promotion) error {
	if err := errors.Join(checkSnapshotResumeOwner(handoff, p), p.CheckSnapshotResumeRequest(time.Now()), ctx.Err()); err != nil {
		return err
	}
	handoff.mu.Lock()
	defer handoff.mu.Unlock()
	if handoff.closed || handoff.restoreLoad == nil || handoff.restoreLoad.resumeAttempted {
		return runtimeadmission.ErrReplay
	}
	handoff.restoreLoad.resumeAttempted = true
	return nil
}

func (v *JailerVMM) observeSnapshotResumeConsumption(ctx context.Context, lease Lease, p runtimeadmission.Promotion, historical time.Time) (runtimeadmission.ArtifactConsumption, runtimeadmission.SnapshotConsumption, error) {
	drives, snapshot, err := v.observedRuntimeSnapshotConsumptionAt(ctx, lease, historical)
	if err != nil {
		return runtimeadmission.ArtifactConsumption{}, runtimeadmission.SnapshotConsumption{}, err
	}
	if !drives.Equal(p.Parent.ArtifactConsumption) || snapshot != p.Parent.SnapshotConsumption {
		return runtimeadmission.ArtifactConsumption{}, runtimeadmission.SnapshotConsumption{}, runtimeadmission.ErrStale
	}
	if err := errors.Join(p.CheckSnapshotResumeRequest(time.Now()), ctx.Err()); err != nil {
		return runtimeadmission.ArtifactConsumption{}, runtimeadmission.SnapshotConsumption{}, err
	}
	return drives, snapshot, nil
}
