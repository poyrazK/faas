package fcvm

// Cold adoption of running guests at vmmd startup (vmmd restart adoption
// ADR §2, slice 2), behind vmmd.toml adopt_running_vms.
//
// A guest is adopted only when its record passes the adoption proof
// (resource_adoption_classify.go) and falls inside what this slice rebuilds.
// Everything else stays in ADR-472 quarantine, exactly as without adoption,
// and is reported with a reason. An adopted guest gets back its slot, its
// lifecycle process, its network namespace binding and resource ownership,
// its guest channels and its monitor loops, so the successor can serve,
// park, freeze and destroy it like one it booted.
//
// A guest woken on a claimed prepared network (ADR-149) is adopted like
// any other: its record keeps the spare's storage key and creator boot, and
// the claim already moved the namespace binding and link to the guest's
// names. Unclaimed spares are never adopted.
//
// Not yet rebuilt, so refused: private-network members recorded before
// attachments were, runtime-admission receipts (never re-bound: ADR-595's
// boot history is immutable; see the ADR), assets other than jails, the
// netns, the host veth, the private side-link, image binds and staged
// (materialised or clone) files, and guests whose Firecracker jail path
// changed with an upgrade.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"path/filepath"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

const (
	adoptionRefusedStaticEgress    = "static_egress_ip"
	adoptionRefusedPrivateNetwork  = "private_network"
	adoptionRefusedPrepared        = "prepared_network"
	adoptionRefusedRehearsal       = "rehearsal"
	adoptionRefusedEgressCircuit   = "egress_circuit"
	adoptionRefusedProductionFork  = "production_fork"
	adoptionRefusedFunction        = "function_resources"
	adoptionRefusedAsset           = "unsupported_asset"
	adoptionRefusedFCChanged       = "firecracker_changed"
	adoptionRefusedNetworkMissing  = "network_unrecorded"
	adoptionRefusedAdoptFailed     = "adopt_failed"
	adoptionRefusedAdoptIncomplete = "adopt_incomplete"
	adoptionRefusedAdmitted        = "admission_receipt"
	adoptionRefusedTeardownFailed  = "teardown_failed"
)

// guestAdopter is the VMM side of adoption; JailerVMM implements it.
type guestAdopter interface {
	adoptGuestProcess(l Lease, process resourceProcessIdentity, owner adoptedOwner, jails, binds, files []resourceAsset) error
	prepareRegisteredGuestVsockListeners(l Lease, origins ...GuestVsockStreamOrigin) error
	adoptJobExitListener(l Lease) error
	chrootRoot(instance string) string
	ResumeVM(ctx context.Context, l Lease) error
}

// WithAdoptAdmittedRuntimes lets adoption keep guests booted under an ADR-595
// runtime-admission receipt (vmmd.toml adopt_admitted_runtimes). Startup
// only. Off, they are refused, and schedd re-admits them.
func (m *Manager) WithAdoptAdmittedRuntimes(on bool) *Manager {
	m.adoptAdmittedRuntimes = on
	return m
}

// AdmissionHeldFrom returns the incarnation that issued parent when this
// process adopted instance carrying exactly that receipt, else "". A grant
// naming parent is then this process's to serve although parent names its
// predecessor (runtimeadmission.Promotion.Held).
func (m *Manager) AdmissionHeldFrom(instance string, parent runtimeadmission.Receipt) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := m.live[instance]
	if inst == nil || inst.admissionHeldFrom == "" || inst.admissionHeldFrom != parent.Binding.Incarnation || !inst.runtimeAdmissionReceipt.Equal(parent) {
		return ""
	}
	return inst.admissionHeldFrom
}

// TrackRestartLeases starts the adoption report's lease set (ADR §3). vmmd
// calls it before adopting, so the scheduler can tell every guest this vmmd
// creates from the predecessor's.
func (m *Manager) TrackRestartLeases() { m.alloc.trackLeases() }

// RestartLeases returns every instance leased since TrackRestartLeases, and
// false when tracking never started or the set overflowed.
func (m *Manager) RestartLeases() ([]string, bool) { return m.alloc.leasedSinceTracking() }

// AdoptRestartSurvivors adopts every proven restart survivor. It must run
// after RecoverRestartQuarantine and before prepared networks or any RPC.
func (m *Manager) AdoptRestartSurvivors(ctx context.Context) (AdoptionDryRunReport, error) {
	adopter, ok := m.vmm.(guestAdopter)
	if !ok || m.nativeVMM() != nil {
		return AdoptionDryRunReport{}, errors.New("adoption needs the jailer VMM without native process recovery")
	}
	if m.resourceJournal == nil {
		return AdoptionDryRunReport{}, errors.New("adoption needs the resource journal")
	}
	m.mu.Lock()
	ready := m.restartInventoryDone && len(m.live) == 0 && m.preparedNetworks == nil
	m.mu.Unlock()
	if !ready {
		return AdoptionDryRunReport{}, errors.New("adoption must follow the restart inventory and precede instances and prepared networks")
	}
	boot, err := resourceKernelBootID("/proc")
	if err != nil {
		return AdoptionDryRunReport{}, fmt.Errorf("adoption: kernel boot id: %w", err)
	}
	c := adoptionClassifier{
		procRoot: "/proc", cgroupRoot: cgroupRoot, netRoot: "/sys/class/net",
		bootID: boot, api: firecrackerAPIState, pidfd: adoptionPidfdCheck, vethPeer: vethPeerInProcessNetns,
	}
	return m.adoptClassified(ctx, adopter, c)
}

func (m *Manager) adoptClassified(ctx context.Context, adopter guestAdopter, c adoptionClassifier) (AdoptionDryRunReport, error) {
	proved, err := c.run(ctx, m.resourceJournal)
	if err != nil {
		return proved, err
	}
	rep := AdoptionDryRunReport{Refused: map[string]int{}}
	for _, v := range proved.Verdicts {
		if v.Adoptable {
			recorded := v
			v = m.adoptOne(ctx, adopter, c, v)
			v.AppID, v.DevPatched = recorded.AppID, recorded.DevPatched
		}
		rep.Verdicts = append(rep.Verdicts, v)
		if AdoptionEnds(v.Resolved) && !v.Adoptable && v.Reason == "" {
			if v.Resolved == AdoptionResolvedKilled {
				rep.Killed++
			} else {
				rep.Stopped++
			}
			continue
		}
		if v.Adoptable {
			rep.Adopted++
			if v.FenceRepair {
				rep.FenceRepairs++
			}
			continue
		}
		rep.Refused[v.Reason]++
	}
	rep.Adoptable = rep.Adopted
	if rep.Adopted > 0 {
		// Host-level policy renders from the live set; include the adopted.
		m.renderHostSMTPAllowlistRules(ctx, false)
	}
	return rep, ctx.Err()
}

// adoptionScope names why a proven record is outside what adoption rebuilds,
// or returns "". chrootRoot is the jail root this vmmd's Firecracker uses for
// an instance; a candidate binary passes its own.
func adoptionScope(chrootRoot func(string) string, adoptAdmitted bool, r resourceJournalRecord) (string, string) {
	a := r.Adoption
	switch {
	case a.Instance.Admitted && !adoptAdmitted:
		return adoptionRefusedAdmitted, "adopt_admitted_runtimes is off: the admission is not re-bound"
	case a.Instance.Admitted && a.Instance.Admission == nil:
		return adoptionRefusedAdmitted, "the block does not carry the admission receipt"
	case (a.Network.PrivateNetworkHandleV4 != 0 || a.Network.PrivateNetworkHandleV6 != 0) && a.Network.Private == nil:
		return adoptionRefusedPrivateNetwork, "private network member recorded before attachments were"
	case a.Network.StaticEgressIP != "":
		return adoptionRefusedStaticEgress, "static egress SNAT is not rebuilt yet"
	case a.Network.RehearsalRunID != "":
		return adoptionRefusedRehearsal, "rehearsal containment and recorder are not rebuilt"
	case a.Network.EgressCircuits:
		return adoptionRefusedEgressCircuit, "egress circuit registration is not rebuilt"
	case r.Lease.Quarantine:
		// ADR-732: the fork's quarantine lives on Instance.Net, which the
		// block does not carry; a re-rendered netns would drop its chains.
		return adoptionRefusedProductionFork, "production fork quarantine is not rebuilt"
	case r.preparedSpare():
		// Unreachable: the classifier skips spares and a spare cannot carry
		// a block. A guest on a claimed prepared network is adopted.
		return adoptionRefusedPrepared, "unclaimed prepared spare"
	}
	var root string
	var ns, veth bool
	for _, asset := range r.Assets {
		switch asset.Kind {
		case "jail":
			if filepath.Base(asset.Path) == "root" {
				root = asset.Path
			}
		case "netns":
			ns = asset.File != nil
		case "veth":
			veth = veth || (asset.Link != nil && asset.Link.Name == r.Lease.VethHost)
		case "bind":
		case "materialised", "clone":
			// A ready guest's staged files are checkpointed; one without its
			// identity cannot be proven ours to remove at teardown.
			if asset.File == nil {
				return adoptionRefusedAsset, "unrecorded " + asset.Kind + " file identity"
			}
		default:
			return adoptionRefusedAsset, "asset kind " + asset.Kind
		}
	}
	if root != chrootRoot(r.Lease.Instance) {
		return adoptionRefusedFCChanged, "jail " + root + " is not this Firecracker's"
	}
	if !r.Lease.Networkless && (!ns || !veth) {
		return adoptionRefusedNetworkMissing, "namespace or host veth not recorded"
	}
	return "", ""
}

// adoptOne rebuilds one proven guest. Before its process is registered a
// failure only returns the slot to quarantine; after, the guest is
// destroyed through the normal teardown, which a refused guest's scheduler
// recovery then replaces.
func (m *Manager) adoptOne(ctx context.Context, adopter guestAdopter, c adoptionClassifier, v AdoptionVerdict) AdoptionVerdict {
	r, ok, err := m.resourceJournal.lookup(v.Instance)
	if err != nil || !ok || r.Adoption == nil || r.Process == nil {
		return refuse(v.Instance, adoptionRefusedAdoptFailed, fmt.Sprint("record unavailable: ", err))
	}
	if reason, detail := adoptionScope(adopter.chrootRoot, m.adoptAdmittedRuntimes, r); reason != "" {
		return refuse(v.Instance, reason, detail)
	}
	if m.functionActivity != nil && functionlifetime.NativeNode(r.Adoption.Instance.Runtime) {
		// Function VM resources (native qualification builds only) keep
		// execution occupancy and migration authority the block does not
		// record.
		return refuse(v.Instance, adoptionRefusedFunction, "function execution state is not recorded")
	}
	if err := m.adoptGuest(ctx, adopter, r); err != nil {
		return refuse(v.Instance, adoptionRefusedAdoptFailed, err.Error())
	}
	if AdoptionEnds(v.Resolved) {
		// The guest is proven ours and was being torn down, or its stream
		// ended with the predecessor: finish through the ordinary path, which
		// kills it through its pidfd, frees its resources and retires the
		// record. A failure keeps the retained cleanup, which the scheduler's
		// next destroy retries.
		if err := m.Destroy(context.WithoutCancel(ctx), v.Instance); err != nil {
			return refuse(v.Instance, adoptionRefusedTeardownFailed, err.Error())
		}
		return AdoptionVerdict{Instance: v.Instance, AppID: v.AppID, Resolved: v.Resolved}
	}
	if err := m.resumeAdoptedGuest(ctx, adopter, c, r); err != nil {
		destroyErr := m.Destroy(context.WithoutCancel(ctx), v.Instance)
		return refuse(v.Instance, adoptionRefusedAdoptIncomplete, errors.Join(err, destroyErr).Error())
	}
	v.Admission = m.adoptedAdmission(v.Instance)
	return v
}

// adoptedAdmission is what an adopted admitted guest's report entry carries.
func (m *Manager) adoptedAdmission(instance string) *AdoptionAdmission {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := m.live[instance]
	if inst == nil || inst.admissionHeldFrom == "" {
		return nil
	}
	rc := inst.runtimeAdmissionReceipt
	return &AdoptionAdmission{
		Token: rc.Binding.Token, IssuingIncarnation: rc.Binding.Incarnation, CompletedAtUnixNano: rc.CompletedAtUnixNano,
		Netns: inst.Net.Netns, HostIP: inst.Lease.HostIP.String(), LeaseUID: int32(inst.Lease.UID), Paused: inst.Paused,
	}
}

func (m *Manager) adoptGuest(ctx context.Context, adopter guestAdopter, r resourceJournalRecord) error {
	var jails, binds, files, private []resourceAsset
	var ns, veth resourceAsset
	for _, a := range r.Assets {
		switch a.Kind {
		case "jail":
			jails = append(jails, a)
		case "bind":
			binds = append(binds, a)
		case "materialised", "clone":
			files = append(files, a)
		case "netns":
			ns = a
		case "veth":
			if a.Link != nil && a.Link.Name == r.Lease.VethHost {
				veth = a
			}
			if host, _ := privateVethNames(r.Lease.Slot); a.Link != nil && a.Link.Name == host {
				private = append(private, a)
			}
		}
	}
	id := r.Lease.Instance
	if _, err := m.alloc.adoptQuarantined(id, r.Lease.Slot); err != nil {
		return err
	}
	if !r.Lease.Networkless {
		anchor := m.anchorNetns
		if anchor == nil {
			anchor = anchorAdoptedNetns
		}
		anchored, err := anchor(r.Lease.Netns, r.Process.PID, *ns.File)
		if err == nil {
			err = m.resourceJournal.reanchorNamespace(id, *anchored)
		}
		if err != nil {
			m.alloc.unadopt(id)
			return fmt.Errorf("network namespace: %w", err)
		}
		m.rememberNamespace(r.Lease.Netns, *anchored)
		m.rememberLink(id, veth)
		for _, a := range private {
			m.rememberLink(id, a) // Teardown removes the side-link it owns.
		}
	}
	lease := m.beginProcessAttempt(r.Lease)
	owner := adoptedOwner{AppID: r.Adoption.Instance.AppID, DeploymentID: r.Adoption.Instance.DeploymentID}
	if err := adopter.adoptGuestProcess(lease, *r.Process, owner, jails, binds, files); err != nil {
		m.alloc.unadopt(id)
		return err
	}
	m.adoptDriveHandoff(ctx, adopter, lease, r)
	inst := m.adoptedInstance(lease, r.Adoption)
	m.restoreAdoptedPolicy(id, inst.AppID, r.Adoption)
	m.mu.Lock()
	m.live[id] = inst
	m.cidToID[GuestVsockCID(lease.Slot)] = id
	delete(m.restartQuarantine, id)
	m.mu.Unlock()
	return nil
}

// driveHandoffAdopter rebuilds a measured guest's drive handoff
// (runtime_drive_handoff_adopt.go); JailerVMM implements it.
type driveHandoffAdopter interface {
	adoptRuntimeDriveHandoff(ctx context.Context, lease Lease, pid int, r runtimeadmission.Receipt, pins []resourceAdoptionDrivePin) error
}

// adoptDriveHandoff gives an adopted measured guest back the drive handoff
// its captures need. Without it the guest still serves; its next capture is
// refused and the app's next wake cold-boots (ADR-005), as before.
func (m *Manager) adoptDriveHandoff(ctx context.Context, adopter guestAdopter, lease Lease, r resourceJournalRecord) {
	adm := r.Adoption.Instance.Admission
	h, ok := adopter.(driveHandoffAdopter)
	if !r.Adoption.Instance.Admitted || adm == nil || len(adm.Drives) == 0 || !ok {
		return
	}
	if err := h.adoptRuntimeDriveHandoff(ctx, lease, r.Process.PID, adm.Receipt, adm.Drives); err != nil {
		slog.Warn("vmmd: adopted measured guest has no drive handoff; its next capture is refused", "instance", lease.Instance, "err", err)
	}
}

// adoptedInstance rebuilds the Instance from the record. Network policy that
// is not recorded (egress allowlist contents, DNS gating sets) is pushed
// again by the scheduler (slice 3); the per-netns rules themselves survived.
func (m *Manager) adoptedInstance(l Lease, a *resourceAdoption) *Instance {
	nc := netns.NewConfig(l.Instance, l.Netns, l.VethHost, l.VethPeer, l.HostIP)
	nc.TapUID = l.UID
	nc.GuestAppPort = a.Instance.Port
	nc.ConntrackCap = m.conntrackCap
	for _, p := range a.Network.EgressAllowlist {
		if prefix, err := netip.ParsePrefix(p); err == nil {
			nc.EgressAllowlist = append(nc.EgressAllowlist, prefix)
		}
	}
	if p := a.Network.Private; p != nil {
		_ = p.netConfig(&nc, l.Slot) // Validated when the record was read.
	}
	method := WakeColdBoot
	if a.Firecracker.Method == WakeRestore.String() {
		method = WakeRestore
	}
	in := a.Instance
	inst := &Instance{
		Lease: l, Net: nc, Method: method, Plan: l.Plan,
		AppID: in.AppID, AccountID: in.AccountID, DeploymentID: in.DeploymentID, WakeID: in.WakeID, NodeID: in.NodeID,
		Port: in.Port, HealthcheckPath: in.HealthcheckPath, HealthcheckGRPC: in.HealthcheckGRPC, ImageHealthcheckRequired: in.ImageHealthcheckRequired,
		StartupDeadlineS: in.StartupDeadlineS, ExecutionMode: in.ExecutionMode, Runtime: in.Runtime,
		WorkloadNames:          append([]string(nil), in.WorkloadNames...),
		LivenessProbe:          append([]byte(nil), in.LivenessProbe...),
		ReadinessProbe:         append([]byte(nil), in.ReadinessProbe...),
		QuietPauseAfter:        durationMillis(in.QuietPauseAfterMs),
		AllowlistHandleV4:      a.Network.AllowlistHandleV4,
		AllowlistHandleV6:      a.Network.AllowlistHandleV6,
		PrivateNetworkHandleV4: a.Network.PrivateNetworkHandleV4,
		PrivateNetworkHandleV6: a.Network.PrivateNetworkHandleV6,
		adoptionVCPUs:          a.Firecracker.VCPUs,
		adoptionMemSizeMiB:     a.Firecracker.MemoryMiB,
		devPatched:             in.DevPatched,
		migrationPaused:        in.MigrationPaused,
		Paused:                 in.WarmPaused,
		warmSpare:              in.WarmSpare,
		Characterization:       api.CharacterizationReport{ObservedClass: in.ObservedClass},
	}
	if adm := in.Admission; in.Admitted && adm != nil {
		// The receipt names the incarnation that issued it; this process
		// holds it from now on (ADR-595 extension).
		inst.runtimeAdmissionReceipt = adm.Receipt.Clone()
		inst.runtimeAdmissionEgress = WakeRequest{AppID: in.AppID, Plan: api.Plan(adm.Plan), EgressAllowlist: slices.Clone(adm.EgressAllowlist), EgressPorts: slices.Clone(adm.EgressPorts)}
		inst.admissionHeldFrom = adm.Receipt.Binding.Incarnation
	}
	if job := in.Job; a.Kind == adoptionKindJob && job != nil {
		inst.IsJob, inst.JobInteractive, inst.JobStartGateEnabled = true, job.Interactive, job.StartGate
		if job.Exit != nil {
			exit := *job.Exit
			inst.jobExit = &exit
		}
	}
	if t := in.FrameworkReadyAt; t != nil {
		v := *t
		inst.FrameworkReadyAt = &v
	}
	if a.Lifecycle.State == adoptionStateFrozen && a.Lifecycle.FrozenSince != nil {
		inst.Frozen, inst.FrozenSince = true, *a.Lifecycle.FrozenSince
	}
	return inst
}

// restoreAdoptedPolicy puts back the per-app and per-instance state the
// block carries: the egress allowlist host rules render from, and the
// backing identity a capture needs. A wake of the same app since startup
// already set the allowlist and wins.
func (m *Manager) restoreAdoptedPolicy(instance, appID string, a *resourceAdoption) {
	if appID != "" {
		m.perAppAllowlistMu.Lock()
		if m.perAppAllowlist == nil {
			m.perAppAllowlist = make(map[string][]netip.Prefix)
		}
		if _, set := m.perAppAllowlist[appID]; !set {
			var prefixes []netip.Prefix
			for _, p := range a.Network.EgressAllowlist {
				if prefix, err := netip.ParsePrefix(p); err == nil {
					prefixes = append(prefixes, prefix)
				}
			}
			m.perAppAllowlist[appID] = prefixes
		}
		m.perAppAllowlistMu.Unlock()
	}
	if a.Backing != nil {
		m.backingMu.Lock()
		if m.instanceBacking == nil {
			m.instanceBacking = make(map[string]BackingIdentity)
		}
		m.instanceBacking[instance] = *a.Backing
		m.backingMu.Unlock()
	}
}

// resumeAdoptedGuest re-opens the guest's channels, undoes a quiet pause the
// previous vmmd left behind and restarts the monitor loops.
func (m *Manager) resumeAdoptedGuest(ctx context.Context, adopter guestAdopter, c adoptionClassifier, r resourceJournalRecord) error {
	id := r.Lease.Instance
	m.mu.Lock()
	inst := m.live[id]
	m.mu.Unlock()
	if inst == nil {
		return errors.New("adopted guest is no longer live")
	}
	// The guest's channels carry the wake identity Wake bound them to, so
	// identity-checked receivers (sidecar health) keep accepting them.
	origin := GuestVsockStreamOrigin{AppID: inst.AppID, WakeID: inst.WakeID, NodeID: inst.NodeID}
	if err := adopter.prepareRegisteredGuestVsockListeners(inst.Lease, origin); err != nil {
		return fmt.Errorf("guest channels: %w", err)
	}
	if inst.IsJob {
		// A job has no probe loops. Its exit receiver comes back unless the
		// exit was already recorded, which WaitJobExit then returns.
		m.mu.Lock()
		exited := inst.jobExit != nil
		m.mu.Unlock()
		if !exited {
			if err := adopter.adoptJobExitListener(inst.Lease); err != nil {
				return fmt.Errorf("job exit listener: %w", err)
			}
		}
		m.markAdoptionDirty(id)
		return nil
	}
	if op := r.Adoption.Lifecycle.Op; op != nil && (op.Kind == adoptionOpFreeze || op.Kind == adoptionOpThaw) {
		// An interrupted freeze or thaw always ends running: the guest is
		// resumed below if paused, its memory stays resident, and the report
		// says thawed so the scheduler can settle a FROZEN row.
		m.setFrozenSwap(inst.Lease, false)
		m.mu.Lock()
		inst.Frozen, inst.FrozenSince = false, time.Time{}
		m.mu.Unlock()
	}
	if inst.Frozen {
		return nil // Thaw restarts the loops.
	}
	m.mu.Lock()
	migrationPaused, warmSpare := inst.migrationPaused, inst.warmSpare
	m.mu.Unlock()
	if migrationPaused || warmSpare {
		// A migration source waits for its lease owner and an unclaimed warm
		// spare for ResumeWarmInstance; ResumeVM resumes either and starts
		// its loops. A spare that is already running (its claim was cut
		// short) is settled by the scheduler's retried resume, which is
		// idempotent.
		return nil
	}
	if state, err := c.api(ctx, filepath.Join(adopter.chrootRoot(id), APISockName)); err == nil && state.State == "Paused" {
		// Not frozen and not warm-pool: the previous vmmd quiet-paused it
		// (ADR-1151), or its freeze or thaw was cut short. The new
		// quiet-pause loop pauses it again when idle.
		if err := adopter.ResumeVM(ctx, inst.Lease); err != nil {
			return fmt.Errorf("resume quiet-paused guest: %w", err)
		}
	}
	loops := context.WithoutCancel(ctx) //nolint:contextcheck // monitor loops belong to the daemon, not to the startup pass
	if m.lifecycleCtx != nil {
		loops = m.lifecycleCtx
	}
	m.startLivenessLoop(loops, id, inst.Lease.Slot, inst.LivenessProbe)
	m.startReadinessLoop(loops, id, inst.Lease.Slot, inst.ReadinessProbe)
	if inst.FrameworkReadyAt == nil {
		m.startFrameworkReadyLoop(loops, id)
	}
	m.markAdoptionDirty(id)
	return nil
}

func durationMillis(ms int64) time.Duration { return time.Duration(ms) * time.Millisecond }
