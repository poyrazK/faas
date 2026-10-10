// adr: 568 — live capture mounts have an original namespace and one-shot receipt.
package jailsetup

import (
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

// Child FDs: gate=3, original root=4, namespace=5, pidfd=6, memory=7,
// device-state=8. Names derive exclusively from the original capture UUID.
type SnapshotOutputScope struct {
	Generation      string              `json:"generation"`
	BootID          string              `json:"boot_id"`
	CaptureID       string              `json:"capture_id"`
	PID             int                 `json:"pid"`
	StartTime       uint64              `json:"start_time"`
	UID             int                 `json:"uid"`
	GID             int                 `json:"gid"`
	ParentPID       int                 `json:"parent_pid"`
	ParentStartTime uint64              `json:"parent_start_time"`
	ParentFDs       [5]int              `json:"parent_fds"`
	Root            DeviceFDIdentity    `json:"root"`
	Namespace       DeviceFDIdentity    `json:"namespace"`
	PIDHandle       DeviceFDIdentity    `json:"pid_handle"`
	RootMountID     uint64              `json:"root_mount_id"`
	Epochs          [2]string           `json:"epochs"`
	Outputs         [2]DeviceFDIdentity `json:"outputs"`
	Targets         [2]DeviceFDIdentity `json:"targets"`
}

func (s *SnapshotOutputScope) UnmarshalJSON(data []byte) error {
	fields, err := deviceObjectFields(data, []string{"generation", "boot_id", "capture_id", "pid", "start_time", "uid", "gid", "parent_pid", "parent_start_time", "parent_fds", "root", "namespace", "pid_handle", "root_mount_id", "epochs", "outputs", "targets"})
	if err != nil {
		return err
	}
	for _, field := range []struct {
		name  string
		count int
	}{{"parent_fds", 5}, {"epochs", 2}, {"outputs", 2}, {"targets", 2}} {
		var values []json.RawMessage
		if err := json.Unmarshal(fields[field.name], &values); err != nil || len(values) != field.count {
			return errors.New("native snapshot handoff: exact input cohort required")
		}
	}
	type plain SnapshotOutputScope
	return json.Unmarshal(data, (*plain)(s))
}

func (s SnapshotOutputScope) Validate() error {
	for _, value := range []string{s.Generation, s.BootID, s.CaptureID, s.Epochs[0], s.Epochs[1]} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return errors.New("native snapshot handoff: invalid original generation or epoch")
		}
	}
	if s.PID <= 0 || s.StartTime == 0 || s.ParentPID <= 0 || s.ParentStartTime == 0 || s.UID < 20000 || s.UID > 29999 || s.GID != s.UID || s.RootMountID == 0 || s.Epochs[0] == s.Epochs[1] || s.Outputs[0] == s.Outputs[1] || s.Targets[0] == s.Targets[1] {
		return errors.New("native snapshot handoff: incomplete original task or output cohort")
	}
	seen := map[int]bool{}
	for _, fd := range s.ParentFDs {
		if fd < 3 || seen[fd] {
			return errors.New("native snapshot handoff: invalid parent descriptors")
		}
		seen[fd] = true
	}
	for _, identity := range s.Identities() {
		if identity.Inode == 0 {
			return errors.New("native snapshot handoff: missing input identity")
		}
	}
	for i, target := range s.Targets {
		if target.Inode == 0 || target == s.Outputs[i] || target == s.Outputs[1-i] {
			return errors.New("native snapshot handoff: missing original placeholder")
		}
	}
	return nil
}

func (s SnapshotOutputScope) Identities() [5]DeviceFDIdentity {
	return [5]DeviceFDIdentity{s.Root, s.Namespace, s.PIDHandle, s.Outputs[0], s.Outputs[1]}
}
func (s SnapshotOutputScope) Names() [2]string {
	return [2]string{"capture-" + s.CaptureID + "-mem", "capture-" + s.CaptureID + "-vmstate"}
}

type SnapshotOutputReceipt struct {
	Scope    SnapshotOutputScope `json:"scope"`
	MountIDs [2]uint64           `json:"mount_ids"`
}

func (r *SnapshotOutputReceipt) UnmarshalJSON(data []byte) error {
	fields, err := deviceObjectFields(data, []string{"scope", "mount_ids"})
	if err != nil {
		return err
	}
	var ids []json.RawMessage
	if err := json.Unmarshal(fields["mount_ids"], &ids); err != nil || len(ids) != 2 {
		return errors.New("native snapshot handoff: exact mount cohort required")
	}
	type plain SnapshotOutputReceipt
	return json.Unmarshal(data, (*plain)(r))
}
func (r SnapshotOutputReceipt) Validate(expected SnapshotOutputScope) error {
	if err := r.Scope.Validate(); err != nil {
		return err
	}
	if r.Scope != expected || r.MountIDs[0] == 0 || r.MountIDs[1] == 0 || r.MountIDs[0] == r.MountIDs[1] || r.MountIDs[0] == expected.RootMountID || r.MountIDs[1] == expected.RootMountID {
		return errors.New("native snapshot handoff: original complete mount receipt required")
	}
	return nil
}
