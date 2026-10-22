package fcvm

import (
	"context"
	"errors"
	"fmt"
)

// NativeLaunchIdentity names one recorded process incarnation on one kernel
// boot. The serving vmmd must separately authenticate its node identity.
type NativeLaunchIdentity struct {
	InstanceID, Generation, KernelBootID string
	PID                                  int
	StartTime                            uint64
}

// NativeLaunchEvidence is a journal snapshot, not a closure receipt. It does
// not authenticate app/deployment ownership or close delayed producers. No
// field authorizes cleanup, replay, evidence deletion or public admission.
type NativeLaunchEvidence struct {
	Identity                                             NativeLaunchIdentity
	Authorized, Revoked, ExitConfirmed, ResourcesRemoved bool
}

// ObserveNativeLaunch returns current durable evidence under the journal lock.
// Missing, damaged or prior-boot records return errors, never absence proofs.
func (v *JailerVMM) ObserveNativeLaunch(ctx context.Context, instanceID string) (NativeLaunchEvidence, error) {
	if v == nil || v.nativeRecovery == nil || v.nativeRecovery.journal == nil {
		return NativeLaunchEvidence{}, errors.New("native launch evidence: recovery journal unavailable")
	}
	observed, err := v.nativeRecovery.journal.observeLaunch(ctx, instanceID)
	if err != nil {
		return NativeLaunchEvidence{}, fmt.Errorf("native launch evidence: observe: %w", err)
	}
	return observed, nil
}

// ObserveNativeLaunchGeneration rejects evidence for a different generation or
// process incarnation. Results remain observations; an action must revalidate
// ownership under its own native producer lock.
func (v *JailerVMM) ObserveNativeLaunchGeneration(ctx context.Context, expected NativeLaunchIdentity) (NativeLaunchEvidence, error) {
	if !validPinnedNativeLaunchIdentity(expected) {
		return NativeLaunchEvidence{}, errors.New("native launch evidence: invalid pinned identity")
	}
	observed, err := v.ObserveNativeLaunch(ctx, expected.InstanceID)
	if err != nil {
		return NativeLaunchEvidence{}, err
	}
	if observed.Identity != expected {
		return NativeLaunchEvidence{}, errors.New("native launch evidence: pinned incarnation changed")
	}
	return observed, nil
}

func (j *nativeLaunchJournal) observeLaunch(ctx context.Context, instance string) (out NativeLaunchEvidence, err error) {
	lock, err := j.lock(ctx, instance)
	if err != nil {
		return out, err
	}
	defer func() {
		err = errors.Join(err, lock.Close())
		if err != nil {
			out = NativeLaunchEvidence{}
		}
	}()
	record, err := j.read(instance)
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	out = nativeLaunchEvidenceFromRecord(record)
	return out, nil
}

func nativeLaunchEvidenceFromRecord(record nativeLaunchRecord) NativeLaunchEvidence {
	return NativeLaunchEvidence{Identity: NativeLaunchIdentity{InstanceID: record.Lease.Instance, Generation: record.Generation, KernelBootID: record.KernelBootID, PID: record.PID, StartTime: record.StartTime}, Authorized: record.Authorized, Revoked: record.Revoked, ExitConfirmed: record.ExitConfirmed, ResourcesRemoved: record.ResourcesRemoved}
}

func validPinnedNativeLaunchIdentity(expected NativeLaunchIdentity) bool {
	return validNativeInstanceName(expected.InstanceID) && canonicalNativeHelperID(expected.Generation) && canonicalNativeHelperID(expected.KernelBootID) && expected.PID >= 0 && expected.PID != 1 && (expected.PID == 0) == (expected.StartTime == 0)
}
