package fcvm

// Journal record version 7: the adoption record (vmmd restart adoption ADR
// §1, slice 1).
//
// A version-7 record is a version-6 record plus one adoption block that
// describes a ready instance well enough for a replacement vmmd to prove it
// owns the guest and to rebuild its in-memory state. This slice only reads
// and writes the block and classifies records against the adoption proof in
// a dry run (resource_adoption_classify.go). Records stay provenance, exactly
// as ADR-472–475 say: nothing here adopts a guest.
//
// Every release reads version 7. Writing it is switched on separately
// (EnableAdoptionRecords, vmmd.toml adoption_record), because older binaries
// reject version 7 and a rollback onto them would fail startup. The switch
// may be turned on only once every compute node runs a reader.
//
// The block holds no environment, secret, command or artifact content.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

const (
	resourceAdoptionVersion = 7

	// Adoption kinds (ADR "Workload kinds"). Apps and jobs are adopted; app
	// tasks, executions and builders are killed (AdoptionResolvedKilled);
	// qualifications are refused.
	adoptionKindApp           = "app"
	adoptionKindJob           = "job"
	adoptionKindAppTask       = "app_task"
	adoptionKindExecution     = "execution"
	adoptionKindQualification = "qualification"
	adoptionKindBuilder       = "builder"

	adoptionStateReady    = "ready"
	adoptionStateFrozen   = "frozen"
	adoptionStateStopping = "stopping"

	// Operation intents (ADR §1 "Before an effect"), written synchronously
	// before the effect starts and cleared when it completes. A successor
	// resolves the ones it finds (ADR §3 "Mid-flight operations").
	adoptionOpSnapshot     = "snapshot"
	adoptionOpWarmSnapshot = "warm_snapshot"
	adoptionOpPark         = "park"
	adoptionOpFreeze       = "freeze"
	adoptionOpThaw         = "thaw"
	adoptionOpStop         = "stop"
	adoptionMaxOpArtifacts = 8

	// Limits keep a malformed block from approaching resourceRecordLimit.
	adoptionMaxWorkloads   = 64
	adoptionMaxProbeBytes  = 16 * 1024
	adoptionMaxStringBytes = 512
	adoptionMaxAllowlist   = 1024
)

type resourceAdoption struct {
	Kind        string                      `json:"kind"`
	ReadyAt     time.Time                   `json:"ready_at"`
	Firecracker resourceAdoptionFirecracker `json:"firecracker"`
	Instance    resourceAdoptionInstance    `json:"instance"`
	Network     resourceAdoptionNetwork     `json:"network"`
	Lifecycle   resourceAdoptionLifecycle   `json:"lifecycle"`
	// Backing is the kernel, base and timer identity the guest booted with.
	// A capture of the adopted guest needs it to be restorable.
	Backing *BackingIdentity `json:"backing,omitempty"`
	// Bridge is the instance's bridge unit run (instance_bridge.go).
	Bridge *resourceAdoptionBridge `json:"bridge,omitempty"`
}

// resourceAdoptionFirecracker identifies the guest's Firecracker process.
// Exe is the device and inode behind /proc/<pid>/exe: it still matches after
// a release replaces the binary on disk.
type resourceAdoptionFirecracker struct {
	Exe       resourceFileIdentity `json:"exe"`
	Version   string               `json:"version"`
	Method    string               `json:"method"`
	VCPUs     int                  `json:"vcpus"`
	MemoryMiB int                  `json:"memory_mib"`
	// MemoryMax is the cgroup memory.max value vmmd wrote for the scope.
	MemoryMax string `json:"memory_max"`
	// ConsoleFile is true when the guest's stdout is its console file. A
	// pipe-console guest stops answering its API once vmmd dies (V-A3).
	ConsoleFile bool `json:"console_file,omitempty"`
}

// resourceAdoptionInstance is the schedd-owned identity and the monitor
// configuration an adopting vmmd needs to restart its loops.
type resourceAdoptionInstance struct {
	AppID     string `json:"app_id,omitempty"`
	AccountID string `json:"account_id,omitempty"`
	// WakeID and NodeID are the identity the guest's channels and readiness
	// observations carry (ADR-570); a successor binds them to the same wake.
	WakeID          string `json:"wake_id,omitempty"`
	NodeID          string `json:"node_id,omitempty"`
	DeploymentID    string `json:"deployment_id,omitempty"`
	Port            int    `json:"port,omitempty"`
	HealthcheckPath string `json:"healthcheck_path,omitempty"`
	// HealthcheckGRPC selects gRPC readiness (main's health.v1 checks); later
	// readers, such as rehearsal suspend, branch on it.
	HealthcheckGRPC          bool            `json:"healthcheck_grpc,omitempty"`
	ImageHealthcheckRequired bool            `json:"image_healthcheck_required,omitempty"`
	StartupDeadlineS         int             `json:"startup_deadline_s,omitempty"`
	ExecutionMode            string          `json:"execution_mode,omitempty"`
	Runtime                  string          `json:"runtime,omitempty"`
	WorkloadNames            []string        `json:"workload_names,omitempty"`
	LivenessProbe            json.RawMessage `json:"liveness_probe,omitempty"`
	ReadinessProbe           json.RawMessage `json:"readiness_probe,omitempty"`
	QuietPauseAfterMs        int64           `json:"quiet_pause_after_ms,omitempty"`
	FrameworkReadyAt         *time.Time      `json:"framework_ready_at,omitempty"`
	// WarmPaused is a resident warm-pool restore (Instance.Paused), claimed
	// or not. WarmSpare is one not yet claimed: its pause must survive
	// adoption, unlike a quiet pause, which is resumed.
	WarmPaused bool `json:"warm_paused,omitempty"`
	WarmSpare  bool `json:"warm_spare,omitempty"`
	// DevPatched: the guest was served a developer live patch, so its source
	// no longer matches its artifact and it must never be snapshotted
	// (ADR-005, ADR-740).
	DevPatched bool `json:"dev_patched,omitempty"`
	// Admitted: the guest booted under a runtime-admission receipt (ADR-595).
	// Admission carries that receipt and the egress inputs its later grants
	// are checked against, so a successor can keep serving it under a re-bind
	// (docs/adr/20261018-runtime-admission-rebind.md). Admitted without
	// Admission is refused.
	Admitted  bool                       `json:"admitted,omitempty"`
	Admission *resourceAdoptionAdmission `json:"admission,omitempty"`
	// MigrationPaused: a live-migration source paused for its lease owner.
	// A successor adopts it still paused.
	MigrationPaused bool `json:"migration_paused,omitempty"`
	// ObservedClass is the guest's characterization of an undeclared app
	// (api.CharacterizationReport.ObservedClass). A characterized worker
	// has no HTTP liveness contract, so losing it would fail its probes.
	ObservedClass string `json:"observed_class,omitempty"`
	// Job is set for a job guest (kind job).
	Job *resourceAdoptionJob `json:"job,omitempty"`
}

// resourceAdoptionAdmission is an admitted guest's current serving receipt
// (the boot's, or the promotion's once promoted) and its admitted egress.
type resourceAdoptionAdmission struct {
	Receipt         runtimeadmission.Receipt `json:"receipt"`
	Plan            string                   `json:"plan"`
	EgressAllowlist []string                 `json:"egress_allowlist,omitempty"`
	EgressPorts     []uint16                 `json:"egress_ports,omitempty"`
	// Drives pins a measured cold boot's drives by the inodes Firecracker
	// holds, so a successor can rebuild the drive handoff a capture needs
	// (runtime_drive_handoff_adopt.go). Absent for every other guest.
	Drives []resourceAdoptionDrivePin `json:"drives,omitempty"`
}

// validate checks the receipt is a well-formed acknowledgment of a lease
// this record holds.
func (a resourceAdoptionAdmission) validate(l Lease) error {
	r := a.Receipt
	if r.Check(r.Binding, time.Unix(0, r.CompletedAtUnixNano)) != nil {
		return errors.New("invalid adoption admission receipt")
	}
	if r.Binding.InstanceID != l.Instance || r.Netns != l.Netns || r.HostIP != l.HostIP.String() || r.LeaseUID != int32(l.UID) ||
		!api.Plan(a.Plan).Valid() || len(a.EgressAllowlist) > adoptionMaxAllowlist || len(a.EgressPorts) > 65535 {
		return errors.New("adoption admission does not describe this lease")
	}
	if len(a.Drives) != 0 {
		return checkDrivePins(r, a.Drives)
	}
	return nil
}

// resourceAdoptionJob is what a successor needs to keep serving a job:
// whether its start gate and attach session exist, and its exit once the
// guest reported it. The exit is written before the guest is acknowledged,
// so a WaitJobExit cut short by the restart gets it from the successor.
type resourceAdoptionJob struct {
	Interactive bool            `json:"interactive,omitempty"`
	StartGate   bool            `json:"start_gate,omitempty"`
	Exit        *JobExitPayload `json:"exit,omitempty"`
}

// resourceAdoptionNetwork holds the nft rule handles the in-place policy
// patches delete by handle.
type resourceAdoptionNetwork struct {
	AllowlistHandleV4      uint64 `json:"allowlist_handle_v4,omitempty"`
	AllowlistHandleV6      uint64 `json:"allowlist_handle_v6,omitempty"`
	PrivateNetworkHandleV4 uint64 `json:"private_network_handle_v4,omitempty"`
	PrivateNetworkHandleV6 uint64 `json:"private_network_handle_v6,omitempty"`
	// EgressAllowlist is the app's applied egress allowlist (CIDRs); host
	// SMTP rules render from it. StaticEgressIP is the app's pinned egress
	// address, if any.
	EgressAllowlist []string `json:"egress_allowlist,omitempty"`
	StaticEgressIP  string   `json:"static_egress_ip,omitempty"`
	// RehearsalRunID is the run whose outbound containment the guest's
	// network carries (ADR-20261017201005). Adoption refuses such a guest:
	// the containment and the run's recorder are not rebuilt.
	RehearsalRunID string `json:"rehearsal_run_id,omitempty"`
	// EgressCircuits: the guest's netns carries ADR-570 egress-circuit sets.
	// Adoption refuses such a guest: its circuit registration is not rebuilt.
	EgressCircuits bool `json:"egress_circuits,omitempty"`
	// Private is the guest's attachment to a Gregale private network, when
	// it has one. The side-link names follow from the slot.
	Private *resourceAdoptionPrivateNetwork `json:"private,omitempty"`
}

// resourceAdoptionPrivateNetwork is what Wake put into netns.Config for a
// private-network member, so an adopting vmmd can patch its policy in place
// and tear the side-link down. The account bridge and transport are host
// state that schedd reconciles separately.
type resourceAdoptionPrivateNetwork struct {
	Bridge        string                             `json:"bridge"`
	Address       string                             `json:"address"`
	CIDRs         []string                           `json:"cidrs"`
	AllowedCIDRs  []string                           `json:"allowed_cidrs,omitempty"`
	FirewallRules []netns.PrivateNetworkFirewallRule `json:"firewall_rules,omitempty"`
}

type resourceAdoptionLifecycle struct {
	State       string     `json:"state"`
	FrozenSince *time.Time `json:"frozen_since,omitempty"`
	// Op is the one lifecycle operation in flight, if any.
	Op *resourceAdoptionOp `json:"op,omitempty"`
}

// resourceAdoptionOp is an operation intent. Artifacts are the storage keys
// the operation is creating, so a successor can discard partial ones.
type resourceAdoptionOp struct {
	Kind      string    `json:"kind"`
	Token     string    `json:"token"`
	Started   time.Time `json:"started"`
	Artifacts []string  `json:"artifacts,omitempty"`
}

func (a *resourceAdoption) clone() *resourceAdoption {
	if a == nil {
		return nil
	}
	c := *a
	c.Network.EgressAllowlist = append([]string(nil), a.Network.EgressAllowlist...)
	if a.Backing != nil {
		b := *a.Backing
		c.Backing = &b
	}
	c.Bridge = a.Bridge.clone()
	c.Instance.WorkloadNames = append([]string(nil), a.Instance.WorkloadNames...)
	c.Instance.LivenessProbe = append(json.RawMessage(nil), a.Instance.LivenessProbe...)
	c.Instance.ReadinessProbe = append(json.RawMessage(nil), a.Instance.ReadinessProbe...)
	if t := a.Instance.FrameworkReadyAt; t != nil {
		v := *t
		c.Instance.FrameworkReadyAt = &v
	}
	if t := a.Lifecycle.FrozenSince; t != nil {
		v := *t
		c.Lifecycle.FrozenSince = &v
	}
	c.Lifecycle.Op = a.Lifecycle.Op.clone()
	return &c
}

func (o *resourceAdoptionOp) clone() *resourceAdoptionOp {
	if o == nil {
		return nil
	}
	c := *o
	c.Artifacts = append([]string(nil), o.Artifacts...)
	return &c
}

func (o *resourceAdoptionOp) validate() error {
	if o == nil {
		return nil
	}
	switch o.Kind {
	case adoptionOpSnapshot, adoptionOpWarmSnapshot, adoptionOpPark, adoptionOpFreeze, adoptionOpThaw, adoptionOpStop:
	default:
		return fmt.Errorf("unknown adoption operation %q", o.Kind)
	}
	if o.Token == "" || !adoptionString(o.Token) || o.Started.IsZero() || len(o.Artifacts) > adoptionMaxOpArtifacts {
		return errors.New("invalid adoption operation intent")
	}
	for _, key := range o.Artifacts {
		if key == "" || !adoptionString(key) {
			return errors.New("invalid adoption operation artifact")
		}
	}
	return nil
}

func adoptionString(s string) bool {
	return len(s) <= adoptionMaxStringBytes && !containsControl(s)
}

func containsControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

func validAdoptionProbe(raw json.RawMessage) bool {
	return len(raw) == 0 || (len(raw) <= adoptionMaxProbeBytes && json.Valid(raw))
}

// validateAdoption is the strict reader for the block. The JSON decoder
// already rejects unknown fields; this rejects values a writer never emits.
func (r resourceJournalRecord) validateAdoption() error {
	a := r.Adoption
	if a == nil {
		return nil
	}
	if r.Version != resourceAdoptionVersion {
		return errors.New("adoption block requires journal version 7")
	}
	if r.preparedSpare() || r.Process == nil {
		return errors.New("adoption block requires a launched guest")
	}
	switch a.Kind {
	case adoptionKindApp, adoptionKindJob, adoptionKindAppTask, adoptionKindExecution, adoptionKindQualification, adoptionKindBuilder:
	default:
		return fmt.Errorf("unknown adoption kind %q", a.Kind)
	}
	f := a.Firecracker
	if a.ReadyAt.IsZero() || f.Exe.Inode == 0 || f.Version == "" || !adoptionString(f.Version) ||
		(f.Method != WakeColdBoot.String() && f.Method != WakeRestore.String()) ||
		f.VCPUs < 1 || f.VCPUs > 64 || f.MemoryMiB < 1 {
		return errors.New("invalid adoption firecracker identity")
	}
	if _, err := strconv.ParseUint(f.MemoryMax, 10, 64); err != nil {
		return errors.New("invalid adoption memory.max")
	}
	in := a.Instance
	for _, s := range []string{in.AppID, in.AccountID, in.DeploymentID, in.HealthcheckPath, in.ExecutionMode, in.Runtime, in.ObservedClass} {
		if !adoptionString(s) {
			return errors.New("invalid adoption instance string")
		}
	}
	if len(in.WorkloadNames) > adoptionMaxWorkloads || in.Port < 0 || in.Port > 65535 || in.StartupDeadlineS < 0 || in.QuietPauseAfterMs < 0 {
		return errors.New("invalid adoption instance configuration")
	}
	for _, name := range in.WorkloadNames {
		if name == "" || !adoptionString(name) {
			return errors.New("invalid adoption workload name")
		}
	}
	if !validAdoptionProbe(in.LivenessProbe) || !validAdoptionProbe(in.ReadinessProbe) {
		return errors.New("invalid adoption probe")
	}
	if in.Admission != nil {
		if !in.Admitted {
			return errors.New("adoption admission without the admitted mark")
		}
		if err := in.Admission.validate(r.Lease); err != nil {
			return err
		}
	}
	if (a.Kind == adoptionKindJob) != (in.Job != nil) {
		return errors.New("adoption job block does not match its kind")
	}
	if in.Job != nil && in.Job.Exit != nil {
		if err := validateJobExitPayload(*in.Job.Exit); err != nil {
			return fmt.Errorf("invalid adoption job exit: %w", err)
		}
	}
	n := a.Network
	if len(n.EgressAllowlist) > adoptionMaxAllowlist {
		return errors.New("adoption egress allowlist too long")
	}
	for _, p := range n.EgressAllowlist {
		if _, err := netip.ParsePrefix(p); err != nil {
			return errors.New("invalid adoption egress allowlist entry")
		}
	}
	if n.StaticEgressIP != "" {
		if _, err := netip.ParseAddr(n.StaticEgressIP); err != nil {
			return errors.New("invalid adoption static egress address")
		}
	}
	if !adoptionString(n.RehearsalRunID) {
		return errors.New("invalid adoption rehearsal run")
	}
	if err := n.Private.validate(); err != nil {
		return err
	}
	if b := a.Backing; b != nil && (!b.complete() || !adoptionString(b.Kernel) || !adoptionString(b.Base) || !adoptionString(b.Timer)) {
		return errors.New("invalid adoption backing identity")
	}
	if err := a.Bridge.validate(r.Lease); err != nil {
		return err
	}
	switch a.Lifecycle.State {
	case adoptionStateReady, adoptionStateStopping:
		if a.Lifecycle.FrozenSince != nil {
			return errors.New("unfrozen adoption state carries a freeze time")
		}
	case adoptionStateFrozen:
		if a.Lifecycle.FrozenSince == nil || a.Lifecycle.FrozenSince.IsZero() {
			return errors.New("frozen adoption state needs its freeze time")
		}
	default:
		return fmt.Errorf("unknown adoption lifecycle state %q", a.Lifecycle.State)
	}
	return a.Lifecycle.Op.validate()
}

// recordAdoption commits block onto the record of l, provided the record is
// still the one for the process the block was captured from. A record retired
// or relaunched in the meantime is left alone.
func (j *ResourceJournal) recordAdoption(l Lease, process resourceProcessIdentity, block resourceAdoption) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errResourceJournalClosed
	}
	r, ok := j.records[l.Instance]
	if !ok || !resourceLeaseMatches(r.Lease, l) || r.Process == nil || *r.Process != process {
		return errAdoptionRecordStale
	}
	r.Version = resourceAdoptionVersion
	r.Adoption = &block
	if err := r.validate(); err != nil {
		return err
	}
	// Durable: adoption writes run off the wake path, and an op intent names
	// the artifacts its effect creates on disk (vmmd restart adoption ADR §1).
	return j.persist(r, true)
}

var errAdoptionRecordStale = errors.New("resource journal: adoption block no longer matches the recorded guest")

// adoptionKind maps an Instance to its adoption kind.
func adoptionKind(inst *Instance) string {
	switch {
	case inst.QualificationOnly:
		return adoptionKindQualification
	case inst.ExecutionOnly:
		return adoptionKindExecution
	case inst.AgentRunOnly:
		// An agent run is one-shot like an execution: the RunAgent stream
		// that owns it ends with the predecessor, so a successor kills it
		// (ADR-20261027090010).
		return adoptionKindExecution
	case inst.AppTaskOnly:
		return adoptionKindAppTask
	case inst.IsJob:
		return adoptionKindJob
	case inst.Lease.IsBuilder:
		// A builder boots through Wake with an export directory; builderd
		// holds that call open, so its VM must never pass for an app.
		return adoptionKindBuilder
	default:
		return adoptionKindApp
	}
}

// adoptionBlockFor captures the block for a live instance. The caller holds
// m.mu. The Firecracker exe identity is filled in later, outside the lock.
func (m *Manager) adoptionBlockFor(inst *Instance, now time.Time) resourceAdoption {
	block := resourceAdoption{
		Kind:    adoptionKind(inst),
		ReadyAt: now.UTC(),
		Firecracker: resourceAdoptionFirecracker{
			Version:   m.fcVersion,
			Method:    inst.Method.String(),
			VCPUs:     inst.adoptionVCPUs,
			MemoryMiB: inst.adoptionMemSizeMiB,
			MemoryMax: fenceMemoryMaxFor(inst.Lease),
		},
		Instance: resourceAdoptionInstance{
			AppID:                    inst.AppID,
			AccountID:                inst.AccountID,
			WakeID:                   inst.WakeID,
			NodeID:                   inst.NodeID,
			DeploymentID:             inst.DeploymentID,
			Port:                     inst.Port,
			HealthcheckPath:          inst.HealthcheckPath,
			HealthcheckGRPC:          inst.HealthcheckGRPC,
			ImageHealthcheckRequired: inst.ImageHealthcheckRequired,
			StartupDeadlineS:         inst.StartupDeadlineS,
			ExecutionMode:            inst.ExecutionMode,
			Runtime:                  inst.Runtime,
			WorkloadNames:            append([]string(nil), inst.WorkloadNames...),
			LivenessProbe:            append(json.RawMessage(nil), inst.LivenessProbe...),
			ReadinessProbe:           append(json.RawMessage(nil), inst.ReadinessProbe...),
			QuietPauseAfterMs:        inst.QuietPauseAfter.Milliseconds(),
			WarmPaused:               inst.Paused,
			WarmSpare:                inst.warmSpare,
			DevPatched:               inst.devPatched,
			Admitted:                 inst.runtimeAdmissionReceipt.Binding.ProtocolVersion != 0 || m.runtimeAdmissionFlights[inst.Lease.Instance] != nil,
			MigrationPaused:          inst.migrationPaused,
			ObservedClass:            inst.Characterization.ObservedClass,
		},
		Network: resourceAdoptionNetwork{
			AllowlistHandleV4:      inst.AllowlistHandleV4,
			AllowlistHandleV6:      inst.AllowlistHandleV6,
			PrivateNetworkHandleV4: inst.PrivateNetworkHandleV4,
			PrivateNetworkHandleV6: inst.PrivateNetworkHandleV6,
			Private:                adoptionPrivateNetworkFor(inst.Net),
			RehearsalRunID:         inst.Net.RehearsalRunID,
			EgressCircuits:         inst.Net.EgressCircuitEnabled,
		},
		Lifecycle: resourceAdoptionLifecycle{State: adoptionStateReady},
	}
	if t := inst.FrameworkReadyAt; t != nil {
		v := t.UTC()
		block.Instance.FrameworkReadyAt = &v
	}
	// A guest whose admission is still in flight is admitted without a
	// receipt yet: a restart before finishAdmittedWake records one refuses
	// it (admission_receipt) instead of adopting it as unadmitted.
	if inst.runtimeAdmissionReceipt.Binding.ProtocolVersion != 0 {
		e := inst.runtimeAdmissionEgress
		block.Instance.Admission = &resourceAdoptionAdmission{
			Receipt: inst.runtimeAdmissionReceipt.Clone(), Plan: string(e.Plan),
			EgressAllowlist: slices.Clone(e.EgressAllowlist), EgressPorts: slices.Clone(e.EgressPorts),
		}
	}
	if block.Kind == adoptionKindJob {
		job := &resourceAdoptionJob{Interactive: inst.JobInteractive, StartGate: inst.JobStartGateEnabled}
		if e := inst.jobExit; e != nil {
			exit := *e
			job.Exit = &exit
		}
		block.Instance.Job = job
	}
	if inst.Frozen {
		since := inst.FrozenSince.UTC()
		block.Lifecycle = resourceAdoptionLifecycle{State: adoptionStateFrozen, FrozenSince: &since}
	}
	block.Lifecycle.Op = inst.adoptionOp.clone()
	block.Bridge = inst.bridge.clone()
	return block
}

// adoptionPrivateNetworkFor records a private-network attachment, or nil.
func adoptionPrivateNetworkFor(nc netns.Config) *resourceAdoptionPrivateNetwork {
	if nc.PrivateVethHost == "" || nc.PrivateNetworkBridge == "" {
		return nil
	}
	p := &resourceAdoptionPrivateNetwork{Bridge: nc.PrivateNetworkBridge, Address: nc.PrivateNetworkAddress.String()}
	for _, c := range nc.PrivateNetworkCIDRs {
		p.CIDRs = append(p.CIDRs, c.String())
	}
	for _, c := range nc.PrivateNetworkAllowedCIDRs {
		p.AllowedCIDRs = append(p.AllowedCIDRs, c.String())
	}
	p.FirewallRules = cloneNetnsPrivateNetworkFirewallRules(nc.PrivateNetworkFirewallRules)
	return p
}

// netConfig applies the attachment to nc for slot.
func (p *resourceAdoptionPrivateNetwork) netConfig(nc *netns.Config, slot int) error {
	address, err := netip.ParseAddr(p.Address)
	if err != nil {
		return err
	}
	cidrs, err := parseAdoptionPrefixes(p.CIDRs)
	if err != nil {
		return err
	}
	allowed, err := parseAdoptionPrefixes(p.AllowedCIDRs)
	if err != nil {
		return err
	}
	nc.PrivateNetworkBridge, nc.PrivateNetworkAddress = p.Bridge, address
	nc.PrivateNetworkCIDRs, nc.PrivateNetworkAllowedCIDRs = cidrs, allowed
	nc.PrivateNetworkFirewallRules = cloneNetnsPrivateNetworkFirewallRules(p.FirewallRules)
	nc.PrivateVethHost, nc.PrivateVethPeer = privateVethNames(slot)
	return nil
}

func parseAdoptionPrefixes(in []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range in {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// validate rejects an attachment a writer never emits: the netns renderer
// would refuse it too (privateNetworkEnabled).
func (p *resourceAdoptionPrivateNetwork) validate() error {
	if p == nil {
		return nil
	}
	if !strings.HasPrefix(p.Bridge, "gpn-") || !adoptionString(p.Bridge) || len(p.CIDRs) == 0 || len(p.CIDRs) > api.PrivateNetworkAttachmentMaxCIDRs ||
		len(p.AllowedCIDRs) > api.PrivateNetworkAttachmentMaxCIDRs || len(p.FirewallRules) > adoptionMaxAllowlist {
		return errors.New("invalid adoption private network")
	}
	var nc netns.Config
	if err := p.netConfig(&nc, 0); err != nil {
		return fmt.Errorf("invalid adoption private network: %w", err)
	}
	if !nc.PrivateNetworkAddress.Is4() || !nc.PrivateNetworkCIDRs[0].Addr().Is4() || !nc.PrivateNetworkCIDRs[0].Contains(nc.PrivateNetworkAddress) {
		return errors.New("invalid adoption private network address")
	}
	return nil
}

// adoptionPolicyFor fills the block fields that live under their own locks,
// outside Manager.mu.
func (m *Manager) adoptionPolicyFor(instance, appID string, block *resourceAdoption) {
	if appID != "" {
		m.perAppAllowlistMu.RLock()
		for _, p := range m.perAppAllowlist[appID] {
			block.Network.EgressAllowlist = append(block.Network.EgressAllowlist, p.String())
		}
		m.perAppAllowlistMu.RUnlock()
		m.perAppStaticIPMu.RLock()
		if ip := m.perAppStaticIP[appID]; ip != nil {
			block.Network.StaticEgressIP = ip.String()
		}
		m.perAppStaticIPMu.RUnlock()
	}
	m.backingMu.Lock()
	if b, ok := m.instanceBacking[instance]; ok {
		block.Backing = &b
	}
	m.backingMu.Unlock()
}

// fenceMemoryMaxFor is the memory.max newCgroupFence writes for l.
func fenceMemoryMaxFor(l Lease) string {
	if l.IsBuilder {
		return memoryMaxValue(api.BuilderMemoryMaxMB(l.MemoryMaxMiB))
	}
	return memoryMaxValue(api.BillableRAMMB(l.MemoryMaxMiB))
}

// adoptionWriter commits adoption blocks off the wake critical path. Marks
// coalesce per instance and one goroutine writes them, so a block computed
// later is never overwritten by one computed earlier.
type adoptionWriter struct {
	// commitMu orders commits: each computes its block and writes it while
	// holding it, so a block computed later is always written later.
	commitMu sync.Mutex
	// batchMu is held while a batch of marks is taken and written, so a
	// flush returns only after a batch the writer already took is written.
	batchMu sync.Mutex
	mu      sync.Mutex
	dirty   map[string]struct{}
	signal  chan struct{}
	procDir string
	now     func() time.Time
}

// EnableAdoptionRecords starts writing version-7 adoption blocks for ready
// instances. Turn it on only once every node that could read this journal,
// including a rollback target, accepts version 7.
func (m *Manager) EnableAdoptionRecords(ctx context.Context) error {
	if m.resourceJournal == nil {
		return errors.New("adoption records need the resource journal")
	}
	w := &adoptionWriter{dirty: make(map[string]struct{}), signal: make(chan struct{}, 1), procDir: "/proc", now: time.Now}
	if !m.adoption.CompareAndSwap(nil, w) {
		return nil
	}
	go m.runAdoptionWriter(ctx, w)
	return nil
}

// markAdoptionDirty schedules a block rewrite for a live instance after it
// reaches readiness or changes something the block records.
func (m *Manager) markAdoptionDirty(instance string) {
	w := m.adoption.Load()
	if w == nil {
		return
	}
	w.mu.Lock()
	w.dirty[instance] = struct{}{}
	w.mu.Unlock()
	select {
	case w.signal <- struct{}{}:
	default:
	}
}

func (m *Manager) runAdoptionWriter(ctx context.Context, w *adoptionWriter) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.signal:
		}
		m.writeAdoptionBatch(w)
	}
}

// writeAdoptionBatch writes every block marked so far.
func (m *Manager) writeAdoptionBatch(w *adoptionWriter) {
	w.batchMu.Lock()
	defer w.batchMu.Unlock()
	w.mu.Lock()
	batch := w.dirty
	w.dirty = make(map[string]struct{})
	w.mu.Unlock()
	for instance := range batch {
		if err := m.commitAdoption(instance, w); err != nil && !errors.Is(err, errAdoptionRecordStale) {
			// The record stays without a current block, so a restart
			// quarantines this guest as before. Nothing else depends on it.
			slog.Warn("vmmd: adoption record not committed", "instance", instance, "err", err)
		}
	}
}

// FlushAdoptionRecords writes every pending block before it returns. A
// planned stop calls it once its RPCs have ended, so a change made just
// before the stop (an operation's end, a policy patch, an attachment) is in
// the record the successor adopts from instead of waiting on the writer.
func (m *Manager) FlushAdoptionRecords() {
	if w := m.adoption.Load(); w != nil {
		m.writeAdoptionBatch(w)
	}
}

// commitAdoption writes the current block for instance. An instance that is
// no longer live, or whose record moved on, is skipped.
func (m *Manager) commitAdoption(instance string, w *adoptionWriter) error {
	w.commitMu.Lock()
	defer w.commitMu.Unlock()
	return m.commitAdoptionLocked(instance, w)
}

// commitAdoptionLocked is commitAdoption with w.commitMu held.
func (m *Manager) commitAdoptionLocked(instance string, w *adoptionWriter) error {
	m.mu.Lock()
	inst, live := m.live[instance]
	if !live || inst.Lease.Instance != instance {
		m.mu.Unlock()
		return errAdoptionRecordStale
	}
	lease := inst.Lease
	block := m.adoptionBlockFor(inst, w.now())
	m.mu.Unlock()
	r, ok, err := m.resourceJournal.lookup(instance)
	if err != nil {
		return err
	}
	if !ok || r.Process == nil {
		return errAdoptionRecordStale
	}
	if r.Adoption != nil {
		// Keep readiness from the first commit; later rewrites only change
		// lifecycle, policy and pause state.
		block.ReadyAt = r.Adoption.ReadyAt
	}
	m.adoptionPolicyFor(lease.Instance, block.Instance.AppID, &block)
	if c, ok := m.vmm.(interface{ consoleIsFile(string) bool }); ok {
		block.Firecracker.ConsoleFile = c.consoleIsFile(instance)
	}
	if adm := block.Instance.Admission; adm != nil && measuredColdBoot(adm.Receipt) {
		if p, ok := m.vmm.(interface {
			adoptionDrivePins(Lease) []resourceAdoptionDrivePin
		}); ok {
			if pins := p.adoptionDrivePins(lease); checkDrivePins(adm.Receipt, pins) == nil {
				adm.Drives = pins
			}
		}
	}
	exe, err := adoptionExeIdentity(w.procDir, *r.Process)
	if err != nil {
		return err
	}
	block.Firecracker.Exe = exe
	return m.resourceJournal.recordAdoption(lease, *r.Process, block)
}

// adoptionExeIdentity reads the device and inode of the recorded process's
// executable, after checking the PID still names that process.
func adoptionExeIdentity(procDir string, want resourceProcessIdentity) (resourceFileIdentity, error) {
	current, err := readResourceProcessIdentity(procDir, want.PID)
	if errors.Is(err, errResourceProcessAbsent) || (err == nil && current != want) {
		return resourceFileIdentity{}, errAdoptionRecordStale
	}
	if err != nil {
		return resourceFileIdentity{}, err
	}
	info, err := os.Stat(filepath.Join(procDir, strconv.Itoa(want.PID), "exe"))
	if err != nil {
		return resourceFileIdentity{}, fmt.Errorf("stat guest executable: %w", err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Ino == 0 {
		return resourceFileIdentity{}, errors.New("guest executable has no inode identity")
	}
	// A second read closes the window in which the PID could have been reused
	// between the identity check and the stat.
	if again, err := readResourceProcessIdentity(procDir, want.PID); err != nil || again != want {
		return resourceFileIdentity{}, errAdoptionRecordStale
	}
	return resourceFileIdentity{Device: uint64(st.Dev), Inode: st.Ino}, nil //nolint:unconvert // Dev is int32 on darwin
}

// MarkDevPatched records, before a developer live patch is served, that
// instance's source may diverge from its artifact (ADR-740). The mark joins
// the instance's adoption block: a committed block is rewritten now, and a
// block not yet committed is computed after the mark, because both happen
// under commitMu. An error means the mark is not durable and the patch must
// not be served. Without adoption records only the in-memory mark is set.
func (m *Manager) MarkDevPatched(instance string) error {
	w := m.adoption.Load()
	if w != nil {
		w.commitMu.Lock()
		defer w.commitMu.Unlock()
	}
	m.mu.Lock()
	inst := m.live[instance]
	if inst == nil {
		m.mu.Unlock()
		return fmt.Errorf("fcvm: mark dev patch %s: not live", instance)
	}
	inst.devPatched = true
	m.mu.Unlock()
	if w == nil {
		return nil
	}
	r, ok, err := m.resourceJournal.lookup(instance)
	if err != nil {
		return fmt.Errorf("fcvm: mark dev patch %s: %w", instance, err)
	}
	if !ok || r.Adoption == nil {
		return nil // The first commit computes the block after the mark.
	}
	if err := m.commitAdoptionLocked(instance, w); err != nil && !errors.Is(err, errAdoptionRecordStale) {
		return fmt.Errorf("fcvm: mark dev patch %s: %w", instance, err)
	}
	return nil
}

// recordMigrationPause marks a migration source paused for its lease owner
// and writes the adoption block at once. An error means the mark is not
// durable; the caller treats the capture as failed and resumes the guest.
// Without adoption records only the in-memory mark is set.
func (m *Manager) recordMigrationPause(instance string, inst *Instance) error {
	m.mu.Lock()
	inst.migrationPaused = true
	m.mu.Unlock()
	w := m.adoption.Load()
	if w == nil {
		return nil
	}
	if err := m.commitAdoption(instance, w); err != nil && !errors.Is(err, errAdoptionRecordStale) {
		m.mu.Lock()
		inst.migrationPaused = false
		m.mu.Unlock()
		return fmt.Errorf("record migration pause: %w", err)
	}
	return nil
}

// RecordJobExit keeps a job's exit with its instance and writes the adoption
// block, before the guest is acknowledged (JailerVMM.WithJobExitSink). The
// successor of a vmmd that stops now still answers WaitJobExit. A failed
// write keeps the exit in memory only: the guest is acknowledged anyway,
// because a retried frame would not make the journal writable.
func (m *Manager) RecordJobExit(instance string, payload JobExitPayload) {
	w := m.adoption.Load()
	if w != nil {
		w.commitMu.Lock()
		defer w.commitMu.Unlock()
	}
	m.mu.Lock()
	inst := m.live[instance]
	if inst == nil || !inst.IsJob {
		m.mu.Unlock()
		return
	}
	inst.jobExit = &payload
	m.mu.Unlock()
	if w == nil {
		return
	}
	if err := m.commitAdoptionLocked(instance, w); err != nil && !errors.Is(err, errAdoptionRecordStale) {
		slog.Warn("vmmd: job exit not recorded for adoption", "instance", instance, "err", err)
	}
}

// beginAdoptionOp records an operation intent before its effect starts and
// returns the function that clears it. The intent is written synchronously:
// a successor that finds no intent may assume no effect began. In shadow mode
// a failed write only logs; it never fails the operation. Without
// EnableAdoptionRecords both calls do nothing.
func (m *Manager) beginAdoptionOp(instance, kind string, artifacts ...string) (end func()) {
	w := m.adoption.Load()
	if w == nil {
		return func() {}
	}
	keys := make([]string, 0, len(artifacts))
	for _, key := range artifacts {
		if key != "" {
			keys = append(keys, key)
		}
	}
	op := &resourceAdoptionOp{Kind: kind, Token: rand.Text(), Started: w.now().UTC(), Artifacts: keys}
	m.mu.Lock()
	inst := m.live[instance]
	if inst == nil {
		m.mu.Unlock()
		return func() {}
	}
	inst.adoptionOp = op
	m.mu.Unlock()
	if err := m.commitAdoption(instance, w); err != nil && !errors.Is(err, errAdoptionRecordStale) {
		slog.Warn("vmmd: adoption operation intent not committed", "instance", instance, "op", kind, "err", err)
	}
	return func() {
		m.mu.Lock()
		if inst.adoptionOp == op {
			inst.adoptionOp = nil
		}
		m.mu.Unlock()
		m.markAdoptionDirty(instance)
	}
}
