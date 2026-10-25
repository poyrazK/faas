package fcvm

// adr: 20261017 connection survival, §2 journal and adoption (P1c)

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const bridgeTestPID = 5151

// bridgeProcess writes a bridge's /proc entry into the fixture's proc tree.
func (f *adoptionFixture) bridgeProcess(startTicks, uid int) {
	f.t.Helper()
	dir := filepath.Join(f.proc, fmt.Sprint(bridgeTestPID))
	fields := strings.Fields(strings.Repeat("0 ", 20))
	fields[0], fields[19] = "S", fmt.Sprint(startTicks)
	f.write(filepath.Join(dir, "stat"), fmt.Sprintf("%d (vmmd-stream-bri) %s\n", bridgeTestPID, strings.Join(fields, " ")))
	f.write(filepath.Join(dir, "status"), fmt.Sprintf("Name:\tvmmd-stream-bri\nUid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid))
	if _, err := os.Stat(filepath.Join(dir, "exe")); err != nil {
		f.write(filepath.Join(dir, "exe"), "bridge binary")
	}
}

func (f *adoptionFixture) bridgeUID() int {
	uid, ok := BridgeUIDForJailUID(f.lease.UID)
	if !ok {
		f.t.Fatalf("lease uid %d has no bridge uid", f.lease.UID)
	}
	return uid
}

func bridgeTestRun(unit string) InstanceBridge {
	return InstanceBridge{Unit: unit, InvocationID: "0123456789abcdef0123456789abcdef", PID: bridgeTestPID,
		Socket: "/run/faas-ibridge/" + idLive + "/http.sock", Port: 8080, AppProtocol: "http1"}
}

func bridgeTestManager(t *testing.T, f *adoptionFixture) (*Manager, *Instance, *adoptionWriter) {
	t.Helper()
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.resourceJournal = f.journal
	inst := &Instance{Lease: f.lease, Method: WakeColdBoot, AppID: "app-1", adoptionVCPUs: 2, adoptionMemSizeMiB: 512}
	m.mu.Lock()
	m.live[idLive] = inst
	m.mu.Unlock()
	w := &adoptionWriter{dirty: map[string]struct{}{}, signal: make(chan struct{}, 1), procDir: f.proc, now: time.Now}
	m.adoption.Store(w)
	return m, inst, w
}

// The bridge run joins the block: with the first block at readiness, and at
// once after it. The strict reader accepts it back.
func TestRecordInstanceBridgeJoinsTheAdoptionBlock(t *testing.T) {
	f := newAdoptionFixture(t)
	f.bridgeProcess(900, f.bridgeUID())
	m, _, w := bridgeTestManager(t, f)
	unit := "faas-ibridge-" + idLive + "-0123abcd.service"
	f.must(m.RecordInstanceBridge(idLive, bridgeTestRun(unit)))
	if r := f.record(); r.Adoption != nil {
		t.Fatalf("recording a bridge committed a block before readiness: %+v", r.Adoption)
	}
	f.must(m.commitAdoption(idLive, w))
	b := f.record().Adoption.Bridge
	if b == nil || b.Unit != unit || b.Process.PID != bridgeTestPID || b.Process.StartTicks != 900 || b.UID != f.bridgeUID() || b.Exe.Inode == 0 {
		t.Fatalf("recorded bridge = %+v", b)
	}

	next := "faas-ibridge-" + idLive + "-89abcdef.service"
	f.must(m.RecordInstanceBridge(idLive, bridgeTestRun(next)))
	if b := f.record().Adoption.Bridge; b == nil || b.Unit != next {
		t.Fatalf("ready instance's block not rewritten: %+v", b)
	}
	m.ForgetInstanceBridge(idLive, unit) // an older run: no effect
	m.FlushAdoptionRecords()
	if b := f.record().Adoption.Bridge; b == nil || b.Unit != next {
		t.Fatalf("forgetting another run dropped the record: %+v", b)
	}
	m.ForgetInstanceBridge(idLive, next)
	m.FlushAdoptionRecords()
	if b := f.record().Adoption.Bridge; b != nil {
		t.Fatalf("stopped bridge still recorded: %+v", b)
	}
}

func TestRecordInstanceBridgeRefusesWhatItCannotProve(t *testing.T) {
	f := newAdoptionFixture(t)
	f.bridgeProcess(900, 1000) // not the lease's bridge uid
	m, _, _ := bridgeTestManager(t, f)
	if err := m.RecordInstanceBridge(idLive, bridgeTestRun("faas-ibridge-"+idLive+"-0123abcd.service")); err == nil {
		t.Fatal("recorded a bridge running as another uid")
	}
	f.bridgeProcess(900, f.bridgeUID())
	if err := m.RecordInstanceBridge(idLive, bridgeTestRun("faas-ibridge-other-0123abcd.service")); err == nil {
		t.Fatal("recorded another instance's unit")
	}
	if err := m.RecordInstanceBridge("gone", bridgeTestRun("faas-ibridge-gone-0123abcd.service")); err == nil {
		t.Fatal("recorded a bridge for an instance that is not live")
	}
}

func TestAdoptionBridgeValidation(t *testing.T) {
	f := newAdoptionFixture(t)
	good := resourceAdoptionBridge{Unit: "faas-ibridge-" + idLive + "-0123abcd.service", InvocationID: "0123456789abcdef0123456789abcdef",
		Process: resourceProcessIdentity{BootID: idOther, PID: bridgeTestPID, StartTicks: 9}, Exe: resourceFileIdentity{Device: 1, Inode: 2},
		UID: f.bridgeUID(), Socket: "/run/faas-ibridge/" + idLive + "/http.sock", Port: 8080}
	if err := good.validate(f.lease); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*resourceAdoptionBridge){
		"other instance's unit": func(b *resourceAdoptionBridge) { b.Unit = "faas-ibridge-other-0123abcd.service" },
		"not a service":         func(b *resourceAdoptionBridge) { b.Unit = "faas-ibridge-" + idLive + "-0123abcd.scope" },
		"short invocation":      func(b *resourceAdoptionBridge) { b.InvocationID = "0123" },
		"upper-case invocation": func(b *resourceAdoptionBridge) { b.InvocationID = strings.ToUpper(b.InvocationID) },
		"pid 1":                 func(b *resourceAdoptionBridge) { b.Process.PID = 1 },
		"no start ticks":        func(b *resourceAdoptionBridge) { b.Process.StartTicks = 0 },
		"no exe":                func(b *resourceAdoptionBridge) { b.Exe.Inode = 0 },
		"jail uid":              func(b *resourceAdoptionBridge) { b.UID = f.lease.UID },
		"relative socket":       func(b *resourceAdoptionBridge) { b.Socket = "http.sock" },
		"unclean socket":        func(b *resourceAdoptionBridge) { b.Socket = "/run/faas-ibridge/../x.sock" },
		"no port":               func(b *resourceAdoptionBridge) { b.Port = 0 },
	} {
		b := good
		mutate(&b)
		if b.validate(f.lease) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A successor gets the record with the adopted instance and proves the
// running process against it.
func TestAdoptedInstanceBridgeProof(t *testing.T) {
	f := newAdoptionFixture(t)
	f.bridgeProcess(900, f.bridgeUID())
	m, _, w := bridgeTestManager(t, f)
	unit := "faas-ibridge-" + idLive + "-0123abcd.service"
	f.must(m.RecordInstanceBridge(idLive, bridgeTestRun(unit)))
	f.must(m.commitAdoption(idLive, w))

	successor := newTestManager(&fakeRunner{}, &fakeVMM{})
	successor.adoption.Store(&adoptionWriter{dirty: map[string]struct{}{}, signal: make(chan struct{}, 1), procDir: f.proc, now: time.Now})
	inst := successor.adoptedInstance(f.lease, f.record().Adoption)
	successor.mu.Lock()
	successor.live[idLive] = inst
	successor.mu.Unlock()
	got, ok := successor.AdoptedInstanceBridge(idLive)
	if !ok || got.Unit != unit || got.PID != bridgeTestPID || got.Netns != f.lease.Netns || got.Port != 8080 || got.AppProtocol != "http1" {
		t.Fatalf("adopted bridge = %+v, %v", got, ok)
	}
	f.must(successor.ProveInstanceBridge(idLive, bridgeTestPID))
	if successor.ProveInstanceBridge(idLive, bridgeTestPID+1) == nil {
		t.Fatal("proved another pid")
	}
	f.bridgeProcess(901, f.bridgeUID()) // the pid now names a later process
	if successor.ProveInstanceBridge(idLive, bridgeTestPID) == nil {
		t.Fatal("proved a reused pid")
	}
	f.bridgeProcess(900, f.bridgeUID()+1)
	if successor.ProveInstanceBridge(idLive, bridgeTestPID) == nil {
		t.Fatal("proved a bridge running as another uid")
	}
	f.bridgeProcess(900, f.bridgeUID())
	exe := filepath.Join(f.proc, fmt.Sprint(bridgeTestPID), "exe")
	f.must(os.Remove(exe))
	f.write(exe, "another binary")
	if successor.ProveInstanceBridge(idLive, bridgeTestPID) == nil {
		t.Fatal("proved a bridge running another executable")
	}
	if _, ok := successor.AdoptedInstanceBridge("gone"); ok || successor.ProveInstanceBridge("gone", bridgeTestPID) == nil {
		t.Fatal("an instance without a record has a bridge")
	}
}
