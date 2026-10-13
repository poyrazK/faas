package fcvm

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

// ObserveArchivedNativeLaunchGeneration reads only the retained exact original
// incarnation, even if the current instance record is newer or absent. Archive
// flags are observations, not authority to retire shared resources or producers.
func (v *JailerVMM) ObserveArchivedNativeLaunchGeneration(ctx context.Context, expected NativeLaunchIdentity) (NativeLaunchEvidence, error) {
	if !validPinnedNativeLaunchIdentity(expected) {
		return NativeLaunchEvidence{}, errors.New("native archive evidence: invalid pinned identity")
	}
	if v == nil || v.nativeRecovery == nil || v.nativeRecovery.journal == nil {
		return NativeLaunchEvidence{}, errors.New("native archive evidence: recovery journal unavailable")
	}
	observed, err := v.nativeRecovery.journal.observeArchivedLaunch(ctx, expected)
	if err != nil {
		return NativeLaunchEvidence{}, fmt.Errorf("native archive evidence: observe: %w", err)
	}
	return observed, nil
}

func (j *nativeLaunchJournal) observeArchivedLaunch(ctx context.Context, expected NativeLaunchIdentity) (out NativeLaunchEvidence, err error) {
	lock, err := j.lock(ctx, expected.InstanceID)
	if err != nil {
		return out, err
	}
	defer func() {
		err = errors.Join(err, lock.Close())
		if err != nil {
			out = NativeLaunchEvidence{}
		}
	}()
	bootID, err := j.currentBootID()
	if err != nil {
		return out, err
	}
	if bootID != expected.KernelBootID {
		return out, errors.New("native archive evidence: pinned kernel boot changed")
	}
	archive := filepath.Join(j.root, "retired")
	if err = checkNativeJournalPath(archive, true); err != nil {
		return out, err
	}
	record, err := readNativeLaunchRecord(filepath.Join(archive, expected.Generation+".json"), expected.InstanceID)
	if err != nil {
		return out, err
	}
	observed := nativeLaunchEvidenceFromRecord(record)
	if observed.Identity != expected || !record.Revoked || !record.ExitConfirmed {
		return out, errors.New("native archive evidence: original retired incarnation differs")
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	return observed, nil
}
