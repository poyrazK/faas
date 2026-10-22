//go:build linux || darwin

// adr: 590
package fcvm

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func checkpointRequestFixture(frame state.ProjectEnvironmentCloneWakeDispatch, lease Lease) WakeRequest {
	return WakeRequest{Instance: frame.InstanceID, AppID: frame.AppID, DeploymentID: frame.DeploymentID, Plan: lease.Plan, MemSizeMiB: lease.MemoryMaxMiB, CPUMillicores: lease.CPUMillicores, VcpuCount: 1, BaseKey: "base-original", LayerKey: "layer-original"}
}

func TestNativeCheckpointWakeRequestCommitment(t *testing.T) {
	w, frame := checkpointWakeFixture(t)
	frame.Method = "cold_boot"
	lease := qualificationLease(frame.InstanceID)
	req := checkpointRequestFixture(frame, lease)
	req.APIEnvEntries = []APIEnvEntry{{Key: "MODE", Value: "original"}}
	if _, err := w.claim(t.Context(), frame); err != nil {
		t.Fatal(err)
	}
	digest, err := checkpointWakeRequestDigest(frame, lease, req)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := w.preparePhysicalDigest(t.Context(), frame, lease, digest)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := w.owner.checkpointWakes(frame.NodeID).read(frame.InstanceID)
	if err != nil || recovered != bound {
		t.Fatal("lost original commitment", err)
	}
	if err := recovered.matchRequest(frame, lease, req); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*WakeRequest){
		"artifact":    func(r *WakeRequest) { r.LayerKey = "other" },
		"ram":         func(r *WakeRequest) { r.MemSizeMiB++ },
		"cpu":         func(r *WakeRequest) { r.CPUMillicores++ },
		"network":     func(r *WakeRequest) { r.EgressAllowlist = []string{"1.2.3.0/24"} },
		"environment": func(r *WakeRequest) { r.APIEnvEntries = []APIEnvEntry{{Key: "MODE", Value: "changed"}} },
		"readiness":   func(r *WakeRequest) { r.HealthcheckPath = "/changed" },
		"execution":   func(r *WakeRequest) { r.ExecutionLeaseToken = "forbidden" },
		"prepared":    func(r *WakeRequest) { r.preparedSecretsEnvJSON = []byte("forbidden") },
	} {
		t.Run(name, func(t *testing.T) {
			changed := req
			mutate(&changed)
			if err := recovered.matchRequest(frame, lease, changed); err == nil {
				t.Fatal("changed request matched")
			}
		})
	}
	revoked, err := w.revoke(t.Context(), frame)
	if err != nil || revoked.RequestDigest != digest {
		t.Fatal("revocation lost commitment", err)
	}
	if err := revoked.matchRequest(frame, lease, req); err == nil {
		t.Fatal("revoked request matched")
	}
}

func TestNativeCheckpointWakeRequestLostPublication(t *testing.T) {
	w, frame := checkpointWakeFixture(t)
	frame.Method = "cold_boot"
	lease := qualificationLease(frame.InstanceID)
	req := checkpointRequestFixture(frame, lease)
	if _, err := w.claim(t.Context(), frame); err != nil {
		t.Fatal(err)
	}
	digest, err := checkpointWakeRequestDigest(frame, lease, req)
	if err != nil {
		t.Fatal(err)
	}
	lost := errors.New("publication reply lost")
	w.writeValue = func(path string, record nativeCheckpointWakeRecord) error {
		if err := writeNativeJournalValue(path, record); err != nil {
			return err
		}
		return lost
	}
	if out, err := w.preparePhysicalDigest(t.Context(), frame, lease, digest); !errors.Is(err, lost) || out != (nativeCheckpointWakeRecord{}) {
		t.Fatal("uncertain publication returned ownership", err)
	}
	recovered, err := w.owner.checkpointWakes(frame.NodeID).read(frame.InstanceID)
	if err != nil || recovered.RequestDigest != digest {
		t.Fatal("lost request commitment", err)
	}
	if out, err := w.owner.checkpointWakes(frame.NodeID).preparePhysicalDigest(t.Context(), frame, lease, digest); err == nil || out != (nativeCheckpointWakeRecord{}) {
		t.Fatal("uncertain binding replayed")
	}
}

func TestNativeCheckpointWakeRequestSnapshotBinding(t *testing.T) {
	_, frame := checkpointWakeFixture(t)
	frame.Method = "restore"
	lease := qualificationLease(frame.InstanceID)
	req := checkpointRequestFixture(frame, lease)
	req.Snapshot = &Snapshot{DeploymentID: frame.DeploymentID, StorageKey: "mem", VMStateStorageKey: "state", FCVersion: "original"}
	first, err := checkpointWakeRequestDigest(frame, lease, req)
	if err != nil {
		t.Fatal(err)
	}
	req.Snapshot.VMStateStorageKey = "changed"
	second, err := checkpointWakeRequestDigest(frame, lease, req)
	if err != nil || first == second {
		t.Fatal("snapshot substitution not detected", err)
	}
	req.Snapshot.DeploymentID = "foreign"
	if _, err := checkpointWakeRequestDigest(frame, lease, req); err == nil {
		t.Fatal("foreign snapshot accepted")
	}
}

func TestNativeCheckpointWakeRequestInvalidEncoding(t *testing.T) {
	_, frame := checkpointWakeFixture(t)
	frame.Method = "cold_boot"
	lease := qualificationLease(frame.InstanceID)
	req := checkpointRequestFixture(frame, lease)
	req.APIEnvEntries = []APIEnvEntry{{Key: "MODE", Value: string([]byte{0xff})}}
	if _, err := checkpointWakeRequestDigest(frame, lease, req); err == nil {
		t.Fatal("invalid UTF-8 acquired a commitment")
	}
	req.APIEnvEntries = nil
	req.ReadinessProbe = []byte("invalid-json")
	if _, err := checkpointWakeRequestDigest(frame, lease, req); err == nil {
		t.Fatal("invalid probe acquired a commitment")
	}
}

func TestNativeCheckpointWakeManagerBindsRequest(t *testing.T) {
	m, _, qualified, _, ctx := nativeQualificationManagerFixture(t)
	_, frame := checkpointWakeFixture(t)
	frame.InstanceID, frame.AppID, frame.DeploymentID, frame.NodeID, frame.WakeID = qualified.InstanceID, qualified.AppID, qualified.DeploymentID, qualified.NodeID, qualified.WakeID
	frame.Method = "cold_boot"
	lease := qualificationLease(frame.InstanceID)
	req := checkpointRequestFixture(frame, lease)
	if _, err := m.ClaimCheckpointWakeDispatch(ctx, frame); err != nil {
		t.Fatal(err)
	}
	wrong := req
	wrong.AppID = "foreign"
	if out, err := m.PrepareCheckpointWakeRequest(ctx, frame, lease, wrong); err == nil || out.NativeGeneration != "" {
		t.Fatal("foreign request acquired ownership")
	}
	bound, err := m.PrepareCheckpointWakeRequest(ctx, frame, lease, req)
	if err != nil || bound.NativeGeneration == "" {
		t.Fatal("request binding failed", err)
	}
	if err := m.CheckCheckpointWakeRequest(ctx, frame, lease, req); err != nil {
		t.Fatal(err)
	}
	if err := m.CheckCheckpointWakeRequest(ctx, frame, lease, wrong); err == nil {
		t.Fatal("manager matched foreign request")
	}
	j, err := m.checkpointWakeJournal(frame)
	if err != nil {
		t.Fatal(err)
	}
	record, err := j.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := record.matchRequest(frame, lease, req); err != nil {
		t.Fatal(err)
	}
	if out, err := m.PrepareCheckpointWakeRequest(ctx, frame, lease, req); err == nil || out.NativeGeneration != "" {
		t.Fatal("manager replayed bound request")
	}
}

func TestNativeCheckpointWakeLegacyWithoutRequestCannotMatch(t *testing.T) {
	w, frame := checkpointWakeFixture(t)
	frame.Method = "cold_boot"
	lease := qualificationLease(frame.InstanceID)
	if _, err := w.claim(t.Context(), frame); err != nil {
		t.Fatal(err)
	}
	bound, err := w.preparePhysical(t.Context(), frame, lease)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(bound)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(raw, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["version"] = json.RawMessage("2")
	delete(legacy, "request_digest")
	path, _ := w.path(frame.InstanceID)
	if err := writeNativeJournalValue(path, legacy); err != nil {
		t.Fatal(err)
	}
	recovered, err := w.read(frame.InstanceID)
	if err != nil || recovered.Version != 2 {
		t.Fatal("legacy generation unreadable", err)
	}
	if err := recovered.matchRequest(frame, lease, checkpointRequestFixture(frame, lease)); err == nil {
		t.Fatal("legacy record inferred request commitment")
	}
}
