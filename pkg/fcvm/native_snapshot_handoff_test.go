//go:build linux || darwin

package fcvm

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/jailsetup"
)

func TestNativeSnapshotHandoffFrameCannotPromoteUnretiredHelper(t *testing.T) {
	j, owner, _, _ := nativeJailDeviceFixture(t)
	s := jailsetup.SnapshotOutputScope{Generation: owner.Generation, BootID: owner.KernelBootID, CaptureID: uuid.NewString(), PID: owner.PID, StartTime: owner.StartTime, UID: owner.Lease.UID, GID: owner.Lease.GID, ParentPID: 123, ParentStartTime: 456, ParentFDs: [5]int{10, 11, 12, 13, 14}, Root: jailsetup.DeviceFDIdentity{Device: 1, Inode: 1}, Namespace: jailsetup.DeviceFDIdentity{Device: 2, Inode: 2}, PIDHandle: jailsetup.DeviceFDIdentity{Device: 3, Inode: 3}, RootMountID: 100, Epochs: [2]string{uuid.NewString(), uuid.NewString()}, Outputs: [2]jailsetup.DeviceFDIdentity{{Device: 4, Inode: 4}, {Device: 4, Inode: 5}}, Targets: [2]jailsetup.DeviceFDIdentity{{Device: 5, Inode: 6}, {Device: 5, Inode: 7}}}
	f := nativeSnapshotOutputFrame{Scope: s, InputsClosed: true, Receipt: &jailsetup.SnapshotOutputReceipt{Scope: s, MountIDs: [2]uint64{101, 102}}}
	launch := owner
	launch.ResourcesRemoved = true
	if err := f.validate(owner, launch); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"unjoined", "unclosed", "foreign_capture", "foreign_process", "foreign_epoch"} {
		t.Run(change, func(t *testing.T) {
			bad, original, child := f, owner, launch
			switch change {
			case "unjoined":
				child.ResourcesRemoved = false
			case "unclosed":
				bad.InputsClosed = false
			case "foreign_capture":
				bad.Scope.CaptureID = uuid.NewString()
			case "foreign_process":
				original.PID++
			case "foreign_epoch":
				bad.Scope.Epochs[0] = uuid.NewString()
			}
			if bad.validate(original, child) == nil {
				t.Fatal("changed receipt or unretired helper promoted")
			}
		})
	}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var decoded nativeSnapshotOutputFrame
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.validate(owner, launch) != nil {
		t.Fatal("exact frame did not survive restart", err)
	}
	j.lockedOwner = &owner
	foreign := owner
	foreign.PID++
	if _, err := j.lockOwner(t.Context(), foreign); err == nil {
		t.Fatal("held lock borrowed another original physical process")
	}
}
