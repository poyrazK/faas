package fcvm

import (
	"testing"
	"time"
)

// A guest's drive base survives a vmmd restart: the adoption block records it
// and the adopting vmmd puts it back, so the guest's next park uploads a delta
// rather than the whole drive (ADR-20261115213230).
func TestAdoptionCarriesDriveBase(t *testing.T) {
	f := newAdoptionFixture(t)
	vmm := NewJailerVMM(t.TempDir(), time.Second)
	m := newTestManager(&fakeRunner{}, vmm)
	m.resourceJournal = f.journal
	inst := &Instance{
		Lease: f.lease, Method: WakeColdBoot, AppID: "app-1", DeploymentID: "dep-1", Port: 8080,
		adoptionVCPUs: 2, adoptionMemSizeMiB: 512,
	}
	m.mu.Lock()
	m.live[idLive] = inst
	m.mu.Unlock()
	vmm.setDriveBaseKey(idLive, "layers/dep-1.ext4")
	w := &adoptionWriter{procDir: f.proc, now: func() time.Time { return time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC) }}
	f.must(m.commitAdoption(idLive, w))
	r := f.record()
	if r.Adoption == nil || r.Adoption.DriveBase != "layers/dep-1.ext4" {
		t.Fatalf("committed drive base = %+v", r.Adoption)
	}

	successor := NewJailerVMM(t.TempDir(), time.Second)
	next := newTestManager(&fakeRunner{}, successor)
	next.restoreAdoptedPolicy(idLive, "app-1", r.Adoption)
	if got := successor.driveBaseKey(idLive); got != "layers/dep-1.ext4" {
		t.Fatalf("adopted drive base = %q", got)
	}

	r.Adoption.DriveBase = "snap/s/v2/drive"
	if err := r.validateAdoption(); err == nil {
		t.Fatal("a snapshot drive was accepted as a drive base")
	}
}
