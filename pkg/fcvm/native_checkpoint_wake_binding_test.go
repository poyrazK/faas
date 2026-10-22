//go:build linux || darwin

// adr: 590
package fcvm

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestNativeCheckpointWakeBindsOriginalPhysicalGeneration(t *testing.T) {
	w, frame := checkpointWakeFixture(t)
	original, err := w.claim(t.Context(), frame)
	if err != nil {
		t.Fatal(err)
	}
	lease := qualificationLease(frame.InstanceID)
	lease.processGeneration = 77
	bound, err := w.preparePhysical(t.Context(), frame, lease)
	if err != nil || bound.NativeGeneration == "" || bound.NativeGeneration == original.Generation || bound.Generation != original.Generation || bound.NativeLease.processGeneration != 0 {
		t.Fatal("original binding changed", err)
	}
	physical, err := w.owner.read(frame.InstanceID)
	if err != nil || physical.Generation != bound.NativeGeneration || physical.KernelBootID != bound.KernelBootID || !sameNativePhysicalLease(physical.Lease, lease) || physical.Authorized || physical.PID != 0 || physical.ExitConfirmed || physical.ResourcesRemoved {
		t.Fatal("prepared publication inferred launch or closure", err)
	}
	if out, err := w.owner.checkpointWakes(frame.NodeID).preparePhysical(t.Context(), frame, lease); err == nil || out != (nativeCheckpointWakeRecord{}) {
		t.Fatal("binding replayed after restart")
	}
	if err := w.owner.prepare(t.Context(), lease); err == nil {
		t.Fatal("generic preparation bypassed original binding")
	}
	if ticket, err := w.owner.beginLaunch(t.Context(), lease); err == nil {
		_ = ticket.close()
		t.Fatal("prepared binding granted generic producer authority")
	}
	revoked, err := w.revoke(t.Context(), frame)
	if err != nil || !revoked.Revoked || revoked.NativeGeneration != bound.NativeGeneration || revoked.NativeLease != bound.NativeLease {
		t.Fatal("revocation replaced physical ownership", err)
	}
	physical, err = w.owner.read(frame.InstanceID)
	if err != nil || physical.Revoked || physical.ExitConfirmed || physical.ResourcesRemoved {
		t.Fatal("incoming tombstone inferred physical revocation or closure", err)
	}
}

func TestNativeCheckpointWakeBindingPublicationUncertainty(t *testing.T) {
	for _, phase := range []string{"binding", "physical"} {
		t.Run(phase, func(t *testing.T) {
			w, frame := checkpointWakeFixture(t)
			if _, err := w.claim(t.Context(), frame); err != nil {
				t.Fatal(err)
			}
			lost := errors.New("publication acknowledgment lost")
			if phase == "binding" {
				w.writeValue = func(path string, r nativeCheckpointWakeRecord) error {
					if err := writeNativeJournalValue(path, r); err != nil {
						return err
					}
					return lost
				}
			} else {
				w.owner.writeRecord = func(path string, r nativeLaunchRecord) error {
					if err := writeNativeLaunchRecord(path, r); err != nil {
						return err
					}
					return lost
				}
			}
			lease := qualificationLease(frame.InstanceID)
			if out, err := w.preparePhysical(t.Context(), frame, lease); !errors.Is(err, lost) || out != (nativeCheckpointWakeRecord{}) {
				t.Fatal("uncertain publication returned ownership", err)
			}
			recovered := w.owner.checkpointWakes(frame.NodeID)
			original, err := recovered.read(frame.InstanceID)
			if err != nil || original.NativeGeneration == "" {
				t.Fatal("original planned generation lost", err)
			}
			if out, err := recovered.preparePhysical(t.Context(), frame, lease); err == nil || out != (nativeCheckpointWakeRecord{}) {
				t.Fatal("uncertain publication rebound original request")
			}
			if after, err := recovered.read(frame.InstanceID); err != nil || after != original {
				t.Fatal("failed replay changed original binding", err)
			}
			if phase == "binding" {
				if _, err := w.owner.read(frame.InstanceID); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("physical publication preceded binding acknowledgment", err)
				}
			} else {
				physical, err := w.owner.read(frame.InstanceID)
				if err != nil || physical.Generation != original.NativeGeneration {
					t.Fatal("uncertain physical publication lost original generation", err)
				}
			}
		})
	}
}

func TestNativeCheckpointWakeBindingRejectsChangedOwnership(t *testing.T) {
	w, frame := checkpointWakeFixture(t)
	if _, err := w.claim(t.Context(), frame); err != nil {
		t.Fatal(err)
	}
	foreign := frame
	foreign.WakeID = uuid.NewString()
	if out, err := w.preparePhysical(t.Context(), foreign, qualificationLease(frame.InstanceID)); err == nil || out != (nativeCheckpointWakeRecord{}) {
		t.Fatal("foreign wake borrowed binding")
	}
	for _, kind := range []string{"instance", "builder", "networkless", "plan"} {
		lease := qualificationLease(frame.InstanceID)
		switch kind {
		case "instance":
			lease.Instance = uuid.NewString()
		case "builder":
			lease.IsBuilder = true
		case "networkless":
			lease.Networkless = true
		case "plan":
			lease.Plan = ""
		}
		if out, err := w.preparePhysical(t.Context(), frame, lease); err == nil || out != (nativeCheckpointWakeRecord{}) {
			t.Fatal("invalid workload lease accepted", kind)
		}
	}
	if _, err := w.revoke(t.Context(), frame); err != nil {
		t.Fatal(err)
	}
	if out, err := w.preparePhysical(t.Context(), frame, qualificationLease(frame.InstanceID)); err == nil || out != (nativeCheckpointWakeRecord{}) {
		t.Fatal("revoked request bound physical generation")
	}
}

func TestNativeCheckpointWakeLegacyBindingUpgrade(t *testing.T) {
	w, frame := checkpointWakeFixture(t)
	original, err := w.claim(t.Context(), frame)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(raw, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["version"] = json.RawMessage("1")
	delete(legacy, "request_digest")
	delete(legacy, "native_generation")
	delete(legacy, "native_lease")
	path, _ := w.path(frame.InstanceID)
	if err := writeNativeJournalValue(path, legacy); err != nil {
		t.Fatal(err)
	}
	bound, err := w.preparePhysical(t.Context(), frame, qualificationLease(frame.InstanceID))
	if err != nil || bound.Version != 3 || bound.Generation != original.Generation || bound.Dispatch != original.Dispatch || bound.AcceptedAt != original.AcceptedAt {
		t.Fatal("legacy upgrade changed incoming identity", err)
	}
	if recovered, err := w.read(frame.InstanceID); err != nil || recovered != bound {
		t.Fatal("upgraded binding is not recoverable", err)
	}
}

func TestNativeCheckpointWakeManagerPreparesExactBinding(t *testing.T) {
	m, _, qualified, _, ctx := nativeQualificationManagerFixture(t)
	_, frame := checkpointWakeFixture(t)
	frame.InstanceID, frame.AppID, frame.DeploymentID, frame.NodeID, frame.WakeID = qualified.InstanceID, qualified.AppID, qualified.DeploymentID, qualified.NodeID, qualified.WakeID
	original, err := m.ClaimCheckpointWakeDispatch(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := m.PrepareCheckpointWakeLaunch(ctx, frame, qualificationLease(frame.InstanceID))
	if err != nil || bound.NativeGeneration == "" || bound.IncomingGeneration != original.IncomingGeneration || bound.Dispatch != frame {
		t.Fatal("manager lost original binding", err)
	}
	if out, err := m.PrepareCheckpointWakeLaunch(ctx, frame, qualificationLease(frame.InstanceID)); err == nil || out != (NativeCheckpointWakeOwnership{}) {
		t.Fatal("manager repeated original preparation", err)
	}
}
