package fcvm

// adr: 20261017 vmmd restart adoption

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adoptingVMM records what adoption asked of the VMM.
type adoptingVMM struct {
	*fakeVMM
	root      string
	adopted   []Lease
	binds     int
	jails     int
	listeners int
	jobExits  int
	resumed   int
	files     int
	owners    []adoptedOwner
	adoptErr  error
}

func (v *adoptingVMM) adoptGuestProcess(l Lease, _ resourceProcessIdentity, owner adoptedOwner, jails, binds, files []resourceAsset) error {
	v.owners = append(v.owners, owner)
	if v.adoptErr != nil {
		return v.adoptErr
	}
	v.adopted = append(v.adopted, l)
	v.jails += len(jails)
	v.binds += len(binds)
	v.files += len(files)
	return nil
}
func (v *adoptingVMM) prepareRegisteredGuestVsockListeners(Lease) error { v.listeners++; return nil }
func (v *adoptingVMM) adoptJobExitListener(Lease) error                 { v.jobExits++; return nil }
func (v *adoptingVMM) chrootRoot(string) string                         { return v.root }
func (v *adoptingVMM) ResumeVM(context.Context, Lease) error            { v.resumed++; return nil }

// adoptionManager is a Manager that restarted over the fixture's journal:
// the inventory quarantined the survivor, as at real startup.
func adoptionManager(t *testing.T, f *adoptionFixture, block resourceAdoption) (*Manager, *adoptingVMM) {
	t.Helper()
	f.must(f.journal.recordAdoption(f.lease, f.process, block))
	vmm := &adoptingVMM{fakeVMM: &fakeVMM{}, root: f.jailRoot}
	m := newTestManager(&fakeRunner{}, vmm)
	f.must(m.WithResourceJournal(f.journal))
	opts := restartInventoryOptions{procRoot: f.proc, netRoot: f.netDir, netnsRoot: filepath.Join(f.root, "netns"), jailRoot: filepath.Join(f.root, "jail")}
	if _, err := m.recoverRestartQuarantine(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if !m.isRestartQuarantined(idLive) {
		t.Fatal("fixture survivor was not quarantined")
	}
	// The successor's own mount namespace: the re-anchored binding is made
	// in it, and teardown checks the binding against it.
	successor := resourceMountIdentity{BootID: idOther, Namespace: 4026532999}
	m.namespaceContext = func() (*resourceMountIdentity, error) { c := successor; return &c, nil }
	// The fixture has no real binding or link: probes see them gone. The
	// veth's creator context is the host network namespace, which a
	// successor shares with its predecessor.
	m.namespaceProbe = func(string) (*resourceAsset, error) { return nil, nil }
	m.linkContext = func() (*resourceMountIdentity, error) {
		return &resourceMountIdentity{BootID: idOther, Namespace: 4026531840}, nil
	}
	m.linkProbe = func(string, int) (*resourceLinkIdentity, error) { return nil, nil }
	m.anchorNetns = func(name string, _ int, want resourceFileIdentity) (*resourceAsset, error) {
		ctx := successor
		mount := successor
		mount.MountID = 7
		return &resourceAsset{Kind: "netns", Path: filepath.Join("/run/netns", name), File: &want, Namespace: &ctx, Mount: &mount}, nil
	}
	return m, vmm
}

func (m *Manager) isRestartQuarantined(instance string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.restartQuarantine[instance]
	return ok
}

func TestAdoptRestartSurvivorsRebuildsAProvenGuest(t *testing.T) {
	f := newAdoptionFixture(t)
	block := f.block()
	block.Instance.QuietPauseAfterMs = 4000
	block.Instance.HealthcheckGRPC = true
	block.Network.EgressAllowlist = []string{"198.51.100.0/24"}
	backing := BackingIdentity{Version: backingIdentityVersion, Kernel: "kernel-digest", Base: "base-digest", Timer: "clocksource=kvm-clock"}
	block.Backing = &backing
	m, vmm := adoptionManager(t, f, block)
	rep, err := m.adoptClassified(t.Context(), vmm, f.classifier())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Adopted != 1 || len(rep.Refused) != 0 {
		t.Fatalf("adoption = %+v", rep)
	}
	inst := m.live[idLive]
	if inst == nil || inst.AppID != "app-1" || inst.DeploymentID != "dep-1" || inst.Port != 8080 || !inst.HealthcheckGRPC ||
		inst.QuietPauseAfter.Milliseconds() != 4000 || inst.Method != WakeRestore || inst.Lease.Slot != f.lease.Slot {
		t.Fatalf("adopted instance = %+v", inst)
	}
	if m.isRestartQuarantined(idLive) || !m.HasInstanceOwnership(idLive) {
		t.Fatal("adopted guest is still quarantined or unowned")
	}
	if len(vmm.adopted) != 1 || vmm.jails != 2 || vmm.listeners != 1 || vmm.resumed != 0 {
		t.Fatalf("vmm adoption = %+v", vmm)
	}
	// The log ring keeps the app's identity, as a booted guest's does.
	if len(vmm.owners) != 1 || vmm.owners[0] != (adoptedOwner{AppID: "app-1", DeploymentID: "dep-1"}) {
		t.Fatalf("adopted owner = %+v", vmm.owners)
	}
	if m.cidToID[GuestVsockCID(f.lease.Slot)] != idLive {
		t.Fatal("guest CID not mapped back to the instance")
	}
	if got := m.instanceBacking[idLive]; got != backing {
		t.Fatalf("backing identity = %+v, want %+v", got, backing)
	}
	if got := m.perAppAllowlist["app-1"]; len(got) != 1 || got[0].String() != "198.51.100.0/24" || len(inst.Net.EgressAllowlist) != 1 {
		t.Fatalf("egress allowlist = %v (net %v)", got, inst.Net.EgressAllowlist)
	}
	// The slot is leased to the guest: a fresh lease cannot take it, and the
	// recorded namespace now names this vmmd's binding.
	if l, err := m.alloc.Acquire("fresh"); err != nil || l.Slot == f.lease.Slot {
		t.Fatalf("fresh lease = %+v, %v", l, err)
	}
	r := f.record()
	for _, a := range r.Assets {
		if a.Kind == "netns" && (a.Mount == nil || a.Mount.Namespace != 4026532999) {
			t.Fatalf("namespace checkpoint not re-anchored: %+v", a)
		}
	}
}

func TestAdoptRestartSurvivorsResumesAQuietPause(t *testing.T) {
	f := newAdoptionFixture(t)
	f.api.State = "Paused"
	m, vmm := adoptionManager(t, f, f.block())
	if rep, err := m.adoptClassified(t.Context(), vmm, f.classifier()); err != nil || rep.Adopted != 1 {
		t.Fatalf("adoption = %+v, %v", rep, err)
	}
	if vmm.resumed != 1 {
		t.Fatalf("quiet-paused guest resumed %d times, want once", vmm.resumed)
	}
}

// An interrupted freeze or thaw always ends with a running guest, whatever
// the block recorded, and the verdict says so.
func TestAdoptRestartSurvivorsResumesAnInterruptedFreeze(t *testing.T) {
	for _, kind := range []string{adoptionOpFreeze, adoptionOpThaw} {
		t.Run(kind, func(t *testing.T) {
			f := newAdoptionFixture(t)
			f.api.State = "Paused"
			block := f.block()
			since := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
			block.Lifecycle = resourceAdoptionLifecycle{State: adoptionStateFrozen, FrozenSince: &since,
				Op: &resourceAdoptionOp{Kind: kind, Token: "t", Started: since}}
			m, vmm := adoptionManager(t, f, block)
			rep, err := m.adoptClassified(t.Context(), vmm, f.classifier())
			if err != nil || rep.Adopted != 1 || len(rep.Verdicts) != 1 || rep.Verdicts[0].Resolved != AdoptionResolvedThawed {
				t.Fatalf("adoption = %+v, %v", rep, err)
			}
			inst := m.live[idLive]
			if inst == nil || inst.Frozen || vmm.resumed != 1 {
				t.Fatalf("instance %+v resumed %d times; want running, resumed once", inst, vmm.resumed)
			}
		})
	}
	// A completed freeze stays frozen and resolves nothing.
	f := newAdoptionFixture(t)
	f.api.State = "Paused"
	block := f.block()
	since := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	block.Lifecycle = resourceAdoptionLifecycle{State: adoptionStateFrozen, FrozenSince: &since}
	m, vmm := adoptionManager(t, f, block)
	rep, err := m.adoptClassified(t.Context(), vmm, f.classifier())
	if err != nil || rep.Adopted != 1 || rep.Verdicts[0].Resolved != "" || !m.live[idLive].Frozen || vmm.resumed != 0 {
		t.Fatalf("completed freeze = %+v, %v, resumed %d", rep, err, vmm.resumed)
	}
}

// A real guest stages its writable layer as a reflink clone. The checkpointed
// clone is handed to the VMM for teardown; one without an identity refuses.
func TestAdoptRestartSurvivorsAdoptsStagedFiles(t *testing.T) {
	f := newAdoptionFixture(t)
	clone := filepath.Join(f.root, "layer.clone")
	f.write(clone, "layer")
	f.must(f.journal.addAsset(idLive, resourceAsset{Kind: "clone", Path: clone}))
	id, err := statIdentity(clone)
	f.must(err)
	f.must(f.journal.checkpointAsset(idLive, clone, id, nil))
	m, vmm := adoptionManager(t, f, f.block())
	if rep, err := m.adoptClassified(t.Context(), vmm, f.classifier()); err != nil || rep.Adopted != 1 || vmm.files != 1 {
		t.Fatalf("adoption = %+v, %v, files %d", rep, err, vmm.files)
	}

	f = newAdoptionFixture(t)
	f.must(f.journal.addAsset(idLive, resourceAsset{Kind: "materialised", Path: filepath.Join(f.root, "uncheckpointed")}))
	m, vmm = adoptionManager(t, f, f.block())
	rep, err := m.adoptClassified(t.Context(), vmm, f.classifier())
	if err != nil || rep.Adopted != 0 || rep.Refused[adoptionRefusedAsset] != 1 {
		t.Fatalf("uncheckpointed file = %+v, %v", rep, err)
	}
}

func TestAdoptRestartSurvivorsRefusesOutsideSlice2Scope(t *testing.T) {
	tests := []struct {
		name   string
		block  func(*resourceAdoption)
		vmm    func(*adoptingVMM)
		reason string
	}{
		{name: "admission receipt", block: func(b *resourceAdoption) { b.Instance.Admitted = true }, reason: adoptionRefusedAdmitted},
		{name: "private network", block: func(b *resourceAdoption) { b.Network.PrivateNetworkHandleV4 = 9 }, reason: adoptionRefusedPrivateNetwork},
		{name: "static egress ip", block: func(b *resourceAdoption) { b.Network.StaticEgressIP = "203.0.113.7" }, reason: adoptionRefusedStaticEgress},
		{name: "rehearsal containment", block: func(b *resourceAdoption) { b.Network.RehearsalRunID = "run-1" }, reason: adoptionRefusedRehearsal},
		{name: "firecracker upgraded", vmm: func(v *adoptingVMM) { v.root = "/srv/fc/jail/firecracker-v1.8.0/x/root" }, reason: adoptionRefusedFCChanged},
		{name: "vmm cannot adopt", vmm: func(v *adoptingVMM) { v.adoptErr = errors.New("no pidfd") }, reason: adoptionRefusedAdoptFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAdoptionFixture(t)
			block := f.block()
			if tt.block != nil {
				tt.block(&block)
			}
			m, vmm := adoptionManager(t, f, block)
			if tt.vmm != nil {
				tt.vmm(vmm)
			}
			rep, err := m.adoptClassified(t.Context(), vmm, f.classifier())
			if err != nil {
				t.Fatal(err)
			}
			if rep.Adopted != 0 || rep.Refused[tt.reason] != 1 {
				t.Fatalf("adoption = %+v, want refused %q", rep, tt.reason)
			}
			// A refused guest keeps today's quarantine: unowned, slot held.
			if m.LiveCount() != 0 || m.HasInstanceOwnership(idLive) || !m.isRestartQuarantined(idLive) {
				t.Fatal("refused guest left quarantine")
			}
			for i := 0; i < 8; i++ {
				if l, err := m.alloc.Acquire(fmt.Sprintf("fresh-%d", i)); err != nil || l.Slot == f.lease.Slot {
					t.Fatalf("fresh lease took the refused guest's slot: %+v, %v", l, err)
				}
			}
		})
	}
}

func TestAdoptRestartSurvivorsNeedsStartupOrder(t *testing.T) {
	f := newAdoptionFixture(t)
	vmm := &adoptingVMM{fakeVMM: &fakeVMM{}, root: f.jailRoot}
	m := newTestManager(&fakeRunner{}, vmm)
	f.must(m.WithResourceJournal(f.journal))
	if _, err := m.AdoptRestartSurvivors(t.Context()); err == nil {
		t.Fatal("adoption ran before the restart inventory")
	}
}

// An adopted guest is destroyed through the ordinary teardown: confirmed
// cleanup retires its record and only then frees its slot.
func TestAdoptedGuestTearsDownThroughTheOrdinaryPath(t *testing.T) {
	f := newAdoptionFixture(t)
	m, vmm := adoptionManager(t, f, f.block())
	if rep, err := m.adoptClassified(t.Context(), vmm, f.classifier()); err != nil || rep.Adopted != 1 {
		t.Fatalf("adoption = %+v, %v", rep, err)
	}
	if err := m.Destroy(t.Context(), idLive); err != nil {
		t.Fatalf("destroy an adopted guest: %v", err)
	}
	if m.LiveCount() != 0 || m.HasInstanceOwnership(idLive) {
		t.Fatal("destroyed guest still live")
	}
	if _, ok, err := f.journal.lookup(idLive); err != nil || ok {
		t.Fatalf("record survived confirmed teardown: %v %v", ok, err)
	}
	if l, err := m.alloc.Acquire("after"); err != nil || l.Slot != f.lease.Slot {
		t.Fatalf("slot not freed after teardown: %+v, %v", l, err)
	}
}

// A stop or destroy the restart cut short is finished, not adopted: the
// proven guest goes through the ordinary teardown and its record retires.
func TestAdoptRestartSurvivorsFinishesAnInterruptedStop(t *testing.T) {
	f := newAdoptionFixture(t)
	block := f.block()
	block.Lifecycle.Op = &resourceAdoptionOp{Kind: adoptionOpStop, Token: "t", Started: time.Now()}
	m, vmm := adoptionManager(t, f, block)
	rep, err := m.adoptClassified(t.Context(), vmm, f.classifier())
	if err != nil || rep.Adopted != 0 || rep.Stopped != 1 || len(rep.Refused) != 0 || len(rep.Verdicts) != 1 {
		t.Fatalf("adoption = %+v, %v", rep, err)
	}
	if v := rep.Verdicts[0]; v.Adoptable || v.Reason != "" || v.Resolved != AdoptionResolvedStopped || v.AppID != "app-1" {
		t.Fatalf("verdict = %+v", v)
	}
	if m.LiveCount() != 0 || m.HasInstanceOwnership(idLive) || m.isRestartQuarantined(idLive) || vmm.listeners != 0 {
		t.Fatal("stopped guest still live, owned, quarantined or re-listened")
	}
	if _, ok, err := f.journal.lookup(idLive); err != nil || ok {
		t.Fatalf("record survived the finished stop: %v %v", ok, err)
	}
	if l, err := m.alloc.Acquire("after"); err != nil || l.Slot != f.lease.Slot {
		t.Fatalf("slot not freed: %+v, %v", l, err)
	}
}

// An app task, execution or builder is killed through the ordinary
// teardown, not adopted: its stream ended with the predecessor. A disposable execution is
// networkless, which the scope and the adoption allow.
func TestAdoptRestartSurvivorsKillsAppTasksAndExecutions(t *testing.T) {
	for _, tc := range []struct {
		kind        string
		networkless bool
	}{{adoptionKindAppTask, false}, {adoptionKindExecution, true}, {adoptionKindBuilder, false}} {
		t.Run(tc.kind, func(t *testing.T) {
			f := adoptionFixtureFor(t, tc.networkless)
			block := f.block()
			block.Kind = tc.kind
			m, vmm := adoptionManager(t, f, block)
			rep, err := m.adoptClassified(t.Context(), vmm, f.classifier())
			if err != nil || rep.Adopted != 0 || rep.Killed != 1 || rep.Stopped != 0 || len(rep.Refused) != 0 {
				t.Fatalf("adoption = %+v, %v", rep, err)
			}
			if v := rep.Verdicts[0]; v.Adoptable || v.Reason != "" || v.Resolved != AdoptionResolvedKilled || v.AppID != "app-1" {
				t.Fatalf("verdict = %+v", v)
			}
			if m.LiveCount() != 0 || m.isRestartQuarantined(idLive) || vmm.listeners != 0 {
				t.Fatal("killed guest still live, quarantined or re-listened")
			}
			if _, ok, err := f.journal.lookup(idLive); err != nil || ok {
				t.Fatalf("record survived the kill: %v %v", ok, err)
			}
			if l, err := m.alloc.Acquire("after"); err != nil || l.Slot != f.lease.Slot {
				t.Fatalf("slot not freed: %+v, %v", l, err)
			}
		})
	}
}

// An undeclared app the guest characterized as a worker keeps that class,
// so its liveness loop stays off HTTP after adoption.
func TestAdoptRestartSurvivorsKeepsTheObservedClass(t *testing.T) {
	f := newAdoptionFixture(t)
	block := f.block()
	block.Instance.ObservedClass = api.ExecutionModeWorker
	m, vmm := adoptionManager(t, f, block)
	if rep, err := m.adoptClassified(t.Context(), vmm, f.classifier()); err != nil || rep.Adopted != 1 {
		t.Fatalf("adoption = %+v, %v", rep, err)
	}
	if inst := m.live[idLive]; inst == nil || !instanceHasNoHTTPContract(inst) {
		t.Fatalf("adopted worker %+v lost its no-HTTP liveness contract", inst)
	}
}

// An unclaimed warm-pool spare is adopted still paused, with no probe loops,
// and ResumeWarmInstance (ResumeVM) claims it on the successor as before. A
// claimed warm instance that was quiet-paused is resumed like any other.
func TestAdoptRestartSurvivorsKeepsAWarmSparePaused(t *testing.T) {
	f := newAdoptionFixture(t)
	f.api.State = "Paused"
	block := f.block()
	block.Instance.WarmPaused, block.Instance.WarmSpare = true, true
	m, vmm := adoptionManager(t, f, block)
	if rep, err := m.adoptClassified(t.Context(), vmm, f.classifier()); err != nil || rep.Adopted != 1 {
		t.Fatalf("adoption = %+v, %v", rep, err)
	}
	inst := m.live[idLive]
	if inst == nil || !inst.Paused || !inst.warmSpare || vmm.resumed != 0 {
		t.Fatalf("warm spare %+v resumed %d times; want adopted paused", inst, vmm.resumed)
	}
	if err := m.ResumeVM(t.Context(), idLive); err != nil {
		t.Fatal(err)
	}
	if inst.warmSpare || !inst.Paused || vmm.resumed != 1 {
		t.Fatalf("claim: spare=%v paused=%v resumed=%d; want claimed, still a warm-pool restore", inst.warmSpare, inst.Paused, vmm.resumed)
	}

	f = newAdoptionFixture(t)
	f.api.State = "Paused"
	block = f.block()
	block.Instance.WarmPaused = true // claimed, then quiet-paused
	m, vmm = adoptionManager(t, f, block)
	if rep, err := m.adoptClassified(t.Context(), vmm, f.classifier()); err != nil || rep.Adopted != 1 || vmm.resumed != 1 || !m.live[idLive].Paused {
		t.Fatalf("claimed warm instance: %+v, %v, resumed %d", rep, err, vmm.resumed)
	}
}

// A job is adopted with its flags and a new exit receiver, and no probe
// loops; one whose exit was recorded gets no receiver, and WaitJobExit
// returns the recorded exit.
func TestAdoptRestartSurvivorsAdoptsAJob(t *testing.T) {
	f := newAdoptionFixture(t)
	block := f.block()
	block.Kind, block.Instance.Port, block.Instance.LivenessProbe = adoptionKindJob, 0, nil
	block.Instance.Job = &resourceAdoptionJob{StartGate: true, Interactive: true}
	m, vmm := adoptionManager(t, f, block)
	if rep, err := m.adoptClassified(t.Context(), vmm, f.classifier()); err != nil || rep.Adopted != 1 {
		t.Fatalf("adoption = %+v, %v", rep, err)
	}
	inst := m.live[idLive]
	if !inst.IsJob || !inst.JobStartGateEnabled || !inst.JobInteractive || inst.jobExit != nil {
		t.Fatalf("adopted job = %+v", inst)
	}
	if vmm.jobExits != 1 || vmm.resumed != 0 {
		t.Fatalf("exit receivers %d, resumes %d; want 1, 0", vmm.jobExits, vmm.resumed)
	}

	f = newAdoptionFixture(t)
	block = f.block()
	exit := JobExitPayload{ExitCode: 124, ErrorClass: "timeout", FinishedAtUnixNano: 7, LeaseToken: "lease-1"}
	block.Kind, block.Instance.Job = adoptionKindJob, &resourceAdoptionJob{Exit: &exit}
	m, vmm = adoptionManager(t, f, block)
	if rep, err := m.adoptClassified(t.Context(), vmm, f.classifier()); err != nil || rep.Adopted != 1 {
		t.Fatalf("adoption = %+v, %v", rep, err)
	}
	if vmm.jobExits != 0 {
		t.Fatal("a job whose exit was recorded got a new receiver")
	}
	if got, err := m.WaitJobExit(t.Context(), idLive, time.Second); err != nil || !reflect.DeepEqual(got, exit) {
		t.Fatalf("WaitJobExit = %+v, %v; want the recorded exit", got, err)
	}
}

// A private-network member is adopted with its attachment rebuilt: policy
// patches find the rule handles and teardown owns the side-link. One
// recorded before attachments were is still refused.
func TestAdoptRestartSurvivorsRebuildsAPrivateNetworkMember(t *testing.T) {
	f := newAdoptionFixture(t)
	block := f.block()
	block.Network.Private = f.attachPrivateNetwork()
	block.Network.PrivateNetworkHandleV4, block.Network.PrivateNetworkHandleV6 = 41, 42
	m, vmm := adoptionManager(t, f, block)
	if rep, err := m.adoptClassified(t.Context(), vmm, f.classifier()); err != nil || rep.Adopted != 1 {
		t.Fatalf("adoption = %+v, %v", rep, err)
	}
	inst := m.live[idLive]
	host, peer := privateVethNames(f.lease.Slot)
	if inst.Net.PrivateNetworkBridge != adoptionTestBridge || inst.Net.PrivateVethHost != host || inst.Net.PrivateVethPeer != peer ||
		inst.Net.PrivateNetworkAddress.String() != "10.250.0.5" || len(inst.Net.PrivateNetworkCIDRs) != 1 ||
		len(inst.Net.PrivateNetworkAllowedCIDRs) != 1 || len(inst.Net.PrivateNetworkFirewallRules) != 1 ||
		inst.PrivateNetworkHandleV4 != 41 || inst.PrivateNetworkHandleV6 != 42 {
		t.Fatalf("adopted private attachment = %+v handles %d/%d", inst.Net, inst.PrivateNetworkHandleV4, inst.PrivateNetworkHandleV6)
	}
	if o, ok := m.linkOwner(host); !ok || o.instance != idLive {
		t.Fatal("the side-link is not owned by the adopted guest")
	}
	if got := adoptionPrivateNetworkFor(inst.Net); got == nil || got.Bridge != adoptionTestBridge {
		t.Fatalf("rewritten block = %+v; want the attachment kept", got)
	}

	f = newAdoptionFixture(t)
	block = f.block()
	block.Network.PrivateNetworkHandleV4 = 41 // an older record: handles, no attachment
	m, vmm = adoptionManager(t, f, block)
	if rep, err := m.adoptClassified(t.Context(), vmm, f.classifier()); err != nil || rep.Refused[adoptionRefusedPrivateNetwork] != 1 {
		t.Fatalf("older private-network record = %+v, %v", rep, err)
	}
}

// A migration source paused for its lease owner is adopted still paused:
// only the owner's ResumeVM (or Destroy) moves it, and resuming clears the
// mark. Resuming it at adoption would run it beside the destination.
func TestAdoptRestartSurvivorsKeepsAMigrationSourcePaused(t *testing.T) {
	f := newAdoptionFixture(t)
	f.api.State = "Paused"
	block := f.block()
	block.Instance.MigrationPaused = true
	m, vmm := adoptionManager(t, f, block)
	rep, err := m.adoptClassified(t.Context(), vmm, f.classifier())
	if err != nil || rep.Adopted != 1 || rep.Verdicts[0].Resolved != "" {
		t.Fatalf("adoption = %+v, %v", rep, err)
	}
	inst := m.live[idLive]
	if inst == nil || !inst.migrationPaused || vmm.resumed != 0 {
		t.Fatalf("migration source %+v resumed %d times; want adopted paused", inst, vmm.resumed)
	}
	if err := m.ResumeVM(t.Context(), idLive); err != nil {
		t.Fatal(err)
	}
	if inst.migrationPaused || vmm.resumed != 1 {
		t.Fatalf("owner resume: paused=%v resumed=%d", inst.migrationPaused, vmm.resumed)
	}
}

// A bind rebuild that fails, here because its source was replaced, leaves
// no reference behind: the next owner of that source sees a clean count.
func TestAdoptGuestBindsDropsReferencesOnFailure(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "base.ext4")
	other := filepath.Join(dir, "layer.ext4")
	for _, p := range []string{good, other} {
		if err := os.WriteFile(p, []byte("image"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	id, err := statIdentity(good)
	if err != nil {
		t.Fatal(err)
	}
	ns := &resourceMountIdentity{BootID: idOther, Namespace: 4026531840, MountID: 9}
	target := resourceFileIdentity{Device: 1, Inode: 2}
	bind := func(src string, file resourceFileIdentity) resourceAsset {
		return resourceAsset{Kind: "bind", Path: "/jail/" + filepath.Base(src), Source: src, SourceFile: &file, OriginalMode: 0o600, Mount: ns, Target: &target}
	}
	v := NewJailerVMM(dir, 0)
	// The record names an inode the source path no longer has. (Not id+1:
	// consecutive files often get consecutive inodes.)
	stale := id
	stale.Inode = 1 << 40
	err = v.adoptGuestBinds(idLive, []resourceAsset{bind(good, id), bind(other, stale)})
	if err == nil {
		t.Fatal("bind with a replaced source adopted")
	}
	v.dropAdoptedBinds(idLive)
	if len(v.bindSourceModes) != 0 || len(v.bindMounts[idLive]) != 0 {
		t.Fatalf("failed adoption left references: %v %v", v.bindSourceModes, v.bindMounts)
	}
	if err := v.adoptGuestBinds(idLive, []resourceAsset{bind(good, id)}); err != nil {
		t.Fatal(err)
	}
	if st := v.bindSourceModes[bindSourceKey{path: good, file: id}]; st.refs != 1 || st.handle == nil {
		t.Fatalf("bind source state = %+v", st)
	}
}

// nsfs inode numbers are reused after a namespace dies (fsn-4, V-A1c): a
// successor whose own namespace got the predecessor's number must not take
// itself for a holder of the predecessor's mounts.
func TestMountNamespaceHeldIgnoresOwnReusedInode(t *testing.T) {
	own, err := statIdentity("/proc/self/ns/mnt")
	if err != nil {
		t.Skip("no /proc on this platform")
	}
	if held, err := mountNamespaceHeld("/proc", own.Inode); err != nil || held {
		t.Fatalf("own namespace reported as a holder: %v %v", held, err)
	}
}
