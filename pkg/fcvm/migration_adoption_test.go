//go:build linux || darwin

// adr: 1529
package fcvm

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Native retirement runs against the journal and fake /proc. The portable
// cgroup hierarchy contains regular files in place of kernel control files.
type migrationPhysicalVMM struct{ *recoveryVMMFixture }

func (v *migrationPhysicalVMM) Kill(ctx context.Context, lease Lease) error {
	if err := os.RemoveAll(nativeCgroupScope(lease)); err != nil {
		return err
	}
	return v.recoveryVMMFixture.Kill(ctx, lease)
}

func (v *migrationPhysicalVMM) DestroyWithExport(ctx context.Context, lease Lease, out string) (int, error) {
	if lease.UID == 0 {
		physical, err := v.jailer.nativeRecovery.journal.read(lease.Instance)
		if err != nil {
			return 0, err
		}
		lease = physical.Lease
	}
	if err := os.RemoveAll(nativeCgroupScope(lease)); err != nil {
		return 0, err
	}
	return v.recoveryVMMFixture.DestroyWithExport(ctx, lease, out)
}

func migrationPhysicalFixture(t *testing.T) (*Manager, state.MigrationAdmissionPlan, WakeRequest) {
	t.Helper()
	m, _, _ := nativeManagerFixture(t)
	m.vmm = &migrationPhysicalVMM{m.vmm.(*recoveryVMMFixture)}
	p := state.MigrationAdmissionPlan{ID: uuid.NewString(), InstanceID: uuid.NewString(), SourceNodeID: uuid.NewString(), DestinationNodeID: uuid.NewString(), SourceWakeID: uuid.NewString(), DestinationWakeID: uuid.NewString(), DestinationGeneration: 1, LeaseToken: uuid.NewString(), MemStorageKey: "migration/mem", VMStateStorageKey: "migration/state", RequestDigest: strings.Repeat("a", 64), RAMMB: 256 + api.PerVMOverheadMB, CPUMillicores: 1000, VCPU: 2, ExpiresAt: time.Now().Add(time.Minute).UTC().Truncate(time.Microsecond)}
	m.nativeQualificationNodeID = p.DestinationNodeID
	r := restoreWakeRequest(p.InstanceID)
	return m, p, r
}

func TestMigrationAdoptionCleanupClosesDelayedCreation(t *testing.T) {
	m, p, req := migrationPhysicalFixture(t)
	proof, err := m.RetireMigrationAdmission(t.Context(), p)
	if err != nil || proof == "" {
		t.Fatalf("unstarted retirement: %q %v", proof, err)
	}
	if _, err := m.AdoptMigrationAdmission(t.Context(), p, req); err == nil {
		t.Fatal("delayed adoption followed cleanup acknowledgement")
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("retired admission allocated resources")
	}
}

func TestMigrationAdoptionCleanupAndReceiptPreserveReplacement(t *testing.T) {
	m, p, req := migrationPhysicalFixture(t)
	if _, err := m.AdoptMigrationAdmission(t.Context(), p, req); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AdoptMigrationAdmission(t.Context(), p, req); !errors.Is(err, state.ErrMigrationAdoptionUnresolved) {
		t.Fatalf("started adoption replayed: %v", err)
	}
	proof, err := m.RetireMigrationAdmission(t.Context(), p)
	if err != nil || proof == "" {
		t.Fatalf("cleanup: %q %v", proof, err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("cleanup acknowledged retained resources")
	}
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	again, err := m.RetireMigrationAdmission(t.Context(), p)
	if err != nil || again != proof || m.LiveCount() != 1 {
		t.Fatalf("cleanup replay touched replacement: %q %v", again, err)
	}
	if err := m.Destroy(t.Context(), p.InstanceID); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationAdoptionCleanupJoinsOriginalBoot(t *testing.T) {
	m, p, req := migrationPhysicalFixture(t)
	fixture := m.vmm.(*migrationPhysicalVMM)
	command := fixture.command
	entered, release := make(chan struct{}), make(chan struct{})
	var first sync.Once
	fixture.command = func(ctx context.Context, argv []string, input []byte) ([]byte, error) {
		first.Do(func() { close(entered); <-release })
		return command(ctx, argv, input)
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	adopted := make(chan error, 1)
	go func() { _, err := m.AdoptMigrationAdmission(t.Context(), p, req); adopted <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("adoption never reached native effects")
	}
	retired := make(chan error, 1)
	go func() { _, err := m.RetireMigrationAdmission(t.Context(), p); retired <- err }()
	select {
	case err := <-retired:
		t.Fatalf("cleanup passed a live producer: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-adopted; err != nil {
		t.Fatal(err)
	}
	if err := <-retired; err != nil {
		t.Fatal(err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("joined cleanup retained resources")
	}
}

func TestMigrationAdoptionRestartRetiresOriginalNativeGeneration(t *testing.T) {
	m, p, req := migrationPhysicalFixture(t)
	if _, err := m.AdoptMigrationAdmission(t.Context(), p, req); err != nil {
		t.Fatal(err)
	}
	replacement := NewManager(&fakeRunner{}, m.vmm, Paths{}, m.fcVersion, nil, nil)
	replacement.nativeQualificationNodeID = p.DestinationNodeID
	proof, err := replacement.RetireMigrationAdmission(t.Context(), p)
	if err != nil || proof == "" {
		t.Fatalf("restart lost original cleanup authority: %q %v", proof, err)
	}
	v := m.nativeVMM()
	physical, err := v.nativeRecoveryRuntime().journal.read(p.InstanceID)
	if err != nil || !physical.Revoked || !physical.ExitConfirmed || !physical.ResourcesRemoved {
		t.Fatalf("retirement has no physical proof: %+v %v", physical, err)
	}
}

func TestMigrationAdoptionRefusesDifferentPhysicalGeneration(t *testing.T) {
	m, p, req := migrationPhysicalFixture(t)
	if _, err := m.AdoptMigrationAdmission(t.Context(), p, req); err != nil {
		t.Fatal(err)
	}
	j := m.nativeVMM().nativeRecoveryRuntime().journal
	r, err := j.read(p.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	original := r
	r.Generation = uuid.NewString()
	if err := j.write(r); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RetireMigrationAdmission(t.Context(), p); err == nil || m.LiveCount() != 1 {
		t.Fatal("cleanup reached a different physical generation", err)
	}
	if err := j.write(original); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RetireMigrationAdmission(t.Context(), p); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationAdoptionMissingPhysicalJournalRetainsOwnership(t *testing.T) {
	m, p, req := migrationPhysicalFixture(t)
	if _, err := m.AdoptMigrationAdmission(t.Context(), p, req); err != nil {
		t.Fatal(err)
	}
	j := m.nativeVMM().nativeRecoveryRuntime().journal
	physical, err := j.read(p.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(j.path(p.InstanceID)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RetireMigrationAdmission(t.Context(), p); err == nil || m.LeasedCount() != 1 {
		t.Fatal("missing journal became cleanup proof", err)
	}
	if err := j.write(physical); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RetireMigrationAdmission(t.Context(), p); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationAdoptionRejectsUnreservedResourcesBeforeEffects(t *testing.T) {
	for _, kind := range []string{"ram", "cpu", "sidecar", "vcpu"} {
		t.Run(kind, func(t *testing.T) {
			m, p, req := migrationPhysicalFixture(t)
			switch kind {
			case "ram":
				req.MemSizeMiB = p.RAMMB
			case "cpu":
				req.CPUMillicores = p.CPUMillicores + 1
			case "sidecar":
				req.Sidecars = []WorkloadSpec{{Name: "worker", RamMB: p.RAMMB}}
			case "vcpu":
				req.VcpuCount = p.VCPU + 1
			}
			if _, err := m.AdoptMigrationAdmission(t.Context(), p, req); err == nil || m.LeasedCount() != 0 {
				t.Fatal("unreserved request reached native effects", err)
			}
		})
	}
}

func TestMigrationAdoptionOrdinaryReuseRequiresOriginalRetirement(t *testing.T) {
	m, p, req := migrationPhysicalFixture(t)
	if _, err := m.AdoptMigrationAdmission(t.Context(), p, req); err != nil {
		t.Fatal(err)
	}
	// A boot which bypasses the live map still cannot borrow the incoming ID.
	j := m.nativeVMM().nativeRecoveryRuntime().journal
	if lock, err := j.lockMigrationPreparation(t.Context(), p.InstanceID); err == nil {
		if lock != nil {
			_ = lock.Close()
		}
		t.Fatal("ordinary preparation borrowed a live incoming owner")
	}
	if err := m.Destroy(t.Context(), p.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal("retired migrated VM blocked a later ordinary wake", err)
	}
	if proof, err := m.RetireMigrationAdmission(t.Context(), p); err != nil || proof == "" || m.LiveCount() != 1 {
		t.Fatal("original proof replay touched later ordinary wake", proof, err)
	}
	if err := m.Destroy(t.Context(), p.InstanceID); err != nil {
		t.Fatal(err)
	}
}
