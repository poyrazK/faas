package fcvm

import (
	"strings"
	"testing"
	"time"
)

func driveBaseAdoptionFixture(t *testing.T, writeDriveBase bool, key string) (*adoptionFixture, resourceJournalRecord) {
	t.Helper()
	f := newAdoptionFixture(t)
	vmm := NewJailerVMM(t.TempDir(), time.Second)
	m := newTestManager(&fakeRunner{}, vmm)
	m.resourceJournal = f.journal
	m.WithAdoptionDriveBase(writeDriveBase)
	inst := &Instance{
		Lease: f.lease, Method: WakeColdBoot, AppID: "app-1", DeploymentID: "dep-1", Port: 8080,
		adoptionVCPUs: 2, adoptionMemSizeMiB: 512,
	}
	m.mu.Lock()
	m.live[idLive] = inst
	m.mu.Unlock()
	vmm.setDriveBaseKey(idLive, key)
	w := &adoptionWriter{procDir: f.proc, now: func() time.Time { return time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC) }}
	f.must(m.commitAdoption(idLive, w))
	return f, f.record()
}

// A guest's drive base survives a vmmd restart: the adoption block records it
// and the adopting vmmd puts it back, so the guest's next park uploads a delta
// rather than the whole drive (ADR-20261115213230 amendment 1).
func TestAdoptionCarriesDriveBase(t *testing.T) {
	_, r := driveBaseAdoptionFixture(t, true, "layers/dep-1.ext4")
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

// The field stays out of the block until adoption_drive_base is on (an older
// vmmd cannot open a journal holding it), and a key the block cannot carry is
// left out rather than making the guest unadoptable.
func TestAdoptionDriveBaseOptional(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write bool
		key   string
	}{
		{"switch off", false, "layers/dep-1.ext4"},
		{"key too long", true, "layers/" + strings.Repeat("k", adoptionMaxStringBytes) + ".ext4"},
		{"snapshot key", true, "snap/s/v2/drive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, r := driveBaseAdoptionFixture(t, tc.write, tc.key)
			if r.Adoption == nil {
				t.Fatal("no adoption block committed")
			}
			if r.Adoption.DriveBase != "" {
				t.Fatalf("drive base = %q, want none", r.Adoption.DriveBase)
			}
		})
	}
}
