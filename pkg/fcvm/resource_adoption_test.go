package fcvm

// adr: 20261017 vmmd restart adoption

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// adoptionFixture is one guest as the journal and a fake host see it: a
// journal record with process, namespace, veth and jail assets, and the
// /proc, cgroupfs and sysfs entries a real ready guest would have.
type adoptionFixture struct {
	t                          *testing.T
	root, proc, cgroup, netDir string
	journal                    *ResourceJournal
	lease                      Lease
	process                    resourceProcessIdentity
	nsFile                     resourceFileIdentity
	jailRoot                   string
	api                        firecrackerState
	apiErr                     error
	vethPaired                 bool
	privateUnpaired            bool
}

const adoptionTestBridge = "gpn-0123456789"

// attachPrivateNetwork gives the fixture guest a private-network side-link
// on adoptionTestBridge, as a Wake with an attachment leaves it, and
// returns the block's record of it.
func (f *adoptionFixture) attachPrivateNetwork() *resourceAdoptionPrivateNetwork {
	f.t.Helper()
	host, _ := privateVethNames(f.lease.Slot)
	dir := filepath.Join(f.netDir, host)
	f.write(filepath.Join(dir, "ifindex"), "23\n")
	f.write(filepath.Join(dir, "address"), "02:00:00:00:00:23\n")
	f.must(os.Symlink(filepath.Join("..", adoptionTestBridge), filepath.Join(dir, "master")))
	ctx := &resourceMountIdentity{BootID: idOther, Namespace: 4026531840}
	f.must(f.journal.addAsset(idLive, resourceAsset{Kind: "veth", Path: resourceLinkPath(host), Namespace: ctx,
		Link: &resourceLinkIdentity{Index: 23, Name: host, Kind: "veth", Address: "02:00:00:00:00:23"}}))
	return &resourceAdoptionPrivateNetwork{Bridge: adoptionTestBridge, Address: "10.250.0.5", CIDRs: []string{"10.250.0.0/24"},
		AllowedCIDRs:  []string{"10.250.0.0/25"},
		FirewallRules: []netns.PrivateNetworkFirewallRule{{Direction: "ingress", Protocol: "tcp", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.250.0.0/24")}}}}
}

const adoptionTestPID = 4242

func newAdoptionFixture(t *testing.T) *adoptionFixture { return adoptionFixtureFor(t, false) }

// adoptionFixtureFor builds the fixture guest; a networkless one is a
// disposable execution, with no namespace or veth.
func adoptionFixtureFor(t *testing.T, networkless bool) *adoptionFixture {
	t.Helper()
	root := t.TempDir()
	f := &adoptionFixture{
		t: t, root: root,
		proc: filepath.Join(root, "proc"), cgroup: filepath.Join(root, "cgroup"), netDir: filepath.Join(root, "net"),
		lease: journalTestLease(idLive, 3),
	}
	f.lease.Networkless = networkless
	f.jailRoot = filepath.Join(root, "jail", idLive, "root")
	pidDir := filepath.Join(f.proc, fmt.Sprint(adoptionTestPID))
	scope := filepath.Join(vmCgroupParent(f.lease), PerInstanceScope(idLive))
	f.write(filepath.Join(f.proc, "sys/kernel/random/boot_id"), idOther+"\n")
	fields := strings.Fields(strings.Repeat("0 ", 20))
	fields[0], fields[19] = "S", "777"
	f.write(filepath.Join(pidDir, "stat"), fmt.Sprintf("%d (firecracker) %s\n", adoptionTestPID, strings.Join(fields, " ")))
	f.write(filepath.Join(pidDir, "cmdline"), "/firecracker\x00--id\x00"+idLive+"\x00--api-sock\x00api.sock\x00")
	f.write(filepath.Join(pidDir, "status"), fmt.Sprintf("Uid:\t%d\t%d\t%d\t%d\n", f.lease.UID, f.lease.UID, f.lease.UID, f.lease.UID))
	f.write(filepath.Join(pidDir, "exe"), "firecracker binary")
	f.write(filepath.Join(pidDir, "cgroup"), "0::/"+scope+"\n")
	f.write(filepath.Join(pidDir, "ns", "net"), "netns")
	f.write(filepath.Join(f.cgroup, scope, "memory.max"), fenceMemoryMaxFor(f.lease)+"\n")
	f.write(filepath.Join(f.netDir, f.lease.VethHost, "ifindex"), "17\n")
	f.write(filepath.Join(f.netDir, f.lease.VethHost, "address"), "02:00:00:00:00:03\n")
	ns, err := statIdentity(filepath.Join(pidDir, "ns", "net"))
	if err != nil {
		t.Fatal(err)
	}
	f.nsFile = ns
	f.process, err = readResourceProcessIdentity(f.proc, adoptionTestPID)
	if err != nil {
		t.Fatal(err)
	}

	f.journal = openTestResourceJournal(t, filepath.Join(root, "journal"))
	ctx := &resourceMountIdentity{BootID: idOther, Namespace: 4026531840}
	checkpoint := &resourceMountIdentity{BootID: idOther, Namespace: 4026531840, MountID: 99}
	f.must(f.journal.begin(f.lease))
	f.must(f.journal.recordProcess(f.lease, f.process))
	f.must(f.journal.addAsset(idLive, resourceAsset{Kind: "jail", Path: filepath.Dir(f.jailRoot), Namespace: ctx}))
	f.must(f.journal.addAsset(idLive, resourceAsset{Kind: "jail", Path: f.jailRoot, Namespace: ctx}))
	if !networkless {
		f.must(f.journal.addAsset(idLive, resourceAsset{Kind: "netns", Path: filepath.Join("/run/netns", f.lease.Netns), Namespace: ctx, File: &ns, Mount: checkpoint}))
		f.must(f.journal.addAsset(idLive, resourceAsset{Kind: "veth", Path: resourceLinkPath(f.lease.VethHost), Namespace: ctx,
			Link: &resourceLinkIdentity{Index: 17, Name: f.lease.VethHost, Kind: "veth", Address: "02:00:00:00:00:03"}}))
	}
	f.api = firecrackerState{ID: idLive, State: "Running", VCPUs: 2, MemMiB: 512, MachineConfigs: true}
	f.vethPaired = true
	return f
}

func (f *adoptionFixture) write(path, body string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *adoptionFixture) must(err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
}

// block is what the writer would commit for the fixture guest.
func (f *adoptionFixture) block() resourceAdoption {
	f.t.Helper()
	exe, err := adoptionExeIdentity(f.proc, f.process)
	if err != nil {
		f.t.Fatal(err)
	}
	return resourceAdoption{
		Kind:    adoptionKindApp,
		ReadyAt: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC),
		Firecracker: resourceAdoptionFirecracker{
			Exe: exe, Version: testFCVersion, Method: WakeRestore.String(), VCPUs: 2, MemoryMiB: 512,
			MemoryMax: fenceMemoryMaxFor(f.lease), ConsoleFile: true,
		},
		Instance: resourceAdoptionInstance{
			AppID: "app-1", AccountID: "acct-1", DeploymentID: "dep-1", Port: 8080,
			WorkloadNames: []string{"web"}, LivenessProbe: json.RawMessage(`{"path":"/healthz"}`),
		},
		Lifecycle: resourceAdoptionLifecycle{State: adoptionStateReady},
	}
}

func (f *adoptionFixture) classifier() adoptionClassifier {
	f.t.Helper()
	boot, err := resourceKernelBootID(f.proc)
	f.must(err)
	return adoptionClassifier{
		procRoot: f.proc, cgroupRoot: f.cgroup, netRoot: f.netDir, bootID: boot,
		api: func(_ context.Context, socket string) (firecrackerState, error) {
			if socket != filepath.Join(f.jailRoot, APISockName) {
				return firecrackerState{}, errors.New("wrong api socket " + socket)
			}
			return f.api, f.apiErr
		},
		vethPeer: func(_ context.Context, pid int, host, peer string) (bool, error) {
			privHost, privPeer := privateVethNames(f.lease.Slot)
			switch {
			case pid != adoptionTestPID:
				return false, errors.New("wrong pid")
			case host == f.lease.VethHost && peer == f.lease.VethPeer:
				return f.vethPaired, nil
			case host == privHost && peer == privPeer:
				return !f.privateUnpaired, nil
			}
			return false, errors.New("wrong veth pair " + host + "/" + peer)
		},
	}
}

func (f *adoptionFixture) record() resourceJournalRecord {
	f.t.Helper()
	r, ok, err := f.journal.lookup(idLive)
	if err != nil || !ok {
		f.t.Fatalf("lookup: %v %v", ok, err)
	}
	return r
}

func TestAdoptionRecordRoundTrip(t *testing.T) {
	f := newAdoptionFixture(t)
	block := f.block()
	f.must(f.journal.recordAdoption(f.lease, f.process, block))
	f.must(f.journal.Close())

	reopened := openTestResourceJournal(t, filepath.Join(f.root, "journal"))
	r, ok, err := reopened.lookup(idLive)
	if err != nil || !ok {
		t.Fatalf("lookup after reopen: %v %v", ok, err)
	}
	if r.Version != resourceAdoptionVersion || r.Adoption == nil {
		t.Fatalf("reopened record version %d adoption %v", r.Version, r.Adoption)
	}
	got, _ := json.Marshal(r.Adoption)
	want, _ := json.Marshal(block)
	if string(got) != string(want) {
		t.Fatalf("adoption block changed across reopen:\n got %s\nwant %s", got, want)
	}
	// A version-7 record is still quarantined like any other: provenance,
	// not ownership.
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	if err := m.WithResourceJournal(reopened); err != nil {
		t.Fatal(err)
	}
	opts := restartInventoryOptions{procRoot: f.proc, netRoot: f.netDir, netnsRoot: filepath.Join(f.root, "netns"), jailRoot: filepath.Join(f.root, "jail")}
	if _, err := m.recoverRestartQuarantine(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if m.HasInstanceOwnership(idLive) || m.LiveCount() != 0 {
		t.Fatal("a version-7 record became lifecycle ownership")
	}
}

func TestAdoptionRecordRejectsInvalidBlocks(t *testing.T) {
	f := newAdoptionFixture(t)
	base := f.record()
	tests := []struct {
		name   string
		mutate func(*resourceJournalRecord)
	}{
		{"block on version 6", func(r *resourceJournalRecord) { r.Version = 6 }},
		{"version 8", func(r *resourceJournalRecord) { r.Version = 8 }},
		{"unknown kind", func(r *resourceJournalRecord) { r.Adoption.Kind = "database" }},
		{"no ready time", func(r *resourceJournalRecord) { r.Adoption.ReadyAt = time.Time{} }},
		{"no exe inode", func(r *resourceJournalRecord) { r.Adoption.Firecracker.Exe.Inode = 0 }},
		{"bad method", func(r *resourceJournalRecord) { r.Adoption.Firecracker.Method = "teleport" }},
		{"no vcpus", func(r *resourceJournalRecord) { r.Adoption.Firecracker.VCPUs = 0 }},
		{"bad memory.max", func(r *resourceJournalRecord) { r.Adoption.Firecracker.MemoryMax = "max" }},
		{"control byte", func(r *resourceJournalRecord) { r.Adoption.Instance.AppID = "app\n1" }},
		{"empty workload", func(r *resourceJournalRecord) { r.Adoption.Instance.WorkloadNames = []string{""} }},
		{"probe not json", func(r *resourceJournalRecord) { r.Adoption.Instance.LivenessProbe = json.RawMessage(`{`) }},
		{"probe too large", func(r *resourceJournalRecord) {
			r.Adoption.Instance.ReadinessProbe = json.RawMessage(`"` + strings.Repeat("x", adoptionMaxProbeBytes) + `"`)
		}},
		{"frozen without time", func(r *resourceJournalRecord) { r.Adoption.Lifecycle.State = adoptionStateFrozen }},
		{"ready with freeze time", func(r *resourceJournalRecord) {
			now := time.Now()
			r.Adoption.Lifecycle.FrozenSince = &now
		}},
		{"unknown state", func(r *resourceJournalRecord) { r.Adoption.Lifecycle.State = "sleeping" }},
		{"unknown op", func(r *resourceJournalRecord) {
			r.Adoption.Lifecycle.Op = &resourceAdoptionOp{Kind: "resize", Token: "t", Started: time.Now()}
		}},
		{"op without token", func(r *resourceJournalRecord) {
			r.Adoption.Lifecycle.Op = &resourceAdoptionOp{Kind: adoptionOpPark, Started: time.Now()}
		}},
		{"op without start", func(r *resourceJournalRecord) {
			r.Adoption.Lifecycle.Op = &resourceAdoptionOp{Kind: adoptionOpPark, Token: "t"}
		}},
		{"op empty artifact", func(r *resourceJournalRecord) {
			r.Adoption.Lifecycle.Op = &resourceAdoptionOp{Kind: adoptionOpPark, Token: "t", Started: time.Now(), Artifacts: []string{""}}
		}},
		{"no process", func(r *resourceJournalRecord) { r.Process = nil }},
		{"bad allowlist entry", func(r *resourceJournalRecord) { r.Adoption.Network.EgressAllowlist = []string{"10.0.0.0/33"} }},
		{"bad static egress ip", func(r *resourceJournalRecord) { r.Adoption.Network.StaticEgressIP = "not-an-ip" }},
		{"incomplete backing", func(r *resourceJournalRecord) {
			r.Adoption.Backing = &BackingIdentity{Version: backingIdentityVersion, Kernel: "k"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := base
			r.Version = resourceAdoptionVersion
			block := f.block()
			r.Adoption = &block
			if err := r.validate(); err != nil {
				t.Fatalf("valid base rejected: %v", err)
			}
			r.Adoption = r.Adoption.clone()
			tt.mutate(&r)
			if err := r.validate(); err == nil {
				t.Fatal("invalid adoption block accepted")
			}
		})
	}
}

// The reader is strict about fields too: a block written by a later release
// with an extra field is refused, like every other journal record (ADR-473).
func TestAdoptionRecordRejectsUnknownFields(t *testing.T) {
	f := newAdoptionFixture(t)
	f.must(f.journal.recordAdoption(f.lease, f.process, f.block()))
	f.must(f.journal.Close())
	dir := filepath.Join(f.root, "journal")
	path := filepath.Join(dir, resourceRecordName(idLive))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(raw), `"lifecycle":{`, `"lifecycle":{"epoch_hint":1,`, 1)
	if patched == string(raw) {
		t.Fatal("fixture record has no lifecycle group")
	}
	if err := os.WriteFile(path, []byte(patched), 0o600); err != nil {
		t.Fatal(err)
	}
	if j, err := OpenResourceJournal(dir); err == nil {
		_ = j.Close()
		t.Fatal("journal with an unknown adoption field opened")
	}
}

func TestRecordAdoptionRefusesAStaleGuest(t *testing.T) {
	f := newAdoptionFixture(t)
	block := f.block()
	other := f.process
	other.StartTicks++
	if err := f.journal.recordAdoption(f.lease, other, block); !errors.Is(err, errAdoptionRecordStale) {
		t.Fatalf("relaunched process: %v", err)
	}
	moved := f.lease
	moved.MemoryMaxMiB++
	if err := f.journal.recordAdoption(moved, f.process, block); !errors.Is(err, errAdoptionRecordStale) {
		t.Fatalf("different lease: %v", err)
	}
	f.must(f.journal.forget(f.lease))
	if err := f.journal.recordAdoption(f.lease, f.process, block); !errors.Is(err, errAdoptionRecordStale) {
		t.Fatalf("retired record: %v", err)
	}
}

func TestAdoptionExeIdentityRefusesAReusedPID(t *testing.T) {
	f := newAdoptionFixture(t)
	if _, err := adoptionExeIdentity(f.proc, f.process); err != nil {
		t.Fatal(err)
	}
	reused := f.process
	reused.StartTicks = 1
	if _, err := adoptionExeIdentity(f.proc, reused); !errors.Is(err, errAdoptionRecordStale) {
		t.Fatalf("reused pid: %v", err)
	}
	gone := f.process
	gone.PID = adoptionTestPID + 1
	if _, err := adoptionExeIdentity(f.proc, gone); !errors.Is(err, errAdoptionRecordStale) {
		t.Fatalf("exited pid: %v", err)
	}
}

func TestAdoptionWriterCommitsTheLiveInstance(t *testing.T) {
	f := newAdoptionFixture(t)
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.resourceJournal = f.journal
	frozenAt := time.Date(2026, 10, 17, 13, 0, 0, 0, time.UTC)
	inst := &Instance{
		Lease: f.lease, Method: WakeRestore, AppID: "app-1", DeploymentID: "dep-1", Port: 8080,
		QuietPauseAfter: 5 * time.Second, adoptionVCPUs: 2, adoptionMemSizeMiB: 512,
	}
	m.mu.Lock()
	m.live[idLive] = inst
	m.mu.Unlock()
	m.perAppAllowlist = map[string][]netip.Prefix{"app-1": {netip.MustParsePrefix("198.51.100.0/24")}}
	m.instanceBacking = map[string]BackingIdentity{idLive: {Version: backingIdentityVersion, Kernel: "k", Base: "b", Timer: "t"}}
	w := &adoptionWriter{procDir: f.proc, now: func() time.Time { return time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC) }}
	f.must(m.commitAdoption(idLive, w))
	r := f.record()
	if got := r.Adoption.Network.EgressAllowlist; len(got) != 1 || got[0] != "198.51.100.0/24" || r.Adoption.Backing == nil || r.Adoption.Backing.Kernel != "k" {
		t.Fatalf("block policy = %+v backing %+v", r.Adoption.Network, r.Adoption.Backing)
	}
	if r.Adoption.Instance.Admitted {
		t.Fatal("a guest without a receipt recorded as admitted")
	}
	if r.Version != resourceAdoptionVersion || r.Adoption == nil || r.Adoption.Kind != adoptionKindApp ||
		r.Adoption.Instance.QuietPauseAfterMs != 5000 || r.Adoption.Lifecycle.State != adoptionStateReady ||
		r.Adoption.Firecracker.Exe.Inode == 0 || r.Adoption.Firecracker.Method != "restore" {
		t.Fatalf("committed block = %+v", r.Adoption)
	}
	firstReady := r.Adoption.ReadyAt

	// Readiness commits while the admission is in flight: the block marks
	// the guest admitted without a receipt, which a successor refuses.
	m.mu.Lock()
	m.runtimeAdmissionFlights = map[string]*runtimeAdmissionFlight{idLive: {}}
	m.mu.Unlock()
	f.must(m.commitAdoption(idLive, w))
	r = f.record()
	if !r.Adoption.Instance.Admitted || r.Adoption.Instance.Admission != nil {
		t.Fatalf("in-flight admission block = admitted %v admission %+v", r.Adoption.Instance.Admitted, r.Adoption.Instance.Admission)
	}
	if reason, _ := adoptionScope(nil, true, r); reason != adoptionRefusedAdmitted {
		t.Fatalf("a successor adopts a guest whose receipt was never recorded: %q", reason)
	}
	m.mu.Lock()
	m.runtimeAdmissionFlights = nil
	m.mu.Unlock()

	// A freeze rewrites the block and keeps the first readiness time. An
	// admitted guest records that it holds a receipt.
	m.mu.Lock()
	inst.Frozen, inst.FrozenSince = true, frozenAt
	inst.runtimeAdmissionReceipt = adoptionTestReceipt(f.lease)
	inst.runtimeAdmissionEgress = WakeRequest{AppID: "app-1", Plan: "pro"}
	m.mu.Unlock()
	w.now = func() time.Time { return frozenAt.Add(time.Hour) }
	f.must(m.commitAdoption(idLive, w))
	r = f.record()
	if r.Adoption.Lifecycle.State != adoptionStateFrozen || !r.Adoption.Lifecycle.FrozenSince.Equal(frozenAt) || !r.Adoption.ReadyAt.Equal(firstReady) || !r.Adoption.Instance.Admitted || r.Adoption.Instance.Admission == nil {
		t.Fatalf("frozen block = %+v", r.Adoption.Lifecycle)
	}

	// An instance that left the live map is skipped.
	m.mu.Lock()
	delete(m.live, idLive)
	m.mu.Unlock()
	if err := m.commitAdoption(idLive, w); !errors.Is(err, errAdoptionRecordStale) {
		t.Fatalf("commit for a gone instance: %v", err)
	}
}

func TestEnableAdoptionRecordsNeedsTheJournal(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	if err := m.EnableAdoptionRecords(t.Context()); err == nil {
		t.Fatal("adoption records enabled without a resource journal")
	}
	// Without the switch, marks are free and write nothing.
	m.markAdoptionDirty(idLive)
}

func TestAdoptionKind(t *testing.T) {
	for want, inst := range map[string]*Instance{
		adoptionKindApp:           {},
		adoptionKindJob:           {IsJob: true},
		adoptionKindAppTask:       {AppTaskOnly: true},
		adoptionKindExecution:     {ExecutionOnly: true},
		adoptionKindQualification: {IsJob: true, QualificationOnly: true},
	} {
		if got := adoptionKind(inst); got != want {
			t.Errorf("adoptionKind(%+v) = %q, want %q", inst, got, want)
		}
	}
	// adr: 20261027090010 — an agent run is killed at adoption, like an
	// execution.
	if got := adoptionKind(&Instance{AgentRunOnly: true}); got != adoptionKindExecution {
		t.Errorf("agent run adoption kind = %q, want %q", got, adoptionKindExecution)
	}
}

func TestAdoptionClassifier(t *testing.T) {
	tests := []struct {
		name         string
		noBlock      bool
		mutate       func(*adoptionFixture, *resourceAdoption)
		wantReason   string
		wantRepair   bool
		wantResolved string
	}{
		{name: "adoptable"},
		{name: "freeze in flight resumes", mutate: func(f *adoptionFixture, b *resourceAdoption) {
			f.api.State = "Paused"
			b.Lifecycle.Op = &resourceAdoptionOp{Kind: adoptionOpFreeze, Token: "t", Started: time.Now()}
		}, wantResolved: AdoptionResolvedThawed},
		{name: "private-network member", mutate: func(f *adoptionFixture, b *resourceAdoption) {
			b.Network.Private, b.Network.PrivateNetworkHandleV4 = f.attachPrivateNetwork(), 9
		}},
		{name: "private side-link left its bridge", mutate: func(f *adoptionFixture, b *resourceAdoption) {
			b.Network.Private = f.attachPrivateNetwork()
			b.Network.Private.Bridge = "gpn-9999999999"
		}, wantReason: adoptionRefusedPrivateLink},
		{name: "private side-link not in the guest", mutate: func(f *adoptionFixture, b *resourceAdoption) {
			b.Network.Private, f.privateUnpaired = f.attachPrivateNetwork(), true
		}, wantReason: adoptionRefusedPrivateLink},
		{name: "private side-link the block does not describe", mutate: func(f *adoptionFixture, _ *resourceAdoption) {
			f.attachPrivateNetwork()
		}, wantReason: adoptionRefusedPrivateLink},
		{name: "private side-link unrecorded", mutate: func(_ *adoptionFixture, b *resourceAdoption) {
			b.Network.Private = &resourceAdoptionPrivateNetwork{Bridge: adoptionTestBridge, Address: "10.250.0.5", CIDRs: []string{"10.250.0.0/24"}}
		}, wantReason: adoptionRefusedPrivateLink},
		{name: "stop in flight finishes", mutate: func(_ *adoptionFixture, b *resourceAdoption) {
			b.Lifecycle.Op = &resourceAdoptionOp{Kind: adoptionOpStop, Token: "t", Started: time.Now()}
		}, wantResolved: AdoptionResolvedStopped},
		{name: "thaw in flight resumes", mutate: func(_ *adoptionFixture, b *resourceAdoption) {
			since := time.Now()
			b.Lifecycle = resourceAdoptionLifecycle{State: adoptionStateFrozen, FrozenSince: &since,
				Op: &resourceAdoptionOp{Kind: adoptionOpThaw, Token: "t", Started: time.Now()}}
		}, wantResolved: AdoptionResolvedThawed},
		{name: "no adoption block", noBlock: true, wantReason: adoptionRefusedNoRecord},
		{name: "job", mutate: func(_ *adoptionFixture, b *resourceAdoption) {
			b.Kind, b.Instance.Job = adoptionKindJob, &resourceAdoptionJob{StartGate: true}
		}},
		{name: "app task is killed", mutate: func(_ *adoptionFixture, b *resourceAdoption) { b.Kind = adoptionKindAppTask }, wantResolved: AdoptionResolvedKilled},
		{name: "execution is killed, whatever was in flight", mutate: func(_ *adoptionFixture, b *resourceAdoption) {
			b.Kind = adoptionKindExecution
			b.Lifecycle.Op = &resourceAdoptionOp{Kind: adoptionOpFreeze, Token: "t", Started: time.Now()}
		}, wantResolved: AdoptionResolvedKilled},
		{name: "builder is killed", mutate: func(_ *adoptionFixture, b *resourceAdoption) { b.Kind = adoptionKindBuilder }, wantResolved: AdoptionResolvedKilled},
		{name: "qualification kind", mutate: func(_ *adoptionFixture, b *resourceAdoption) { b.Kind = adoptionKindQualification }, wantReason: adoptionRefusedKind},
		{name: "interrupted teardown", mutate: func(_ *adoptionFixture, b *resourceAdoption) { b.Lifecycle.State = adoptionStateStopping }, wantReason: adoptionRefusedStopping},
		{name: "pipe console", mutate: func(_ *adoptionFixture, b *resourceAdoption) { b.Firecracker.ConsoleFile = false }, wantReason: adoptionRefusedConsolePipe},
		{name: "park in flight finishes the teardown", mutate: func(_ *adoptionFixture, b *resourceAdoption) {
			b.Lifecycle.Op = &resourceAdoptionOp{Kind: adoptionOpPark, Token: "t", Started: time.Now(), Artifacts: []string{"snapshots/a/mem"}}
		}, wantResolved: AdoptionResolvedStopped},
		{name: "warm snapshot in flight finishes the teardown", mutate: func(_ *adoptionFixture, b *resourceAdoption) {
			b.Lifecycle.Op = &resourceAdoptionOp{Kind: adoptionOpWarmSnapshot, Token: "t", Started: time.Now()}
		}, wantResolved: AdoptionResolvedStopped},
		{name: "unanswered migration prepare resumes", mutate: func(f *adoptionFixture, b *resourceAdoption) {
			f.api.State = "Paused"
			b.Lifecycle.Op = &resourceAdoptionOp{Kind: adoptionOpSnapshot, Token: "t", Started: time.Now()}
		}, wantResolved: AdoptionResolvedResumed},
		{name: "answered migration prepare stays paused", mutate: func(f *adoptionFixture, b *resourceAdoption) {
			f.api.State = "Paused"
			b.Instance.MigrationPaused = true
			b.Lifecycle.Op = &resourceAdoptionOp{Kind: adoptionOpSnapshot, Token: "t", Started: time.Now()}
		}},
		{name: "other boot", mutate: func(f *adoptionFixture, _ *resourceAdoption) {
			f.write(filepath.Join(f.proc, "sys/kernel/random/boot_id"), idDead+"\n")
		}, wantReason: adoptionRefusedOtherBoot},
		{name: "process exited", mutate: func(f *adoptionFixture, _ *resourceAdoption) {
			f.must(os.RemoveAll(filepath.Join(f.proc, fmt.Sprint(adoptionTestPID))))
		}, wantReason: adoptionRefusedProcessAbsent},
		{name: "pid reused", mutate: func(f *adoptionFixture, _ *resourceAdoption) {
			fields := strings.Fields(strings.Repeat("0 ", 20))
			fields[0], fields[19] = "S", "778"
			f.write(filepath.Join(f.proc, fmt.Sprint(adoptionTestPID), "stat"), fmt.Sprintf("%d (firecracker) %s\n", adoptionTestPID, strings.Join(fields, " ")))
		}, wantReason: adoptionRefusedProcessReused},
		{name: "another instance's process", mutate: func(f *adoptionFixture, _ *resourceAdoption) {
			f.write(filepath.Join(f.proc, fmt.Sprint(adoptionTestPID), "cmdline"), "/firecracker\x00--id\x00"+idOther+"\x00")
		}, wantReason: adoptionRefusedArgv},
		{name: "jailer not yet exec'd", mutate: func(f *adoptionFixture, _ *resourceAdoption) {
			f.write(filepath.Join(f.proc, fmt.Sprint(adoptionTestPID), "cmdline"), "/jailer\x00--id\x00"+idLive+"\x00--exec-file\x00/firecracker\x00")
		}, wantReason: adoptionRefusedArgv},
		{name: "uid changed", mutate: func(f *adoptionFixture, _ *resourceAdoption) {
			f.write(filepath.Join(f.proc, fmt.Sprint(adoptionTestPID), "status"), "Uid:\t0\t0\t0\t0\n")
		}, wantReason: adoptionRefusedUID},
		{name: "other executable", mutate: func(_ *adoptionFixture, b *resourceAdoption) { b.Firecracker.Exe.Inode++ }, wantReason: adoptionRefusedExe},
		{name: "wrong cgroup", mutate: func(f *adoptionFixture, _ *resourceAdoption) {
			f.write(filepath.Join(f.proc, fmt.Sprint(adoptionTestPID), "cgroup"), "0::/faas.slice/faas-cp.slice/faas-vmmd.service\n")
		}, wantReason: adoptionRefusedCgroup},
		{name: "fence drifted", mutate: func(f *adoptionFixture, _ *resourceAdoption) {
			f.write(filepath.Join(f.cgroup, vmCgroupParent(f.lease), PerInstanceScope(idLive), "memory.max"), "max\n")
		}, wantRepair: true},
		{name: "other namespace", mutate: func(f *adoptionFixture, _ *resourceAdoption) {
			// Create the replacement while the original still exists, so it
			// cannot reuse the original's inode (Linux filesystems reuse a
			// freed inode at once).
			path := filepath.Join(f.proc, fmt.Sprint(adoptionTestPID), "ns", "net")
			f.write(path+".other", "another netns")
			f.must(os.Rename(path+".other", path))
		}, wantReason: adoptionRefusedNetns},
		{name: "veth missing", mutate: func(f *adoptionFixture, _ *resourceAdoption) {
			f.must(os.RemoveAll(filepath.Join(f.netDir, f.lease.VethHost)))
		}, wantReason: adoptionRefusedVeth},
		{name: "veth replaced", mutate: func(f *adoptionFixture, _ *resourceAdoption) {
			f.write(filepath.Join(f.netDir, f.lease.VethHost, "ifindex"), "18\n")
		}, wantReason: adoptionRefusedVeth},
		{name: "veth not paired in the guest's namespace", mutate: func(f *adoptionFixture, _ *resourceAdoption) { f.vethPaired = false }, wantReason: adoptionRefusedVeth},
		{name: "api silent", mutate: func(f *adoptionFixture, _ *resourceAdoption) { f.api = firecrackerState{} }, wantReason: adoptionRefusedAPIIdentity},
		{name: "api names another vm", mutate: func(f *adoptionFixture, _ *resourceAdoption) { f.api.ID = idOther }, wantReason: adoptionRefusedAPIIdentity},
		{name: "vm not started", mutate: func(f *adoptionFixture, _ *resourceAdoption) { f.api.State = "Not started" }, wantReason: adoptionRefusedAPIIdentity},
		{name: "quiet paused is adoptable", mutate: func(f *adoptionFixture, _ *resourceAdoption) { f.api.State = "Paused" }},
		{name: "machine config differs", mutate: func(f *adoptionFixture, _ *resourceAdoption) { f.api.VCPUs = 4 }, wantReason: adoptionRefusedMachineConfig},
		{name: "machine config unanswered", mutate: func(f *adoptionFixture, _ *resourceAdoption) {
			f.api = firecrackerState{ID: idLive, State: "Running"}
			f.apiErr = errMachineConfigUnavailable
		}, wantReason: adoptionRefusedMachineConfig},
		{name: "api unreachable", mutate: func(f *adoptionFixture, _ *resourceAdoption) {
			f.apiErr = errors.New("connection refused")
		}, wantReason: adoptionRefusedAPIUnreachable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAdoptionFixture(t)
			block := f.block()
			if tt.mutate != nil {
				tt.mutate(f, &block)
			}
			if !tt.noBlock {
				// Write through validation, as the real writer does.
				r := f.record()
				r.Version, r.Adoption = resourceAdoptionVersion, &block
				f.must(r.validate())
				f.journal.mu.Lock()
				err := f.journal.persist(r, true)
				f.journal.mu.Unlock()
				f.must(err)
			}
			v := f.classifier().classify(t.Context(), f.record())
			if tt.wantReason == "" {
				if !v.Adoptable || v.FenceRepair != tt.wantRepair || v.Resolved != tt.wantResolved {
					t.Fatalf("verdict = %+v, want adoptable (repair %v, resolved %q)", v, tt.wantRepair, tt.wantResolved)
				}
				return
			}
			if v.Adoptable || v.Reason != tt.wantReason {
				t.Fatalf("verdict = %+v, want refused %q", v, tt.wantReason)
			}
		})
	}
}

func TestAdoptionDryRunReport(t *testing.T) {
	f := newAdoptionFixture(t)
	f.must(f.journal.recordAdoption(f.lease, f.process, f.block()))
	// A second guest that never reached readiness has no block.
	pending := journalTestLease(idOther, 5)
	f.must(f.journal.begin(pending))
	rep, err := f.classifier().run(t.Context(), f.journal)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Adoptable != 1 || rep.Refused[adoptionRefusedNoRecord] != 1 || len(rep.Verdicts) != 2 {
		t.Fatalf("dry run = %+v", rep)
	}
	// The dry run changes nothing.
	if r := f.record(); r.Adoption == nil || r.Version != resourceAdoptionVersion {
		t.Fatal("dry run rewrote the record")
	}
}

func TestAdoptionOperationIntent(t *testing.T) {
	f := newAdoptionFixture(t)
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.resourceJournal = f.journal
	inst := &Instance{Lease: f.lease, Method: WakeColdBoot, adoptionVCPUs: 2, adoptionMemSizeMiB: 512}
	m.mu.Lock()
	m.live[idLive] = inst
	m.mu.Unlock()
	// Without the writer, intents are free and write nothing.
	m.beginAdoptionOp(idLive, adoptionOpPark)()
	if r := f.record(); r.Adoption != nil {
		t.Fatal("intent written while adoption records are off")
	}

	w := &adoptionWriter{dirty: map[string]struct{}{}, signal: make(chan struct{}, 1), procDir: f.proc, now: time.Now}
	m.adoption.Store(w)
	end := m.beginAdoptionOp(idLive, adoptionOpPark, "snapshots/x/mem", "")
	r := f.record()
	op := r.Adoption.Lifecycle.Op
	if op == nil || op.Kind != adoptionOpPark || op.Token == "" || len(op.Artifacts) != 1 || op.Artifacts[0] != "snapshots/x/mem" {
		t.Fatalf("intent not committed synchronously: %+v", r.Adoption)
	}
	// A park intent resolves (slice 3) instead of refusing; this guest is
	// still refused for its pipe console, which the fake VMM implies.
	if v := f.classifier().classify(t.Context(), r); v.Reason == adoptionRefusedOperation {
		t.Fatalf("in-flight park classified %+v", v)
	}
	end()
	if _, dirty := w.dirty[idLive]; !dirty {
		t.Fatal("ending the intent did not schedule a rewrite")
	}
	f.must(m.commitAdoption(idLive, w))
	if r := f.record(); r.Adoption.Lifecycle.Op != nil {
		t.Fatalf("intent survived its operation: %+v", r.Adoption.Lifecycle.Op)
	}
	// A late end of an older intent never clears a newer one.
	first := m.beginAdoptionOp(idLive, adoptionOpFreeze)
	second := m.beginAdoptionOp(idLive, adoptionOpThaw)
	first()
	m.mu.Lock()
	kind := inst.adoptionOp.Kind
	m.mu.Unlock()
	if kind != adoptionOpThaw {
		t.Fatalf("older intent cleared the newer one: %s", kind)
	}
	second()
}

func TestAllocatorAdoptQuarantined(t *testing.T) {
	a := NewAllocator()
	a.quarantine(map[int]struct{}{4: {}, 6: {}})
	lease, err := a.adoptQuarantined(idLive, 4)
	if err != nil || lease.Slot != 4 || lease.UID != JailUIDBase+4 || lease.Netns != "fc-"+idLive {
		t.Fatalf("adopt = %+v, %v", lease, err)
	}
	for name, try := range map[string]func() error{
		"free slot":         func() error { _, err := a.adoptQuarantined(idOther, 5); return err },
		"slot leased":       func() error { _, err := a.adoptQuarantined(idOther, 4); return err },
		"instance leased":   func() error { _, err := a.adoptQuarantined(idLive, 6); return err },
		"slot out of range": func() error { _, err := a.adoptQuarantined(idOther, MaxSlots); return err },
	} {
		if try() == nil {
			t.Errorf("%s: adopted", name)
		}
	}
	// A fresh lease never receives an adopted or quarantined slot.
	for i := 0; i < 8; i++ {
		l, err := a.Acquire(fmt.Sprintf("fresh-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if l.Slot == 4 || l.Slot == 6 {
			t.Fatalf("fresh lease got held slot %d", l.Slot)
		}
	}
	// Release after the adopted guest's teardown frees the slot again.
	if err := a.Release(idLive); err != nil {
		t.Fatal(err)
	}
	if _, err := a.adoptQuarantined(idOther, 4); err == nil {
		t.Fatal("released slot adopted as if quarantined")
	}
}

// A paused warm restore is recorded as an unclaimed spare until its claim.
func TestAdoptionBlockRecordsTheWarmSpare(t *testing.T) {
	f := newAdoptionFixture(t)
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.resourceJournal = f.journal
	inst := &Instance{Lease: f.lease, Method: WakeRestore, AppID: "app-1", Paused: true, warmSpare: true, adoptionVCPUs: 2, adoptionMemSizeMiB: 512}
	m.mu.Lock()
	m.live[idLive] = inst
	m.mu.Unlock()
	w := &adoptionWriter{dirty: map[string]struct{}{}, signal: make(chan struct{}, 1), procDir: f.proc, now: time.Now}
	f.must(m.commitAdoption(idLive, w))
	if r := f.record(); !r.Adoption.Instance.WarmPaused || !r.Adoption.Instance.WarmSpare {
		t.Fatalf("spare block = %+v", r.Adoption.Instance)
	}
	m.adoption.Store(w)
	if err := m.ResumeVM(t.Context(), idLive); err != nil {
		t.Fatal(err)
	}
	f.must(m.commitAdoption(idLive, w))
	if r := f.record(); !r.Adoption.Instance.WarmPaused || r.Adoption.Instance.WarmSpare {
		t.Fatalf("claimed block = %+v; want a warm restore that is no longer a spare", r.Adoption.Instance)
	}
}

// A builder boots through Wake with an export directory; its block must say
// builder, never app, or a successor would adopt it as an app.
func TestAdoptionKindRecordsBuilders(t *testing.T) {
	for _, tc := range []struct {
		inst *Instance
		want string
	}{
		{&Instance{Lease: Lease{IsBuilder: true}}, adoptionKindBuilder},
		{&Instance{}, adoptionKindApp},
		{&Instance{IsJob: true}, adoptionKindJob},
		{&Instance{AppTaskOnly: true}, adoptionKindAppTask},
		{&Instance{ExecutionOnly: true}, adoptionKindExecution},
	} {
		if got := adoptionKind(tc.inst); got != tc.want {
			t.Fatalf("adoptionKind(%+v) = %q, want %q", tc.inst.Lease, got, tc.want)
		}
	}
}

// A job's block carries its start gate, and its exit is written before the
// guest is acknowledged.
func TestRecordJobExitWritesTheBlock(t *testing.T) {
	f := newAdoptionFixture(t)
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.resourceJournal = f.journal
	inst := &Instance{Lease: f.lease, Method: WakeColdBoot, IsJob: true, JobStartGateEnabled: true, AccountID: "acct-1", adoptionVCPUs: 2, adoptionMemSizeMiB: 512}
	m.mu.Lock()
	m.live[idLive] = inst
	m.mu.Unlock()
	w := &adoptionWriter{dirty: map[string]struct{}{}, signal: make(chan struct{}, 1), procDir: f.proc, now: time.Now}
	m.adoption.Store(w)
	f.must(m.commitAdoption(idLive, w))
	r := f.record()
	if r.Adoption.Kind != adoptionKindJob || r.Adoption.Instance.Job == nil || !r.Adoption.Instance.Job.StartGate || r.Adoption.Instance.Job.Exit != nil {
		t.Fatalf("job block = %+v", r.Adoption.Instance.Job)
	}
	exit := JobExitPayload{ExitCode: 0, ErrorClass: "succeeded", FinishedAtUnixNano: 1, LeaseToken: "lease-1"}
	m.RecordJobExit(idLive, exit)
	if r := f.record(); r.Adoption.Instance.Job.Exit == nil || !reflect.DeepEqual(*r.Adoption.Instance.Job.Exit, exit) {
		t.Fatalf("recorded exit = %+v", r.Adoption.Instance.Job.Exit)
	}
	if got, err := m.WaitJobExit(t.Context(), idLive, time.Second); err != nil || !reflect.DeepEqual(got, exit) {
		t.Fatalf("WaitJobExit = %+v, %v; want the recorded exit", got, err)
	}

	bad := f.record()
	bad.Adoption.Instance.Job = nil
	if err := bad.validate(); err == nil {
		t.Fatal("a job block without its job section validated")
	}
	bad = f.record()
	bad.Adoption.Instance.Job.Exit.ErrorClass = "bogus"
	if err := bad.validate(); err == nil {
		t.Fatal("an invalid recorded exit validated")
	}
}

// A flush writes a pending mark before it returns, with no writer running.
func TestFlushAdoptionRecordsWritesPendingMarks(t *testing.T) {
	f := newAdoptionFixture(t)
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.resourceJournal = f.journal
	m.FlushAdoptionRecords() // Records off: nothing to write.
	inst := &Instance{Lease: f.lease, Method: WakeRestore, AppID: "app-1", Paused: true, warmSpare: true, adoptionVCPUs: 2, adoptionMemSizeMiB: 512}
	m.mu.Lock()
	m.live[idLive] = inst
	m.mu.Unlock()
	w := &adoptionWriter{dirty: map[string]struct{}{}, signal: make(chan struct{}, 1), procDir: f.proc, now: time.Now}
	m.adoption.Store(w)
	f.must(m.commitAdoption(idLive, w))
	m.mu.Lock()
	inst.warmSpare = false
	m.mu.Unlock()
	m.markAdoptionDirty(idLive)
	m.FlushAdoptionRecords()
	if r := f.record(); r.Adoption.Instance.WarmSpare {
		t.Fatal("flush left the pending mark unwritten")
	}
	if len(w.dirty) != 0 {
		t.Fatalf("marks left after flush: %v", w.dirty)
	}
}

// A migration source's pause is in the block before the prepare answers,
// and an owner resume clears it.
func TestSnapshotKeepAliveRecordsTheMigrationPause(t *testing.T) {
	f := newAdoptionFixture(t)
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.resourceJournal = f.journal
	inst := &Instance{Lease: f.lease, Method: WakeColdBoot, AppID: "app-1", adoptionVCPUs: 2, adoptionMemSizeMiB: 512}
	m.mu.Lock()
	m.live[idLive] = inst
	m.mu.Unlock()
	w := &adoptionWriter{dirty: map[string]struct{}{}, signal: make(chan struct{}, 1), procDir: f.proc, now: time.Now}
	m.adoption.Store(w)
	f.must(m.commitAdoption(idLive, w))
	if _, err := m.SnapshotKeepAlive(t.Context(), idLive, SnapshotSpec{}); err != nil {
		t.Fatal(err)
	}
	if r := f.record(); !r.Adoption.Instance.MigrationPaused {
		t.Fatal("migration pause not in the block when the prepare answered")
	}
	if err := m.ResumeVM(t.Context(), idLive); err != nil {
		t.Fatal(err)
	}
	f.must(m.commitAdoption(idLive, w))
	if r := f.record(); r.Adoption.Instance.MigrationPaused {
		t.Fatal("owner resume left the migration pause recorded")
	}
}

func TestMarkDevPatchedIsDurableBeforeThePatchIsServed(t *testing.T) {
	f := newAdoptionFixture(t)
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.resourceJournal = f.journal
	inst := &Instance{Lease: f.lease, Method: WakeColdBoot, AppID: "app-1", adoptionVCPUs: 2, adoptionMemSizeMiB: 512}
	m.mu.Lock()
	m.live[idLive] = inst
	m.mu.Unlock()
	if err := m.MarkDevPatched("gone"); err == nil {
		t.Fatal("marked an instance that is not live")
	}

	// Before readiness no block exists, so the mark only waits for the
	// first commit, which must carry it.
	w := &adoptionWriter{dirty: map[string]struct{}{}, signal: make(chan struct{}, 1), procDir: f.proc, now: time.Now}
	m.adoption.Store(w)
	f.must(m.MarkDevPatched(idLive))
	if r := f.record(); r.Adoption != nil {
		t.Fatalf("mark committed a block before readiness: %+v", r.Adoption)
	}
	f.must(m.commitAdoption(idLive, w))
	if r := f.record(); !r.Adoption.Instance.DevPatched {
		t.Fatal("first block lost the mark")
	}

	// After readiness the mark rewrites the committed block at once.
	m.mu.Lock()
	inst.devPatched = false
	m.mu.Unlock()
	f.must(m.commitAdoption(idLive, w))
	f.must(m.MarkDevPatched(idLive))
	r := f.record()
	if !r.Adoption.Instance.DevPatched {
		t.Fatal("mark not written synchronously into the committed block")
	}
	rep, err := f.classifier().run(t.Context(), f.journal)
	if err != nil || len(rep.Verdicts) != 1 || !rep.Verdicts[0].DevPatched {
		t.Fatalf("verdicts do not carry the mark: %+v, %v", rep.Verdicts, err)
	}
	if got := m.adoptedInstance(f.lease, r.Adoption); !got.devPatched {
		t.Fatal("adopted instance lost the mark")
	}
}

// On a filesystem without reflink the clone probe is released in full, so
// the record never names a clone that is gone: a restarted vmmd refused
// every guest on such a host ("clone ... is not the recorded file").
func TestReflinkWritableCloneRetiresTheProbeWhenReflinkFails(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "layer.ext4")
	if err := os.WriteFile(src, []byte("layer"), 0o600); err != nil {
		t.Fatal(err)
	}
	j := openTestResourceJournal(t, filepath.Join(dir, "journal"))
	lease := journalTestLease(idLive, 3)
	if err := j.begin(lease); err != nil {
		t.Fatal(err)
	}
	v := NewJailerVMM(filepath.Join(dir, "jail"), time.Second)
	v.resourceJournal = j
	path, cloned, err := v.reflinkWritableClone(src, lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	if cloned {
		t.Skipf("this filesystem reflinks (%s); the probe is the clone", path)
	}
	r, ok, err := j.lookup(lease.Instance)
	if err != nil || !ok {
		t.Fatalf("record = %v, %v", ok, err)
	}
	for _, a := range r.Assets {
		if a.Kind == "clone" {
			t.Fatalf("record keeps the released probe: %+v", a)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".faas-layer-*")); len(left) != 0 {
		t.Fatalf("probe files left: %v", left)
	}
	v.mu.Lock()
	tracked := len(v.materialisedTmp[lease.Instance]) + len(v.materialisedIdentity)
	v.mu.Unlock()
	if tracked != 0 {
		t.Fatalf("probe still tracked for teardown: %d", tracked)
	}
}

// adoptionTestReceipt is a well-formed admission receipt for lease l.
func adoptionTestReceipt(l Lease) runtimeadmission.Receipt {
	now := time.Now()
	hash := strings.Repeat("a", 64)
	b := runtimeadmission.Binding{
		ProtocolVersion: runtimeadmission.ProtocolVersion, Token: uuid.NewString(), InstanceID: l.Instance,
		AppID: uuid.NewString(), DeploymentID: uuid.NewString(), AccountID: uuid.NewString(), NodeID: uuid.NewString(), Incarnation: uuid.NewString(),
		DesiredRevision: 1, EffectiveHash: hash, CapturedInputHash: hash, PayloadHash: hash, EgressRevision: 1,
		IssuedAtUnixNano: now.Add(-time.Minute).UnixNano(), ExpiresAtUnixNano: now.Add(4 * time.Minute).UnixNano(),
	}
	return runtimeadmission.Receipt{Binding: b, NativeInputHash: hash, Netns: l.Netns, HostIP: l.HostIP.String(), LeaseUID: int32(l.UID),
		Method: vmmdpb.WakeMethod_WAKE_COLD_BOOT, CompletedAtUnixNano: now.UnixNano()}
}

// An admitted guest's block carries its receipt and egress; a successor
// with adopt_admitted_runtimes keeps the guest and holds the receipt, and
// one without refuses it, as does any successor for a block without one.
func TestAdoptRestartSurvivorsHoldsAnAdmittedReceipt(t *testing.T) {
	f := newAdoptionFixture(t)
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.resourceJournal = f.journal
	receipt := adoptionTestReceipt(f.lease)
	inst := &Instance{Lease: f.lease, Method: WakeColdBoot, AppID: "app-1", adoptionVCPUs: 2, adoptionMemSizeMiB: 512,
		runtimeAdmissionReceipt: receipt, runtimeAdmissionEgress: WakeRequest{AppID: "app-1", Plan: "pro", EgressAllowlist: []string{"10.1.0.0/16"}, EgressPorts: []uint16{443}}}
	m.mu.Lock()
	m.live[idLive] = inst
	m.mu.Unlock()
	w := &adoptionWriter{dirty: map[string]struct{}{}, signal: make(chan struct{}, 1), procDir: f.proc, now: time.Now}
	f.must(m.commitAdoption(idLive, w))
	block := *f.record().Adoption
	if adm := block.Instance.Admission; !block.Instance.Admitted || adm == nil || !adm.Receipt.Equal(receipt) || adm.Plan != "pro" || len(adm.EgressPorts) != 1 {
		t.Fatalf("admission block = %+v", block.Instance.Admission)
	}
	bad := f.record()
	bad.Adoption.Instance.Admission.Receipt.Netns = "fc-another"
	if bad.validate() == nil {
		t.Fatal("an admission for another lease validated")
	}

	for _, tc := range []struct {
		name  string
		on    bool
		strip bool
		want  string
	}{{"flag off", false, false, adoptionRefusedAdmitted}, {"no receipt in the block", true, true, adoptionRefusedAdmitted}, {"held", true, false, ""}} {
		t.Run(tc.name, func(t *testing.T) {
			g := newAdoptionFixture(t)
			b := g.block()
			b.Instance.Admitted, b.Instance.Admission = true, &resourceAdoptionAdmission{Receipt: adoptionTestReceipt(g.lease), Plan: "pro"}
			if tc.strip {
				b.Instance.Admission = nil
			}
			sm, vmm := adoptionManager(t, g, b)
			sm.WithAdoptAdmittedRuntimes(tc.on)
			rep, err := sm.adoptClassified(t.Context(), vmm, g.classifier())
			if err != nil {
				t.Fatal(err)
			}
			if tc.want != "" {
				if rep.Refused[tc.want] != 1 {
					t.Fatalf("report = %+v, want refused %s", rep, tc.want)
				}
				return
			}
			held := sm.live[idLive]
			v := rep.Verdicts[0]
			if rep.Adopted != 1 || held == nil || held.admissionHeldFrom != b.Instance.Admission.Receipt.Binding.Incarnation ||
				!held.runtimeAdmissionReceipt.Equal(b.Instance.Admission.Receipt) || held.runtimeAdmissionEgress.Plan != "pro" {
				t.Fatalf("adopted admission = %+v", held)
			}
			if a := v.Admission; a == nil || a.Token != b.Instance.Admission.Receipt.Binding.Token || a.IssuingIncarnation != held.admissionHeldFrom || a.Netns != g.lease.Netns || a.LeaseUID != int32(g.lease.UID) {
				t.Fatalf("verdict admission = %+v", v.Admission)
			}
		})
	}
}
