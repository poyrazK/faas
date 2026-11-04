package fcvm

// adr: 20261018 runtime admission re-bind, open question 1

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// measuredHandoff is a measured cold boot as its predecessor left it: a
// pinned, observed handoff, the Firecracker process holding its drives (a
// fake procfs, process 42), the receipt and the block's drive pins.
type measuredHandoff struct {
	runtimeDriveFixture
	handoff *runtimeDriveHandoff
	proc    string
	receipt runtimeadmission.Receipt
	pins    []resourceAdoptionDrivePin
}

func newMeasuredHandoff(t *testing.T) measuredHandoff {
	t.Helper()
	f := newRuntimeDriveFixture(t)
	f.pin(t)
	handoff := f.measure(t)
	proc := runtimeDriveProcFixture(t, handoff)
	start, err := observeRuntimeDriveHandles(t.Context(), proc, 42, f.lease.UID, handoff.drives)
	if err != nil {
		t.Fatal(err)
	}
	handoff.mu.Lock()
	handoff.observation.ProcessPID, handoff.observation.ProcessStart = 42, start
	handoff.observation.Drives = nil
	for _, d := range handoff.drives {
		handoff.observation.Drives = append(handoff.observation.Drives, d.observation)
	}
	observation := handoff.observation
	handoff.mu.Unlock()
	hash, err := runtimeadmission.HashArtifactSources(f.sources)
	if err != nil {
		t.Fatal(err)
	}
	receipt := adoptionTestReceipt(measuredReceiptLease(f.lease))
	receipt.Binding.ProtocolVersion, receipt.Binding.ArtifactSourcesHash = runtimeadmission.ArtifactProtocolVersion, hash
	receipt.ArtifactConsumption = runtimeConsumptionFromObservation(observation)
	pins := f.vmm.adoptionDrivePins(f.lease)
	if checkDrivePins(receipt, pins) != nil {
		t.Fatalf("predecessor pins = %+v", pins)
	}
	return measuredHandoff{runtimeDriveFixture: f, handoff: handoff, proc: proc, receipt: receipt, pins: pins}
}

// measuredReceiptLease is the fixture lease with the network identity a
// receipt names.
func measuredReceiptLease(l Lease) Lease {
	l.Netns, l.HostIP = "fc-"+l.Instance, netip.MustParseAddr("10.100.0.3")
	return l
}

// The successor reopens each drive through the descriptor Firecracker holds
// and gets the handoff the predecessor had: same inodes, same bytes, and the
// live observation a booted handoff passes.
func TestRebuildRuntimeDriveHandoffReopensTheDrivesFirecrackerHolds(t *testing.T) {
	f := newMeasuredHandoff(t)
	rebuilt, err := rebuildRuntimeDriveHandoff(t.Context(), f.proc, 42, f.lease, f.receipt, f.pins)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closePinnedDrives(rebuilt.drives) })
	if rebuilt.lease != f.lease || len(rebuilt.drives) != len(f.handoff.drives) || rebuilt.observation.ProcessPID != 42 ||
		rebuilt.observation.ProcessStart != f.receipt.ArtifactConsumption.ProcessStart || rebuilt.observation.ConfigHash != f.handoff.observation.ConfigHash {
		t.Fatalf("rebuilt handoff = %+v", rebuilt)
	}
	for i, d := range rebuilt.drives {
		was := f.handoff.drives[i]
		if !os.SameFile(d.info, was.info) || d.observation != was.observation || d.file == was.file {
			t.Fatalf("drive %d = %+v, predecessor's %+v", i, d, was)
		}
		actual, _, err := measurePinnedRuntimeDrive(t.Context(), d.file, d.observation.Injected.Bytes)
		if err != nil || actual != d.observation.Injected {
			t.Fatalf("drive %d reads %+v, %v; recorded %+v", i, actual, err, d.observation.Injected)
		}
	}
	if start, err := observeRuntimeDriveHandles(t.Context(), f.proc, 42, f.lease.UID, rebuilt.drives); err != nil || start != rebuilt.observation.ProcessStart {
		t.Fatalf("rebuilt handoff observation: start %q, %v", start, err)
	}
	if rebuilt.sources[f.config.Drives[1].DriveID] != f.sources[1] {
		t.Fatalf("sources = %+v", rebuilt.sources)
	}
}

func TestRebuildRuntimeDriveHandoffRefusesWhatDoesNotCheckOut(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(t *testing.T, f *measuredHandoff, pid *int)
	}{
		{"restored guest", func(_ *testing.T, f *measuredHandoff, _ *int) { f.receipt.Method = vmmdpb.WakeMethod_WAKE_RESTORE }},
		{"protocol 1", func(_ *testing.T, f *measuredHandoff, _ *int) {
			f.receipt.Binding.ProtocolVersion = runtimeadmission.ProtocolVersion
		}},
		{"pins out of order", func(_ *testing.T, f *measuredHandoff, _ *int) { f.pins[0], f.pins[1] = f.pins[1], f.pins[0] }},
		{"a pin missing", func(_ *testing.T, f *measuredHandoff, _ *int) { f.pins = f.pins[:1] }},
		{"another process", func(_ *testing.T, _ *measuredHandoff, pid *int) { *pid = 43 }},
		{"another lease uid", func(_ *testing.T, f *measuredHandoff, _ *int) { f.receipt.LeaseUID++ }},
		{"inode no longer held", func(t *testing.T, f *measuredHandoff, _ *int) {
			if err := os.Remove(filepath.Join(f.proc, "42/fd/4")); err != nil {
				t.Fatal(err)
			}
		}},
		{"inode replaced", func(_ *testing.T, f *measuredHandoff, _ *int) { f.pins[1].File.Inode++ }},
		{"size changed", func(t *testing.T, f *measuredHandoff, _ *int) {
			file, err := os.OpenFile(f.handoff.drives[1].file.Name(), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, err = file.WriteString("grown")
			if err = errors.Join(err, file.Close()); err != nil {
				t.Fatal(err)
			}
		}},
		{"writable drive aliases the base", func(t *testing.T, f *measuredHandoff, _ *int) {
			f.pins[1].File = f.pins[0].File
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMeasuredHandoff(t)
			pid := 42
			tc.edit(t, &f, &pid)
			if rebuilt, err := rebuildRuntimeDriveHandoff(t.Context(), f.proc, pid, f.lease, f.receipt, f.pins); err == nil {
				closePinnedDrives(rebuilt.drives)
				t.Fatal("rebuilt a handoff that does not check out")
			}
		})
	}
}

// Only an observed cold boot's handoff is recorded: a restore's also owns
// its load, which is not recorded.
func TestAdoptionDrivePinsOnlyForAnObservedColdBoot(t *testing.T) {
	f := newMeasuredHandoff(t)
	other := f.lease
	other.UID++
	if pins := f.vmm.adoptionDrivePins(other); pins != nil {
		t.Fatalf("pins for another lease = %+v", pins)
	}
	f.handoff.mu.Lock()
	f.handoff.restoreLoad = &verifiedSnapshotRestore{}
	f.handoff.mu.Unlock()
	if pins := f.vmm.adoptionDrivePins(f.lease); pins != nil {
		t.Fatalf("pins for a restored guest = %+v", pins)
	}
	f.handoff.mu.Lock()
	f.handoff.restoreLoad, f.handoff.observation.ProcessPID = nil, 0
	f.handoff.mu.Unlock()
	if pins := f.vmm.adoptionDrivePins(f.lease); pins != nil {
		t.Fatalf("pins before the observation = %+v", pins)
	}
}

// The record carries pins only for the receipt's drives, in order.
func TestAdoptionAdmissionValidatesItsDrivePins(t *testing.T) {
	f := newMeasuredHandoff(t)
	lease := measuredReceiptLease(f.lease)
	good := resourceAdoptionAdmission{Receipt: f.receipt, Plan: "pro", Drives: f.pins}
	if err := good.validate(lease); err != nil {
		t.Fatalf("measured admission: %v", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*resourceAdoptionAdmission)
	}{
		{"another drive id", func(a *resourceAdoptionAdmission) { a.Drives[0].DriveID = "other" }},
		{"no inode", func(a *resourceAdoptionAdmission) { a.Drives[1].File.Inode = 0 }},
		{"a path", func(a *resourceAdoptionAdmission) { a.Drives[0].Path = "../escape" }},
		{"one pin", func(a *resourceAdoptionAdmission) { a.Drives = a.Drives[:1] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := good
			bad.Drives = append([]resourceAdoptionDrivePin(nil), good.Drives...)
			tc.edit(&bad)
			if bad.validate(lease) == nil {
				t.Fatal("validated")
			}
		})
	}
}

// Adoption hands an admitted guest's recorded drive pins to the VMM, and a
// handoff that cannot be rebuilt leaves the guest adopted.
func TestAdoptRestartSurvivorsRebuildsAMeasuredDriveHandoff(t *testing.T) {
	for _, tc := range []struct {
		name       string
		handoffErr error
	}{{"rebuilt", nil}, {"not rebuilt", runtimeadmission.ErrStale}} {
		t.Run(tc.name, func(t *testing.T) {
			measured := newMeasuredHandoff(t)
			g := newAdoptionFixture(t)
			receipt := adoptionTestReceipt(g.lease)
			receipt.Binding.ProtocolVersion, receipt.Binding.ArtifactSourcesHash = runtimeadmission.ArtifactProtocolVersion, measured.receipt.Binding.ArtifactSourcesHash
			receipt.ArtifactConsumption = measured.receipt.ArtifactConsumption.Clone()
			b := g.block()
			b.Instance.Admitted, b.Instance.Admission = true, &resourceAdoptionAdmission{Receipt: receipt, Plan: "pro", Drives: measured.pins}
			sm, vmm := adoptionManager(t, g, b)
			sm.WithAdoptAdmittedRuntimes(true)
			vmm.handoffErr = tc.handoffErr
			rep, err := sm.adoptClassified(t.Context(), vmm, g.classifier())
			if err != nil {
				t.Fatal(err)
			}
			if rep.Adopted != 1 || len(vmm.handoffs) != 1 {
				t.Fatalf("report = %+v, handoffs %+v", rep, vmm.handoffs)
			}
			h := vmm.handoffs[0]
			if h.lease != sm.live[idLive].Lease || h.pid != g.process.PID || len(h.pins) != len(measured.pins) || h.pins[1] != measured.pins[1] {
				t.Fatalf("handoff call = %+v", h)
			}
		})
	}
	// A guest without pins (protocol 1, or a restore) asks for nothing.
	g := newAdoptionFixture(t)
	b := g.block()
	b.Instance.Admitted, b.Instance.Admission = true, &resourceAdoptionAdmission{Receipt: adoptionTestReceipt(g.lease), Plan: "pro"}
	sm, vmm := adoptionManager(t, g, b)
	sm.WithAdoptAdmittedRuntimes(true)
	if rep, err := sm.adoptClassified(t.Context(), vmm, g.classifier()); err != nil || rep.Adopted != 1 || len(vmm.handoffs) != 0 {
		t.Fatalf("report = %+v, %v, handoffs %+v", rep, err, vmm.handoffs)
	}
}

// pinningVMM hands the block writer fixed drive pins.
type pinningVMM struct {
	*fakeVMM
	pins []resourceAdoptionDrivePin
}

func (v *pinningVMM) adoptionDrivePins(Lease) []resourceAdoptionDrivePin { return v.pins }

// The block writer records the VMM's pins for a measured cold boot only,
// and only when they describe its receipt's drives.
func TestAdoptionBlockRecordsMeasuredDrivePins(t *testing.T) {
	measured := newMeasuredHandoff(t)
	for _, tc := range []struct {
		name     string
		protocol uint32
		pins     []resourceAdoptionDrivePin
		want     bool
	}{
		{"measured cold boot", runtimeadmission.ArtifactProtocolVersion, measured.pins, true},
		{"pins for other drives", runtimeadmission.ArtifactProtocolVersion, []resourceAdoptionDrivePin{measured.pins[1], measured.pins[0]}, false},
		{"protocol 1", runtimeadmission.ProtocolVersion, measured.pins, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdoptionFixture(t)
			m := newTestManager(&fakeRunner{}, &pinningVMM{fakeVMM: &fakeVMM{}, pins: tc.pins})
			m.resourceJournal = f.journal
			receipt := adoptionTestReceipt(f.lease)
			if tc.protocol == runtimeadmission.ArtifactProtocolVersion {
				receipt.Binding.ProtocolVersion, receipt.Binding.ArtifactSourcesHash = tc.protocol, measured.receipt.Binding.ArtifactSourcesHash
				receipt.ArtifactConsumption = measured.receipt.ArtifactConsumption.Clone()
			}
			inst := &Instance{Lease: f.lease, Method: WakeColdBoot, AppID: "app-1", adoptionVCPUs: 2, adoptionMemSizeMiB: 512,
				runtimeAdmissionReceipt: receipt, runtimeAdmissionEgress: WakeRequest{AppID: "app-1", Plan: "pro"}}
			m.mu.Lock()
			m.live[idLive] = inst
			m.mu.Unlock()
			w := &adoptionWriter{dirty: map[string]struct{}{}, signal: make(chan struct{}, 1), procDir: f.proc, now: time.Now}
			f.must(m.commitAdoption(idLive, w))
			adm := f.record().Adoption.Instance.Admission
			if adm == nil || (len(adm.Drives) != 0) != tc.want {
				t.Fatalf("admission = %+v, want pins %v", adm, tc.want)
			}
		})
	}
}
