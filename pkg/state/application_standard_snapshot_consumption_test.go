package state

// adr: 595
// Shared durable-store tests use simulated native witnesses, never a running VM.

import (
	"errors"
	"strings"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func standardConsumedRestoreReceipt(b runtimeadmission.Binding, f standardRestoreFixture) runtimeadmission.Receipt {
	r := consumedNativeReceipt(b, f.Capture)
	r.Method = vmmdpb.WakeMethod_WAKE_RESTORE
	r.ArtifactConsumption.ConfigHash = runtimeadmission.SnapshotLoadCommandHash(false)
	c := f.Evidence.Capture
	r.SnapshotConsumption = runtimeadmission.SnapshotConsumption{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: b.SnapshotCaptureToken, EvidenceHash: b.SnapshotEvidenceHash, Memory: c.Memory, VMState: c.VMState, PrivateDrive: c.PrivateDrive, MappedMemoryBytes: c.Memory.Bytes}
	return r
}

func standardSnapshotConsumptionPublication(t *testing.T, newStore func(*testing.T) standardSnapshotPublicationTestStore) {
	t.Helper()
	for _, fault := range []string{"complete", "memory", "vmstate", "private", "command", "partial", "cold", "paused", "missing"} {
		t.Run(fault, func(t *testing.T) {
			s := newStore(t)
			f := standardRestoreTestFixture(t, s)
			b, err := s.IssueInstanceApplicationStandardBoot(t.Context(), f.Target.State, f.Binding)
			if err != nil {
				t.Fatal(err)
			}
			r := standardConsumedRestoreReceipt(b, f)
			switch fault {
			case "memory":
				r.SnapshotConsumption.Memory.Digest = "sha256:" + strings.Repeat("0", 64)
			case "vmstate":
				r.SnapshotConsumption.VMState.Digest = "sha256:" + strings.Repeat("0", 64)
			case "private":
				r.SnapshotConsumption.PrivateDrive.Digest = "sha256:" + strings.Repeat("0", 64)
			case "command":
				r.ArtifactConsumption.ConfigHash = strings.Repeat("0", 64)
			case "partial":
				r.SnapshotConsumption.MappedMemoryBytes--
			case "cold":
				r.Method = vmmdpb.WakeMethod_WAKE_COLD_BOOT
			case "paused":
				r.Paused = true
			case "missing":
				r.SnapshotConsumption = runtimeadmission.SnapshotConsumption{}
			}
			if fault == "memory" || fault == "vmstate" || fault == "private" {
				if err := r.Check(b, time.Now()); err != nil {
					t.Fatal("substitution fixture is not structurally valid", err)
				}
			}
			_, err = s.PublishInstanceApplicationStandardRuntime(t.Context(), f.Target.State, StateRunning, r)
			if fault != "complete" {
				if !errors.Is(err, ErrApplicationStandardRuntimeStale) && !errors.Is(err, ErrInvalidArgument) {
					t.Fatal("changed consumption published", err)
				}
				assertNativeBootUnpublished(t, s, f.Target)
				return
			}
			if err != nil {
				t.Fatal("complete measured receipt refused", err)
			}
			got, err := s.(InstanceApplicationStandardRuntimeReceiptStore).GetInstanceApplicationStandardRuntimeReceipt(t.Context(), f.Target.ID)
			if err != nil || !got.Equal(r) {
				t.Fatal("durable receipt lost measured snapshot facts", err)
			}
			got.SnapshotConsumption.Memory.Digest = "reader-change"
			again, err := s.(InstanceApplicationStandardRuntimeReceiptStore).GetInstanceApplicationStandardRuntimeReceipt(t.Context(), f.Target.ID)
			if err != nil || !again.Equal(r) {
				t.Fatal("reader changed durable receipt", err)
			}
		})
	}
}

func TestMemStandardSnapshotConsumptionPublication(t *testing.T) {
	standardSnapshotConsumptionPublication(t, func(*testing.T) standardSnapshotPublicationTestStore { return NewMemStore() })
}
