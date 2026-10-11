package jailsetup

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func snapshotScopeFixture() SnapshotOutputScope {
	return SnapshotOutputScope{Generation: uuid.NewString(), BootID: uuid.NewString(), CaptureID: uuid.NewString(), PID: 123, StartTime: 456, UID: 20001, GID: 20001, ParentPID: 789, ParentStartTime: 1234, ParentFDs: [5]int{10, 11, 12, 13, 14}, Root: DeviceFDIdentity{1, 1}, Namespace: DeviceFDIdentity{2, 2}, PIDHandle: DeviceFDIdentity{3, 3}, RootMountID: 100, Epochs: [2]string{uuid.NewString(), uuid.NewString()}, Outputs: [2]DeviceFDIdentity{{4, 4}, {4, 5}}, Targets: [2]DeviceFDIdentity{{5, 6}, {5, 7}}}
}

func TestSnapshotOutputScopeRefusesForeignOrIncompleteCohort(t *testing.T) {
	s := snapshotScopeFixture()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"capture", "generation", "boot", "pid", "start", "uid", "gid", "parent", "duplicate_fd", "missing_fd", "missing_input", "duplicate_output", "duplicate_epoch", "missing_target", "adopted_target", "cross_output_alias"} {
		t.Run(change, func(t *testing.T) {
			bad := s
			switch change {
			case "capture":
				bad.CaptureID = "foreign"
			case "generation":
				bad.Generation = uuid.Nil.String()
			case "boot":
				bad.BootID = ""
			case "pid":
				bad.PID = 0
			case "start":
				bad.StartTime = 0
			case "uid":
				bad.UID = 0
			case "gid":
				bad.GID++
			case "parent":
				bad.ParentStartTime = 0
			case "duplicate_fd":
				bad.ParentFDs[4] = bad.ParentFDs[3]
			case "missing_fd":
				bad.ParentFDs[4] = 0
			case "missing_input":
				bad.Outputs[0].Inode = 0
			case "duplicate_output":
				bad.Outputs[1] = bad.Outputs[0]
			case "duplicate_epoch":
				bad.Epochs[1] = bad.Epochs[0]
			case "missing_target":
				bad.Targets[0].Inode = 0
			case "adopted_target":
				bad.Targets[0] = bad.Outputs[0]
			case "cross_output_alias":
				bad.Targets[0] = bad.Outputs[1]
			}
			if bad.Validate() == nil {
				t.Fatal("changed original cohort accepted")
			}
		})
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got SnapshotOutputScope
	if err := json.Unmarshal(data, &got); err != nil || got != s {
		t.Fatal("scope round trip failed", err)
	}
	for _, bad := range []string{strings.Replace(string(data), `"parent_fds":[10,11,12,13,14]`, `"parent_fds":[10,11,12,13]`, 1), strings.Replace(string(data), `"root":{"device":1,"inode":1}`, `"root":{"device":1}`, 1), strings.Replace(string(data), `"root":`, `"unknown":`, 1), strings.Replace(string(data), `"capture_id":`, `"capture_id":null,"capture_id":`, 1), strings.TrimSuffix(string(data), "}") + `,"extra":true}`} {
		if json.Unmarshal([]byte(bad), &got) == nil {
			t.Fatal("ambiguous or incomplete durable scope decoded")
		}
	}
}

func TestSnapshotOutputReceiptRequiresOriginalCompletePrivateMounts(t *testing.T) {
	s := snapshotScopeFixture()
	r := SnapshotOutputReceipt{Scope: s, MountIDs: [2]uint64{101, 102}}
	if err := r.Validate(s); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []SnapshotOutputReceipt{{Scope: s, MountIDs: [2]uint64{0, 102}}, {Scope: s, MountIDs: [2]uint64{101, 101}}, {Scope: s, MountIDs: [2]uint64{100, 102}}} {
		if bad.Validate(s) == nil {
			t.Fatal("incomplete mount proof accepted")
		}
	}
	foreign := s
	foreign.CaptureID = uuid.NewString()
	if r.Validate(foreign) == nil {
		t.Fatal("another capture borrowed a mount receipt")
	}
	good := "101 100 8:1 /file /capture rw,nosuid,nodev,noexec - ext4 /dev/sda rw\n"
	if !snapshotMountAccess([]byte(good), 101) {
		t.Fatal("private writable output mount refused")
	}
	for _, bad := range []string{strings.Replace(good, ",nodev", "", 1), strings.Replace(good, ",noexec", "", 1), strings.Replace(good, ",nosuid", "", 1), strings.Replace(good, "rw,nosuid", "ro,nosuid", 1), good + "incomplete\n"} {
		if snapshotMountAccess([]byte(bad), 101) {
			t.Fatal("unsafe output mount accepted")
		}
	}
}
