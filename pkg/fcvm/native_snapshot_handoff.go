// adr: 568 — host output anchors require a distinct original-VM namespace handoff.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"

	"github.com/onebox-faas/faas/pkg/jailsetup"
)

type nativeSnapshotOutputFrame struct {
	Scope        jailsetup.SnapshotOutputScope    `json:"scope"`
	InputsClosed bool                             `json:"inputs_closed"`
	Receipt      *jailsetup.SnapshotOutputReceipt `json:"receipt,omitempty"`
	Memory       *nativeSnapshotMemoryFrame       `json:"memory,omitempty"`
}

func (f *nativeSnapshotOutputFrame) UnmarshalJSON(data []byte) error {
	var shapeErr error
	for _, fields := range [][]string{{"scope", "inputs_closed"}, {"scope", "inputs_closed", "receipt"}, {"scope", "inputs_closed", "receipt", "memory"}} {
		_, shapeErr = nativeJournalObjectFields(data, fields)
		if shapeErr == nil {
			break
		}
	}
	if shapeErr != nil {
		return shapeErr
	}
	type plain nativeSnapshotOutputFrame
	return json.Unmarshal(data, (*plain)(f))
}
func (f nativeSnapshotOutputFrame) validate(owner, launch nativeLaunchRecord) error {
	s := f.Scope
	if err := s.Validate(); err != nil {
		return err
	}
	if !owner.Authorized || s.Generation != owner.Generation || s.BootID != owner.KernelBootID || s.PID != owner.PID || s.StartTime != owner.StartTime || s.UID != owner.Lease.UID || s.GID != owner.Lease.GID || (launch.Authorized || launch.ResourcesRemoved) && !f.InputsClosed {
		return errors.New("native snapshot handoff: original physical or input authority changed")
	}
	if f.Memory != nil {
		if f.Receipt == nil {
			return errors.New("native snapshot memory: original handoff receipt is required")
		}
		if err := f.Memory.validate(owner); err != nil {
			return err
		}
	}
	if f.Receipt != nil {
		if !launch.Authorized || !launch.ResourcesRemoved {
			return errors.New("native snapshot handoff: receipt precedes helper retirement")
		}
		return f.Receipt.Validate(s)
	}
	return nil
}

type nativeSnapshotOutputInputs struct {
	scope jailsetup.SnapshotOutputScope
	files []*os.File
}

func (p *nativeSnapshotOutputInputs) Close() error {
	var err error
	for _, file := range p.files {
		if file != nil {
			err = errors.Join(err, file.Close())
		}
	}
	p.files = nil
	return err
}

type nativeSnapshotOutputHandoffBackend interface {
	CheckSnapshotOutputHandoff(context.Context, *JailerVMM) error
	HandoffSnapshotOutputs(context.Context, *JailerVMM, nativeLaunchRecord) error
}

func newNativeSnapshotOutputCommand(helper string) (*exec.Cmd, error) {
	cmd, err := newNativeHostHelperCommand(helper, []string{helper})
	if err != nil {
		return nil, err
	}
	cmd.Args = []string{"vmmd-host-helper", "--launch-snapshot-output-setup", "3"}
	return cmd, nil
}

func (j *nativeHostHelperJournal) lockOwner(ctx context.Context, expected nativeLaunchRecord) (func() error, error) {
	if j.lockedOwner != nil {
		if !sameNativeSnapshotProcess(*j.lockedOwner, expected) {
			return nil, errors.New("native helper: original held physical owner changed")
		}
		return func() error { return nil }, ctx.Err()
	}
	lock, err := j.owner.lock(ctx, expected.Lease.Instance)
	if err != nil {
		return nil, err
	}
	return lock.Close, nil
}

// The caller still holds the physical and both output epoch locks. An exit
// status, a recovered frame or joined inputs alone never grants mount readiness.
func (j *nativeHostHelperJournal) confirmSnapshotOutputReceipt(ctx context.Context, owner nativeLaunchRecord, data []byte) error {
	if j.snapshotPermit == nil {
		return errors.New("native snapshot handoff: original capture permit required")
	}
	var receipt jailsetup.SnapshotOutputReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return err
	}
	current, err := j.owner.read(owner.Lease.Instance)
	if err != nil || !sameNativeSnapshotProcess(current, owner) {
		return errors.Join(err, errors.New("native snapshot handoff: original physical owner changed"))
	}
	images := nativeImageSourceJournal{owner: j.owner, backend: j.owner.imageSources}
	if err := images.captureOutputAuthority(ctx, owner, current, *j.snapshotPermit); err != nil {
		return err
	}
	frames, err := j.records(current)
	if err != nil {
		return err
	}
	var match *nativeHostHelperRecord
	for i := range frames {
		if frames[i].SnapshotOutput != nil {
			if match != nil {
				return errors.New("native snapshot handoff: multiple original scopes")
			}
			match = &frames[i]
		}
	}
	if match == nil || match.SnapshotOutput.Scope.CaptureID != j.snapshotPermit.Capture.CaptureID || !match.Launch.Authorized || !match.Launch.ResourcesRemoved || !match.SnapshotOutput.InputsClosed || j.groups == nil {
		return errors.New("native snapshot handoff: original helper retirement incomplete")
	}
	if err := j.groups.Removed(match.Group); err != nil {
		return err
	}
	if err := receipt.Validate(match.SnapshotOutput.Scope); err != nil {
		return err
	}
	match.SnapshotOutput.Receipt = &receipt
	return j.write(current, *match)
}

func nativeSnapshotNamespaceScope(frame nativeHostHelperRecord) *jailsetup.DeviceSetupScope {
	if frame.JailDevice != nil {
		return &frame.JailDevice.Scope
	}
	if frame.SnapshotOutput != nil {
		return &jailsetup.DeviceSetupScope{Namespace: frame.SnapshotOutput.Scope.Namespace}
	}
	return nil
}
