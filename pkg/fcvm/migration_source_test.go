//go:build linux || darwin

// adr: 1530
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state"
)

type sourceCaptureVMM struct {
	*migrationPhysicalVMM
	capture func(context.Context, Lease, SnapshotSpec) (SnapshotInfo, error)
}

func (v *sourceCaptureVMM) SnapshotKeepAlive(ctx context.Context, l Lease, spec SnapshotSpec) (SnapshotInfo, error) {
	return v.capture(ctx, l, spec)
}

func sourcePhysicalFixture(t *testing.T) (*Manager, state.MigrationSource, WakeRequest) {
	t.Helper()
	m, p, req := migrationPhysicalFixture(t)
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	b, err := m.BindMigrationSource(t.Context(), state.MigrationSourceBinding{InstanceID: req.Instance, SourceNodeID: p.DestinationNodeID, SourceWakeID: uuid.NewString(), LeaseToken: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	return m, state.MigrationSource{MigrationSourceBinding: b, Phase: state.MigrationSourceBound}, req
}

func TestMigrationSourceRetirementReplaysWithoutTouchingReplacement(t *testing.T) {
	m, b, req := sourcePhysicalFixture(t)
	b.Phase = state.MigrationSourceRetiring
	outcome, proof, err := m.FinishMigrationSource(t.Context(), b, true)
	if err != nil || outcome != state.MigrationSourceRetired || proof == "" {
		t.Fatalf("retire source: %q %q %v", outcome, proof, err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("retirement proof retained resources")
	}
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	_, again, err := m.FinishMigrationSource(t.Context(), b, true)
	if err != nil || again != proof || m.LiveCount() != 1 {
		t.Fatalf("retry touched replacement: %q %v", again, err)
	}
	if _, err := m.SnapshotMigrationSource(t.Context(), b.MigrationSourceBinding, SnapshotSpec{}); err == nil {
		t.Fatal("delayed capture paused replacement")
	}
	if err := m.Destroy(t.Context(), b.InstanceID); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationSourceCleanupFailureKeepsOriginalResourceOwnership(t *testing.T) {
	m, b, _ := sourcePhysicalFixture(t)
	b.Phase = state.MigrationSourceRetiring
	recovery := m.nativeVMM().nativeRecoveryRuntime()
	resources := recovery.resources
	recovery.resources = func(Lease, netns.Config) error { return errors.New("native resource removal failed") }
	if _, proof, err := m.FinishMigrationSource(t.Context(), b, true); err == nil || proof != "" || m.LeasedCount() == 0 {
		t.Fatalf("failed removal released source: %q %v leases=%d", proof, err, m.LeasedCount())
	}
	recovery.resources = resources
	if outcome, proof, err := m.FinishMigrationSource(t.Context(), b, true); err != nil || outcome != state.MigrationSourceRetired || proof == "" || m.LeasedCount() != 0 {
		t.Fatalf("retry did not retire original: %q %q %v", outcome, proof, err)
	}
}

func TestMigrationSourceGenericReplacementPreservesOriginalProof(t *testing.T) {
	m, b, req := sourcePhysicalFixture(t)
	b.Phase = state.MigrationSourceRetiring
	if err := m.Destroy(t.Context(), b.InstanceID); err != nil {
		t.Fatal(err)
	}
	j := migrationSourceJournal{owner: m.nativeVMM().nativeRecoveryRuntime().journal}
	if err := os.WriteFile(filepath.Join(j.root(b.InstanceID), ".launch-interrupted"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	outcome, proof, err := m.FinishMigrationSource(t.Context(), b, true)
	if err != nil || outcome != state.MigrationSourceRetired || proof == "" || m.LiveCount() != 1 {
		t.Fatalf("generic replacement erased old retirement: %q %q %v", outcome, proof, err)
	}
	if err := m.Destroy(t.Context(), b.InstanceID); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationSourceDaemonReplacementUsesOriginalJournal(t *testing.T) {
	m, b, _ := sourcePhysicalFixture(t)
	b.Phase = state.MigrationSourceRetiring
	next := NewManager(&fakeRunner{}, m.vmm, Paths{}, m.fcVersion, nil, nil)
	next.nativeQualificationNodeID = b.SourceNodeID
	outcome, proof, err := next.FinishMigrationSource(t.Context(), b, true)
	if err != nil || outcome != state.MigrationSourceRetired || proof == "" || next.LeasedCount() != 0 {
		t.Fatalf("restart lost cleanup authority: %q %q %v", outcome, proof, err)
	}
}

func TestMigrationSourceMissingOrChangedEvidenceCannotRetire(t *testing.T) {
	for _, mode := range []string{"missing_native", "changed_generation", "missing_source", "corrupt_source", "corrupt_receipt"} {
		t.Run(mode, func(t *testing.T) {
			m, b, _ := sourcePhysicalFixture(t)
			b.Phase = state.MigrationSourceRetiring
			j := m.nativeVMM().nativeRecoveryRuntime().journal
			source := migrationSourceJournal{owner: j}
			original, err := j.read(b.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(source.path(b.MigrationSourceBinding))
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing_native":
				err = os.Remove(j.path(b.InstanceID))
			case "changed_generation":
				changed := original
				changed.Generation = uuid.NewString()
				err = j.write(changed)
			case "missing_source":
				err = os.Remove(source.path(b.MigrationSourceBinding))
			case "corrupt_source":
				err = os.WriteFile(source.path(b.MigrationSourceBinding), []byte(`{"version":1}`), 0o600)
			case "corrupt_receipt":
				err = source.write(nativeMigrationSource{Version: 1, Binding: b.MigrationSourceBinding, Outcome: state.MigrationSourceRetired, Proof: "unrelated-proof"})
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := m.FinishMigrationSource(t.Context(), b, true); err == nil || m.LiveCount() != 1 {
				t.Fatal("unproven cleanup released original", err)
			}
			if err := j.write(original); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source.path(b.MigrationSourceBinding), data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := m.FinishMigrationSource(t.Context(), b, true); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigrationSourceResumeReceiptFencesDelayedCaptureAndRetirement(t *testing.T) {
	m, b, _ := sourcePhysicalFixture(t)
	outcome, proof, err := m.FinishMigrationSource(t.Context(), b, false)
	if err != nil || outcome != state.MigrationSourceResumed || proof == "" {
		t.Fatalf("resume original: %q %q %v", outcome, proof, err)
	}
	if _, _, err := m.FinishMigrationSource(t.Context(), b, true); err == nil || m.LiveCount() != 1 {
		t.Fatal("late acknowledgement destroyed resumed source", err)
	}
	if _, err := m.SnapshotMigrationSource(t.Context(), b.MigrationSourceBinding, SnapshotSpec{}); err == nil {
		t.Fatal("capture followed resume receipt")
	}
	if err := m.Destroy(t.Context(), b.InstanceID); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationSourceNewRetirementAuthorityCanCloseResumedOriginal(t *testing.T) {
	m, target, req := sourcePhysicalFixture(t)
	if _, _, err := m.FinishMigrationSource(t.Context(), target, false); err != nil {
		t.Fatal(err)
	}
	// The logical owner ended after resume. A new durable retiring phase is
	// distinct authority from an old acknowledgement against a bound source.
	target.Phase = state.MigrationSourceRetiring
	if outcome, _, err := m.FinishMigrationSource(t.Context(), target, true); err != nil || outcome != state.MigrationSourceRetired {
		t.Fatalf("retirement could not close original resumed generation: %q %v", outcome, err)
	}
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.FinishMigrationSource(t.Context(), target, true); err != nil || m.LiveCount() != 1 {
		t.Fatal("old retirement touched replacement", err)
	}
	if err := m.Destroy(t.Context(), target.InstanceID); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationSourceRetirementJoinsCaptureAndRetainsFailure(t *testing.T) {
	m, b, _ := sourcePhysicalFixture(t)
	b.Phase = state.MigrationSourceRetiring
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	v := &sourceCaptureVMM{migrationPhysicalVMM: m.vmm.(*migrationPhysicalVMM)}
	v.capture = func(context.Context, Lease, SnapshotSpec) (SnapshotInfo, error) {
		once.Do(func() { close(entered); <-release })
		return SnapshotInfo{}, errors.New("snapshot reply lost")
	}
	m.vmm = v
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	captured := make(chan error, 1)
	go func() {
		_, err := m.SnapshotMigrationSource(t.Context(), b.MigrationSourceBinding, SnapshotSpec{})
		captured <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("capture never dispatched")
	}
	retired := make(chan error, 1)
	go func() { _, _, err := m.FinishMigrationSource(t.Context(), b, true); retired <- err }()
	select {
	case err := <-retired:
		t.Fatalf("retirement passed active capture: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-captured; err == nil {
		t.Fatal("capture failure lost")
	}
	if err := <-retired; err != nil {
		t.Fatal(err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("retirement did not join capture")
	}
}
