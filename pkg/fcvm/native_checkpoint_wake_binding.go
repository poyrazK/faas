// adr: 590
package fcvm

import (
	"context"
	"errors"
	"os"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func validateCheckpointWakeLease(frame state.ProjectEnvironmentCloneWakeDispatch, lease Lease) error {
	if err := validateNativeJournalLease(lease); err != nil {
		return err
	}
	if lease.Instance != frame.InstanceID || !lease.Plan.Valid() || lease.IsBuilder || lease.Networkless {
		return errors.New("native checkpoint wake: physical lease differs from original workload ownership")
	}
	return nil
}

// Publication order is incoming binding, then prepared physical journal. The
// shared incoming lock prevents revoke, generic launch and qualification claims
// from interleaving. Lost outcomes never authorize a second physical generation.
func (j *nativeCheckpointWakeJournal) preparePhysical(ctx context.Context, frame state.ProjectEnvironmentCloneWakeDispatch, lease Lease) (out nativeCheckpointWakeRecord, err error) {
	return j.preparePhysicalDigest(ctx, frame, lease, "")
}

func (j *nativeCheckpointWakeJournal) preparePhysicalDigest(ctx context.Context, frame state.ProjectEnvironmentCloneWakeDispatch, lease Lease, digest string) (out nativeCheckpointWakeRecord, err error) {
	defer func() {
		if err != nil {
			out = nativeCheckpointWakeRecord{}
		}
	}()
	if err = validateCheckpointWakeFrame(frame, j.nodeID); err != nil {
		return out, err
	}
	if err = validateCheckpointWakeLease(frame, lease); err != nil {
		return out, err
	}
	frame.DispatchedAt = frame.DispatchedAt.UTC().Round(0)
	q := j.owner.qualifications(j.nodeID)
	lock, err := q.lock(ctx, frame.InstanceID)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	out, err = j.read(frame.InstanceID)
	if err != nil {
		return out, err
	}
	if out.Dispatch != frame || !out.Claimed || out.Revoked || out.NativeGeneration != "" {
		return out, errors.New("native checkpoint wake: original request changed, revoked or already bound")
	}
	if err = q.requireProfileAbsent(frame.InstanceID, true); err != nil {
		return out, err
	}
	qualificationPath, pathErr := q.path(frame.InstanceID)
	if pathErr != nil {
		return out, pathErr
	}
	if _, pathErr = os.Lstat(qualificationPath); !errors.Is(pathErr, os.ErrNotExist) {
		return out, errors.Join(pathErr, errors.New("native checkpoint wake: qualification owner exists"))
	}
	if err = q.requirePhysicalNativeAbsent(frame.InstanceID); err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	lease.processGeneration = 0
	out.Version, out.NativeGeneration, out.NativeLease = 3, uuid.NewString(), lease
	out.RequestDigest = digest
	if err = j.write(out); err != nil {
		return out, err
	}
	if err = j.owner.prepareGeneration(ctx, lease, out.NativeGeneration); err != nil {
		return out, err
	}
	return out, nil
}
