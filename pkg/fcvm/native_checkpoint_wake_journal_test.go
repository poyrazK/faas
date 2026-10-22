//go:build linux || darwin

// adr: 590
package fcvm

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func checkpointWakeFixture(t *testing.T) (*nativeCheckpointWakeJournal, state.ProjectEnvironmentCloneWakeDispatch) {
	q, f, _ := nativeQualificationFixture(t)
	return q.owner.checkpointWakes(f.NodeID), state.ProjectEnvironmentCloneWakeDispatch{InstanceID: f.InstanceID, AppID: f.AppID, DeploymentID: f.DeploymentID, NodeID: f.NodeID, WakeID: f.WakeID, RequestReason: "environment-clone:" + uuid.NewString(), Method: "cold_boot", DispatchedAt: time.Now().UTC().Round(0)}
}

func TestNativeCheckpointWakeOriginalTombstone(t *testing.T) {
	w, frame := checkpointWakeFixture(t)
	tombstone, err := w.revoke(t.Context(), frame)
	if err != nil || !tombstone.Revoked || tombstone.Claimed {
		t.Fatal("pre-dispatch revocation failed", err)
	}
	recovered := w.owner.checkpointWakes(frame.NodeID)
	if out, err := recovered.claim(t.Context(), frame); err == nil || out != (nativeCheckpointWakeRecord{}) {
		t.Fatal("delayed claim bypassed original tombstone", err)
	}
	if out, err := recovered.revoke(t.Context(), frame); err != nil || out != tombstone {
		t.Fatal("restart changed tombstone", err)
	}
	if err := w.owner.prepare(t.Context(), qualificationLease(frame.InstanceID)); err == nil {
		t.Fatal("generic producer bypassed incoming fence")
	}
	foreign := frame
	foreign.DeploymentID = uuid.NewString()
	if out, err := w.revoke(t.Context(), foreign); err == nil || out != (nativeCheckpointWakeRecord{}) {
		t.Fatal("foreign request borrowed tombstone")
	}
	path, _ := w.path(frame.InstanceID)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("journal is not private", err)
	}
}

func TestNativeCheckpointWakeClaimCannotReplayOrBorrowOwner(t *testing.T) {
	w, frame := checkpointWakeFixture(t)
	claimed, err := w.claim(t.Context(), frame)
	if err != nil || !claimed.Claimed || claimed.Revoked || claimed.Dispatch != frame {
		t.Fatal("original claim failed", err)
	}
	if out, err := w.claim(t.Context(), frame); err == nil || out != (nativeCheckpointWakeRecord{}) {
		t.Fatal("claim replayed")
	}
	if err := w.owner.prepare(t.Context(), qualificationLease(frame.InstanceID)); err == nil {
		t.Fatal("incoming claim granted generic native authority")
	}
	revoked, err := w.revoke(t.Context(), frame)
	if err != nil || !revoked.Revoked || revoked.Generation != claimed.Generation || revoked.Dispatch != frame {
		t.Fatal("revoke changed original ownership", err)
	}
	q, qualified, ctx := nativeQualificationFixture(t)
	existing, err := q.claim(ctx, qualified)
	if err != nil {
		t.Fatal(err)
	}
	other := frame
	other.InstanceID = qualified.InstanceID
	other.NodeID = qualified.NodeID
	if out, err := q.owner.checkpointWakes(qualified.NodeID).claim(ctx, other); err == nil || out != (nativeCheckpointWakeRecord{}) {
		t.Fatal("wake borrowed qualification instance")
	}
	if _, err := q.revoke(ctx, existing.Execution); err != nil {
		t.Fatal(err)
	}
}

func TestNativeCheckpointWakeLostPublicationAndDamagedEvidence(t *testing.T) {
	w, frame := checkpointWakeFixture(t)
	lost := errors.New("publication reply lost")
	w.writeValue = func(path string, record nativeCheckpointWakeRecord) error {
		if err := writeNativeJournalValue(path, record); err != nil {
			return err
		}
		return lost
	}
	if out, err := w.claim(t.Context(), frame); !errors.Is(err, lost) || out != (nativeCheckpointWakeRecord{}) {
		t.Fatal("uncertain publication returned authority", err)
	}
	w.writeValue = nil
	if _, err := w.claim(t.Context(), frame); err == nil {
		t.Fatal("uncertain claim replayed")
	}
	path, _ := w.path(frame.InstanceID)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, damage := range []string{string(raw) + " {}", strings.Replace(string(raw), `"version":3`, `"version":3,"version":3`, 1), strings.Replace(string(raw), `"claimed"`, `"unexpected"`, 1)} {
		if damage == string(raw) {
			continue
		}
		if err := os.WriteFile(path, []byte(damage), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := w.revoke(t.Context(), frame); err == nil || out != (nativeCheckpointWakeRecord{}) {
			t.Fatal("damaged original record accepted")
		}
		if err := w.owner.prepare(t.Context(), qualificationLease(frame.InstanceID)); err == nil {
			t.Fatal("damaged incoming fence bypassed")
		}
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	w.owner.bootID = func() (string, error) { return uuid.NewString(), nil }
	if out, err := w.revoke(t.Context(), frame); err == nil || out != (nativeCheckpointWakeRecord{}) {
		t.Fatal("prior boot accepted")
	}
}

func TestNativeCheckpointWakeRejectsCanceledAndExistingPhysicalOwner(t *testing.T) {
	w, frame := checkpointWakeFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if out, err := w.claim(ctx, frame); err == nil || out != (nativeCheckpointWakeRecord{}) {
		t.Fatal("canceled claim accepted")
	}
	if err := w.owner.prepare(t.Context(), qualificationLease(frame.InstanceID)); err != nil {
		t.Fatal(err)
	}
	for _, claim := range []bool{true, false} {
		if out, err := w.update(t.Context(), frame, claim); err == nil || out != (nativeCheckpointWakeRecord{}) {
			t.Fatal("existing physical owner borrowed")
		}
	}
}

func TestNativeCheckpointWakeConcurrentClaimAndRevoke(t *testing.T) {
	w, frame := checkpointWakeFixture(t)
	results := make(chan error, 2)
	go func() { _, err := w.claim(t.Context(), frame); results <- err }()
	go func() { _, err := w.revoke(t.Context(), frame); results <- err }()
	first, second := <-results, <-results
	if first != nil && second != nil {
		t.Fatal("neither incoming transition persisted", first, second)
	}
	original, err := w.read(frame.InstanceID)
	if err != nil || !original.Revoked || original.Dispatch != frame {
		t.Fatal("racing claim erased revocation", err)
	}
	if out, err := w.owner.checkpointWakes(frame.NodeID).claim(t.Context(), frame); err == nil || out != (nativeCheckpointWakeRecord{}) {
		t.Fatal("racing claim replayed after restart")
	}
}

func TestNativeCheckpointWakeExcludesQualificationClaimsAndTombstones(t *testing.T) {
	w, frame := checkpointWakeFixture(t)
	if _, err := w.claim(t.Context(), frame); err != nil {
		t.Fatal(err)
	}
	_, qualified, ctx := nativeQualificationFixture(t)
	qualified.InstanceID, qualified.NodeID = frame.InstanceID, frame.NodeID
	q := w.owner.qualifications(frame.NodeID)
	for _, claim := range []bool{true, false} {
		if _, err := q.update(ctx, qualified, claim); err == nil {
			t.Fatal("qualification borrowed retained wake instance")
		}
	}
}

func TestNativeCheckpointWakeManagerAuthenticatesNode(t *testing.T) {
	m, _, qualified, _, ctx := nativeQualificationManagerFixture(t)
	frame := state.ProjectEnvironmentCloneWakeDispatch{InstanceID: qualified.InstanceID, AppID: qualified.AppID, DeploymentID: qualified.DeploymentID, NodeID: qualified.NodeID, WakeID: qualified.WakeID, RequestReason: "environment-clone:" + uuid.NewString(), Method: "cold_boot", DispatchedAt: time.Now().UTC().Round(0)}
	for _, node := range []string{"", uuid.NewString()} {
		m.WithNativeQualificationNodeID(node)
		if out, err := m.ClaimCheckpointWakeDispatch(ctx, frame); err == nil || out != (NativeCheckpointWakeOwnership{}) {
			t.Fatal("foreign or unconfigured node accepted")
		}
	}
	m.WithNativeQualificationNodeID(frame.NodeID)
	original, err := m.ClaimCheckpointWakeDispatch(ctx, frame)
	if err != nil || original.Dispatch != frame || !original.Claimed || original.Revoked {
		t.Fatal("original manager claim failed", err)
	}
	revoked, err := m.RevokeCheckpointWakeDispatch(ctx, frame)
	if err != nil || !revoked.Revoked || revoked.IncomingGeneration != original.IncomingGeneration || revoked.KernelBootID != original.KernelBootID {
		t.Fatal("manager revocation changed original ownership", err)
	}
}

func TestNativeCheckpointWakeExcludesRestoreQualificationProfile(t *testing.T) {
	w, frame := checkpointWakeFixture(t)
	q := w.owner.qualifications(frame.NodeID)
	path, err := q.restores().path(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(q.restores().root(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("damaged restore ownership"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := w.claim(t.Context(), frame); err == nil || out != (nativeCheckpointWakeRecord{}) {
		t.Fatal("checkpoint borrowed restore ownership")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := w.claim(t.Context(), frame); err != nil {
		t.Fatal(err)
	}

}

func TestNativeCheckpointWakeBlocksRestoreQualificationClaim(t *testing.T) {
	restore, qualified, ctx := nativeQualificationRestoreFixture(t)
	_, frame := checkpointWakeFixture(t)
	frame.InstanceID, frame.AppID, frame.DeploymentID, frame.NodeID, frame.WakeID = qualified.InstanceID, qualified.AppID, qualified.DeploymentID, qualified.NodeID, qualified.WakeID
	w := restore.incoming.owner.checkpointWakes(frame.NodeID)
	if _, err := w.claim(ctx, frame); err != nil {
		t.Fatal(err)
	}
	if _, err := restore.claim(ctx, qualified, "1.7.0"); err == nil {
		t.Fatal("restore qualification borrowed checkpoint instance")
	}
}
