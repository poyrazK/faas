package sched

// Sandbox sessions (ADR-20261027090013 §4). A session is admitted like an
// agent run, on the agent pool as KindAgentRun, and its VM lives as long as
// one RunSandbox call: schedd holds the reservation for the call and keeps no
// session state, so a restart leaves neither a VM nor a reservation behind.
// agentd owns the session, its idle timers and its end.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"github.com/onebox-faas/faas/pkg/wire"
)

// SandboxLayerKeyForArch is where imaged publishes the platform sandbox
// layer (storagekey.SandboxLayerForArch).
func SandboxLayerKeyForArch(arch string) string {
	return storagekey.SandboxLayerForArch(arch)
}

// SandboxAdmission is one session's machine, already checked against the
// account's plan by apid and agentd.
type SandboxAdmission struct {
	SandboxID     string
	AccountID     string
	Plan          api.Plan
	RAMMB         int
	VCPU          int
	CPUMillicores int
	// Network is the session's network mode (ADR-20261027090013 §6); empty
	// is none, what an agentd from before network modes sends.
	Network string
	// WorkspaceKey is the persisted workspace the session starts from
	// (ADR-20261027090013 step 7).
	WorkspaceKey string
	// ImageKey is the seller image's full-rootfs artifact the session
	// boots in place of the platform sandbox layer; vmmd checks its shape.
	ImageKey string
	// StartCmd, ReadyCmd and ReadyTimeout are a template session's commands.
	StartCmd     string
	ReadyCmd     string
	ReadyTimeout time.Duration
	// EgressAllow and EgressDeny are a web-mode session's domain policy
	// (ADR-20261117091026 §3); vmmd validates and enforces them.
	EgressAllow []string
	EgressDeny  []string
	// WebDomainLog asks vmmd for the session's domain log
	// (ADR-20261117091026 §4), relayed to the WebDomains hook.
	WebDomainLog bool
	// Browser boots a preset session with the sandbox browser running
	// (ADR-20261116182615 §1).
	Browser bool
	// Task makes the session a sandbox task (ADR-20261116182615 §3).
	Task *SandboxTask
	// Persistent sizes the session within the persistent shapes
	// (ADR-20261122132055 §5) instead of an agent run's.
	Persistent bool
	// Resume restores a paused persistent session from its pause capture
	// (ADR-20261122132055 §2), preferring the node that holds it.
	Resume *SandboxResume
	// Volume is a persistent session's workspace volume (ADR-20261122132055
	// §4); a zero GB has none.
	Volume SandboxVolume
	// Reattach takes over a persistent session's VM that kept running on
	// its node when its stream ended (ADR-20261122132055 §7).
	Reattach *SandboxReattach
}

// SandboxReattach names a running session VM: its node and instance.
type SandboxReattach struct {
	NodeID   string
	Instance string
}

// SandboxVolume is a session's workspace volume as vmmd prepares it: its
// size, its newest backup (empty: never backed up) and how often vmmd
// backs it up while it runs.
type SandboxVolume struct {
	GB          int
	BackupID    string
	BackupEvery time.Duration
	// NodeID holds the volume's newest copy; StoredBackupID is the newest
	// backup the shared store holds, which a resume restores once NodeID
	// has left the pool and taken its copy and unuploaded backups with it.
	NodeID         string
	StoredBackupID string
	// SourceSandboxID is a fork's source (ADR-20261122132055 §3): BackupID
	// is the source's backup, which the fork's volume starts from.
	SourceSandboxID string
}

// SandboxVolumeBackup is vmmd's report of a stored volume backup.
type SandboxVolumeBackup struct {
	ID            string
	SizeBytes     int64
	DataChunks    int
	UploadedBytes int64
	BackupMs      int64
	Incremental   bool
}

// SandboxResume is a paused session's capture and the node it was taken on.
// RequireNode: the capture is on that node alone (durability node), so the
// resume runs there or is refused for capacity and tried again. A node that
// left the agent pool cannot hold it any more: the resume then goes anywhere
// and cold-boots when the restore finds no capture.
type SandboxResume struct {
	Snapshot    AgentRunSnapshot
	NodeID      string
	RequireNode bool
	// Fork: the capture is another session's (ADR-20261122132055 §3); the
	// session starts from it whatever its kind. With RequireNode the shared
	// store does not hold it yet and only its node can restore it.
	Fork bool
	// ForkedFrom is the fork's source session, which its guest learns.
	ForkedFrom string
}

// SandboxForked is vmmd's answer to a fork: the published fork capture of
// a session that runs on.
type SandboxForked struct {
	Snapshot       AgentRunSnapshot
	VMStateBytes   int64
	ForkMs         int64
	HasBacking     bool
	VolumeBackupID string
}

// SandboxPaused is vmmd's answer to a pause: the published capture.
type SandboxPaused struct {
	Snapshot     AgentRunSnapshot
	VMStateBytes int64
	PauseMs      int64
	// HasBacking: the capture's backing identity was written (ADR-510).
	HasBacking bool
	// VolumeBackupID is the volume backup that matches the capture, or
	// empty for a session without a volume.
	VolumeBackupID string
}

// SandboxTask is a sandbox task as vmmd runs it.
type SandboxTask struct {
	Kind           string
	URL            string
	MaxChars       int
	Timeout        time.Duration
	MaxOutputBytes int64
}

// SandboxWebDomain is one entry of a web-mode session's domain log: a
// granted domain, or a refused one with why (ADR-20261117091026 §4).
type SandboxWebDomain struct {
	Domain  string
	Refused string
	At      time.Time
}

// SandboxBooted is vmmd's report of a booted session VM. Actions is set
// when the session takes browser actions (ADR-20261117065818 §2), and
// EgressPolicy when vmmd enforces the session's domain policy
// (ADR-20261117091026 §3).
type SandboxBooted struct {
	Method       string
	BootMs       int64
	Actions      bool
	EgressPolicy bool
	// Reattached: the running VM took the new stream (ADR-20261122132055
	// §7); nothing booted.
	Reattached bool
}

// SandboxAction is one browser action agentd relays to the session's VM
// (ADR-20261117065818 §2); vmmd validates it.
type SandboxAction struct {
	ID             string
	Kind           string
	URL            string
	Ref            int
	Text           string
	Submit         bool
	Key            string
	Direction      string
	MaxChars       int
	Timeout        time.Duration
	MaxOutputBytes int64
	// Screenshot has vmmd also store the viewport (ADR-20261117065818 §5).
	Screenshot bool
}

// SandboxActionDone is a browser action's end as vmmd reports it.
type SandboxActionDone struct {
	ActionID        string
	ExitCode        int
	Output          []byte
	TimedOut        bool
	Truncated       bool
	Error           string
	ScreenshotBytes int64
}

// SandboxTaskDone is a sandbox task's end as vmmd reports it.
type SandboxTaskDone struct {
	ExitCode  int
	Output    []byte
	TimedOut  bool
	Truncated bool
	Error     string
}

// network is the mode the session boots with.
func (a SandboxAdmission) network() string {
	if a.Network == "" {
		return api.SandboxNetworkNone
	}
	return a.Network
}

// SandboxSpec is the machine vmmd boots for one session.
type SandboxSpec struct {
	Instance        string
	SandboxID       string
	AccountID       string
	Plan            api.Plan
	BaseKey         string
	LayerKey        string
	VCPU            int
	MemSizeMiB      int
	CPUMillicores   int
	QuietPauseAfter time.Duration
	Network         string
	WorkspaceKey    string
	ImageKey        string
	StartCmd        string
	ReadyCmd        string
	ReadyTimeout    time.Duration
	// Browser has vmmd start the sandbox browser as the session's commands,
	// and makes the warm snapshot the browser shape's (ADR-20261116182615).
	Browser bool
	// Task is the session's sandbox task, if it is one.
	Task *SandboxTask
	// EgressAllow and EgressDeny are the session's domain policy.
	EgressAllow  []string
	EgressDeny   []string
	WebDomainLog bool
	// Snapshot restores the session from a warm snapshot of the same drives,
	// machine and network mode (ADR-20261027090013 §12).
	Snapshot *AgentRunSnapshot
	// SessionCapture marks Snapshot as the session's own pause capture
	// (ADR-20261122132055 §2).
	SessionCapture bool
	// ForkedFrom is the source of a fork restored from its fork capture.
	ForkedFrom string
	// Volume is the session's workspace volume.
	Volume SandboxVolume
	// Persistent: vmmd keeps the VM for a reattach when the stream is lost;
	// Reattach takes over such a VM instead of booting one
	// (ADR-20261122132055 §7).
	Persistent bool
	Reattach   bool
}

// SandboxControl is a request agentd sends a live session's VM: a freeze,
// a close, an answer to a workspace persist (ADR-20261027090013 step 7), or
// a browser action (ADR-20261117065818 §2).
type SandboxControl struct {
	Op              SandboxOp
	PersistGrant    *SandboxPersistGrant
	PersistRecorded *SandboxPersistRecorded
	Action          *SandboxAction
	// CaptureID names a pause's capture (ADR-20261122132055 §2).
	CaptureID string
}

// SandboxOp is what a SandboxControl asks.
type SandboxOp int

const (
	SandboxOpFreeze SandboxOp = iota + 1
	SandboxOpClose
	SandboxOpPersistGrant
	SandboxOpPersistRecorded
	SandboxOpAction
	SandboxOpPause
	// SandboxOpFork takes a fork capture CaptureID of the running session
	// (ADR-20261122132055 §3).
	SandboxOpFork
)

// SandboxFreeze and SandboxClose are the controls without a payload.
var (
	SandboxFreeze = SandboxControl{Op: SandboxOpFreeze}
	SandboxClose  = SandboxControl{Op: SandboxOpClose}
)

// SandboxPersistGrant is agentd's answer to a persist request: a reserved
// snapshot, or Refusal, a problem as JSON.
type SandboxPersistGrant struct {
	RequestID       string
	SnapshotID      string
	StorageKey      string
	MaxBytes        int64
	ExpiresAtUnixMs int64
	Refusal         []byte
}

// SandboxPersistDone is vmmd's report of a written archive, or Failed.
type SandboxPersistDone struct {
	RequestID  string
	SnapshotID string
	Bytes      int64
	SHA256     string
	Failed     bool
}

// SandboxPersistRecorded is agentd's record of a written archive.
type SandboxPersistRecorded struct {
	RequestID string
	Recorded  bool
}

// SandboxActivity is vmmd's report of a session's calls.
type SandboxActivity struct {
	CallsInFlight int
	LastCallEnded time.Time
}

// SandboxHooks observe a live session. Booted runs once; the others as vmmd
// reports them. A hook's error ends the session.
type SandboxHooks struct {
	Booted        func(ctx context.Context, b SandboxBooted) error
	Activity      func(ctx context.Context, a SandboxActivity) error
	Frozen        func(ctx context.Context) error
	FreezeRefused func(ctx context.Context, reason string) error
	Thawed        func(ctx context.Context) error
	// PersistRequested and PersistDone relay a workspace persist's events.
	PersistRequested func(ctx context.Context, requestID string) error
	PersistDone      func(ctx context.Context, d SandboxPersistDone) error
	// Usage relays vmmd's cumulative usage of the session VM
	// (ADR-20261027090013 step 11).
	Usage func(ctx context.Context, u AgentRunUsage) error
	// TaskDone relays a sandbox task's end (ADR-20261116182615 §3).
	TaskDone func(ctx context.Context, d SandboxTaskDone) error
	// ActionDone relays a browser action's end (ADR-20261117065818 §2).
	ActionDone func(ctx context.Context, d SandboxActionDone) error
	// WebDomains relays vmmd's domain log (ADR-20261117091026 §4).
	WebDomains func(ctx context.Context, d []SandboxWebDomain) error
	// Paused and PauseRefused relay vmmd's answers to a pause
	// (ADR-20261122132055 §2); the stream ends cleanly after Paused.
	Paused       func(ctx context.Context, p SandboxPaused) error
	PauseRefused func(ctx context.Context, reason string) error
	// Forked and ForkRefused relay vmmd's answers to a fork
	// (ADR-20261122132055 §3); the session runs on after either.
	Forked      func(ctx context.Context, f SandboxForked) error
	ForkRefused func(ctx context.Context, reason string) error
	// Evacuate asks the session's owner to move it off its node
	// (ADR-20261122132055 §7): the node is leaving the pool, or its vmmd is
	// stopping. It runs at most once per reason.
	Evacuate func(ctx context.Context, reason string) error
	// VolumeBackup relays a backup of the session's volume taken while it
	// runs (ADR-20261122132055 §4).
	VolumeBackup func(ctx context.Context, b SandboxVolumeBackup) error
}

type sandboxVMMClient interface {
	RunSandbox(context.Context, SandboxSpec, SandboxHooks, <-chan SandboxControl) error
}

// sandboxRouter is the VMMRouter surface sessions use.
type sandboxRouter interface {
	RunSandbox(ctx context.Context, nodeID string, spec SandboxSpec, hooks SandboxHooks, control <-chan SandboxControl) error
}

// placeVolume points a session with a volume at the node holding its newest
// copy (ADR-20261122132055 §4). Once that node left the pool, its copy and
// the backups only it held are gone: vmmd restores the newest stored backup
// instead of waiting for an upload that will never land.
func (r *AgentRunner) placeVolume(ctx context.Context, a *SandboxAdmission, req *Request) {
	if a.Volume.GB == 0 || a.Volume.NodeID == "" {
		return
	}
	if known, _ := r.poolNodeState(ctx, a.Volume.NodeID); !known {
		if a.Volume.BackupID != a.Volume.StoredBackupID {
			slog.Default().Warn("sched: sandbox volume node left the pool; restoring the newest stored backup",
				"sandbox_id", a.SandboxID, "node", a.Volume.NodeID, "backup_id", a.Volume.BackupID,
				"stored_backup_id", a.Volume.StoredBackupID)
		}
		a.Volume.BackupID = a.Volume.StoredBackupID
		return
	}
	if len(req.PreferredNodeIDs) == 0 {
		req.PreferredNodeIDs = []string{a.Volume.NodeID}
	}
}

// SandboxInstanceID names a session's VM; the session ID is a UUID.
func SandboxInstanceID(sandboxID string) string { return "sandbox-" + sandboxID }

// SandboxesEnabled reports whether this runner boots sandbox sessions.
func (r *AgentRunner) SandboxesEnabled() bool { return r != nil && r.cfg.SandboxLayerKey != "" }

// RunSandbox admits one session, boots it on the chosen node's vmmd, and
// keeps it until close, ctx's end or a broken stream, then releases the
// reservation. placed runs once the reservation is held, before the boot;
// its error ends the session unbooted. A clean close returns nil.
func (r *AgentRunner) RunSandbox(ctx context.Context, a SandboxAdmission, placed func(AgentRunPlacement) error, hooks SandboxHooks, control <-chan SandboxControl) error {
	if r.cfg.SandboxLayerKey == "" {
		return ErrAgentRunsDisabled
	}
	if err := a.validate(); err != nil {
		return err
	}
	if a.Reattach != nil {
		return r.reattachSandbox(ctx, a, placed, hooks, control)
	}
	req := Request{Instance: SandboxInstanceID(a.SandboxID), Plan: a.Plan, RAMMB: a.RAMMB, VCPU: a.VCPU, CPUMillicores: a.CPUMillicores}
	if a.Resume != nil && a.Resume.NodeID != "" {
		// The capture's node has it in its cache; any node restores it from
		// the shared store (ADR-20261122132055 §2). A draining node still
		// holds a node-only capture while it uploads: the resume waits for
		// the upload rather than cold-booting (§7).
		req.PreferredNodeIDs = []string{a.Resume.NodeID}
		if a.Resume.RequireNode && a.Resume.Fork {
			if _, active := r.poolNodeState(ctx, a.Resume.NodeID); !active {
				// A fork pinned to its capture's node has no capture
				// anywhere else yet: refuse it, and agentd waits for the
				// upload instead of booting a fork without its source's
				// memory.
				return api.NewProblem(503, api.CodeCapacity, "Capture node unavailable",
					"the fork's capture is only on node "+a.Resume.NodeID+", which is not taking sessions")
			}
		} else if a.Resume.RequireNode {
			switch known, active := r.poolNodeState(ctx, a.Resume.NodeID); {
			case !known:
				// The node is gone and its capture with it: the session
				// resumes cold wherever there is room.
				resume := *a.Resume
				resume.RequireNode = false
				a.Resume = &resume
			case !active:
				// A drain or a vmmd restart: the capture is still there,
				// so the resume waits for the node rather than resume cold
				// and drop it.
				return errCaptureNodeUnavailable(a.Resume.NodeID)
			}
		}
	}
	r.placeVolume(ctx, &a, &req)
	placement, release, err := r.admit(ctx, req)
	if err != nil {
		return err
	}
	if a.Resume != nil && a.Resume.RequireNode && placement.NodeID != a.Resume.NodeID {
		// Only that node holds the capture until its upload lands
		// (ADR-20261122132055 §2); refused before placed records a node
		// that will never run the session.
		release()
		return errCaptureNodeUnavailable(a.Resume.NodeID)
	}
	capturing := false
	defer func() {
		if !capturing {
			release()
		}
	}()
	if placed != nil {
		if err := placed(placement); err != nil {
			return err
		}
	}
	router, ok := r.router.(sandboxRouter)
	if !ok {
		return api.NewProblem(501, api.CodeNotImplemented, "Sandbox sessions unavailable", "the vmmd router does not support sandbox sessions")
	}
	spec := SandboxSpec{Instance: placement.Instance, SandboxID: a.SandboxID, AccountID: a.AccountID, Plan: a.Plan,
		BaseKey: r.cfg.BaseKey, LayerKey: r.cfg.SandboxLayerKey, VCPU: a.VCPU, MemSizeMiB: a.RAMMB,
		CPUMillicores: a.CPUMillicores, QuietPauseAfter: api.QuietPauseAfterMillis * time.Millisecond, Network: a.network(),
		WorkspaceKey: a.WorkspaceKey, ImageKey: a.ImageKey, StartCmd: a.StartCmd, ReadyCmd: a.ReadyCmd, ReadyTimeout: a.ReadyTimeout,
		Browser: a.Browser, Task: a.Task,
		EgressAllow: a.EgressAllow, EgressDeny: a.EgressDeny, WebDomainLog: a.WebDomainLog, Volume: a.Volume,
		Persistent: a.Persistent && a.Task == nil}
	if a.Resume != nil {
		snap := a.Resume.Snapshot
		spec.Snapshot, spec.SessionCapture = &snap, true
		if a.Resume.Fork {
			spec.ForkedFrom = a.Resume.ForkedFrom
		}
		// A resumed session is never a warm capture's proof, and its capture
		// is not the warm cache's to drop.
		return r.runResumedSandbox(ctx, router, placement, spec, hooks, control)
	}
	shape := r.sandboxShape(placement.NodeID, spec)
	spec.Snapshot = r.sandboxSnapshotFor(shape, spec)
	var method string
	booted := hooks.Booted
	hooks.Booted = func(ctx context.Context, b SandboxBooted) error {
		method = b.Method
		if booted != nil {
			return booted(ctx, b)
		}
		return nil
	}
	err = r.runSandboxWatched(ctx, router, placement.NodeID, spec, hooks, control)
	if spec.Snapshot != nil && method != "" && method != vmmdpb.WakeMethod_WAKE_RESTORE.String() {
		r.dropSandboxSnapshot(shape, spec.Snapshot)
	}
	if err != nil {
		return fmt.Errorf("sched: sandbox %s on node %s: %w", a.SandboxID, placement.NodeID, err)
	}
	// A session that cold-booted and ended cleanly proves the shape boots;
	// its capture runs under the session's reservation, which it releases.
	if method == vmmdpb.WakeMethod_WAKE_COLD_BOOT.String() && r.claimSandboxCapture(shape, spec) {
		capturing = true
		go r.captureSandbox(context.WithoutCancel(ctx), placement, spec, shape, release)
	}
	return nil
}

// runResumedSandbox runs a session restored from its pause capture.
func (r *AgentRunner) runResumedSandbox(ctx context.Context, router sandboxRouter, placement AgentRunPlacement, spec SandboxSpec,
	hooks SandboxHooks, control <-chan SandboxControl) error {
	if err := r.runSandboxWatched(ctx, router, placement.NodeID, spec, hooks, control); err != nil {
		return fmt.Errorf("sched: sandbox %s on node %s: %w", spec.SandboxID, placement.NodeID, err)
	}
	return nil
}

func (a SandboxAdmission) validate() error {
	idx := a.Plan.PlanIndex()
	ramMax, vcpuMax := api.AgentRunRAMMBMax[idx], api.AgentRunVCPUMax[idx]
	if a.Persistent {
		ramMax, vcpuMax = api.SandboxPersistentRAMMBMax[idx], api.SandboxPersistentVCPUMax[idx]
	}
	switch {
	case a.Resume != nil && (!a.Persistent && !a.Resume.Fork || a.WorkspaceKey != "" || a.Task != nil || a.Resume.Snapshot.StorageKey == "" ||
		a.Resume.RequireNode && a.Resume.NodeID == ""):
		return &AgentRunInvalidError{Reason: "only a persistent session or a fork resumes, from its capture and without a workspace archive or task"}
	case a.Volume.GB < 0 || a.Volume.GB > 0 && (!a.Persistent && !a.fork() || a.Task != nil || a.WorkspaceKey != "" || a.Volume.GB > api.SandboxVolumeMaxGB[idx]) ||
		a.Volume.GB == 0 && (a.Volume.BackupID != "" || a.Volume.BackupEvery != 0 || a.Volume.SourceSandboxID != "") ||
		a.Volume.SourceSandboxID != "" && (!a.fork() || a.Volume.BackupID == "") || !a.Persistent && a.Volume.BackupEvery != 0:
		return &AgentRunInvalidError{Reason: fmt.Sprintf("a %d GB volume is not a persistent session's or a fork's within the %s plan", a.Volume.GB, a.Plan)}
	case a.Reattach != nil && (!a.Persistent || a.Resume != nil || a.Reattach.NodeID == "" ||
		a.Reattach.Instance != SandboxInstanceID(a.SandboxID)):
		return &AgentRunInvalidError{Reason: "only a running persistent session reattaches, to its own instance on its node"}
	case a.Persistent && api.SandboxPersistentMax[idx] == 0:
		return &AgentRunInvalidError{Reason: fmt.Sprintf("the %s plan has no persistent sessions", a.Plan)}
	case a.SandboxID == "" || a.AccountID == "" || !a.Plan.Valid() || api.SandboxMaxLifetimeSec[idx] == 0:
		return &AgentRunInvalidError{Reason: "sandbox needs a session, an account and a plan with sessions"}
	case a.RAMMB < api.AgentRunRAMMBMin || a.RAMMB > ramMax || a.RAMMB%api.AgentRunRAMMBMin != 0:
		return &AgentRunInvalidError{Reason: fmt.Sprintf("sandbox RAM %d MB is outside the %s plan", a.RAMMB, a.Plan)}
	case a.VCPU < 1 || a.VCPU > vcpuMax || a.CPUMillicores < 1:
		return &AgentRunInvalidError{Reason: fmt.Sprintf("sandbox vCPU %d is outside the %s plan", a.VCPU, a.Plan)}
	case a.network() != api.SandboxNetworkNone && a.network() != api.SandboxNetworkRegistries && a.network() != api.SandboxNetworkWeb:
		return &AgentRunInvalidError{Reason: fmt.Sprintf("sandbox network mode %q is not supported", a.Network)}
	case a.Browser && (a.ImageKey != "" || a.StartCmd != "" || a.ReadyCmd != ""):
		return &AgentRunInvalidError{Reason: "the sandbox browser goes with a preset session, without commands of its own"}
	}
	return nil
}

// fork reports a session that starts from another session's fork capture.
func (a SandboxAdmission) fork() bool { return a.Resume != nil && a.Resume.Fork }

// RunSandbox routes one session to its node's vmmd.
// DeleteSandboxVolume removes a session's workspace volume from one node's
// vmmd (ADR-20261122132055 §4) and reports whether it held one.
func (r *AgentRunner) DeleteSandboxVolume(ctx context.Context, nodeID, accountID, sandboxID string) (bool, error) {
	router, ok := r.router.(sandboxVolumeRouter)
	if !ok {
		return false, api.NewProblem(501, api.CodeNotImplemented, "Sandbox volumes unavailable", "the vmmd router does not support sandbox volumes")
	}
	return router.DeleteSandboxVolume(ctx, nodeID, accountID, sandboxID)
}

type sandboxVolumeRouter interface {
	DeleteSandboxVolume(ctx context.Context, nodeID, accountID, sandboxID string) (bool, error)
}

type sandboxVolumeClient interface {
	DeleteSandboxVolume(ctx context.Context, accountID, sandboxID string) (bool, error)
}

// DeleteSandboxVolume asks nodeID's vmmd to remove a session's volume.
func (r *VMMRouter) DeleteSandboxVolume(ctx context.Context, nodeID, accountID, sandboxID string) (bool, error) {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return false, err
	}
	volumes, ok := cli.(sandboxVolumeClient)
	if !ok {
		return false, api.NewProblem(501, api.CodeNotImplemented, "Sandbox volumes unavailable", "vmmd client does not support sandbox volumes")
	}
	return volumes.DeleteSandboxVolume(ctx, accountID, sandboxID)
}

// DeleteSandboxVolume removes a session's volume from this vmmd's node.
func (c *VMMClient) DeleteSandboxVolume(ctx context.Context, accountID, sandboxID string) (bool, error) {
	if c == nil || c.cli == nil {
		return false, errors.New("sched: nil vmmd sandbox client")
	}
	fields, _ := wire.FromContext(ctx)
	resp, err := c.cli.DeleteSandboxVolume(wire.WithCorrelationOutgoing(ctx, fields),
		&vmmdpb.DeleteSandboxVolumeRequest{AccountId: accountID, SandboxId: sandboxID})
	if err != nil {
		return false, liftErr(err)
	}
	return resp.GetDeleted(), nil
}

func (r *VMMRouter) RunSandbox(ctx context.Context, nodeID string, spec SandboxSpec, hooks SandboxHooks, control <-chan SandboxControl) error {
	cli, err := r.resolveFor(ctx, nodeID)
	if err != nil {
		return err
	}
	sandboxClient, ok := cli.(sandboxVMMClient)
	if !ok {
		return api.NewProblem(501, api.CodeNotImplemented, "Sandbox sessions unavailable", "vmmd client does not support sandbox sessions")
	}
	return sandboxClient.RunSandbox(ctx, spec, hooks, control)
}

// RunSandbox boots one session VM on vmmd and relays control and reports
// until close or the stream's end; vmmd destroys the VM then.
func (c *VMMClient) RunSandbox(ctx context.Context, spec SandboxSpec, hooks SandboxHooks, control <-chan SandboxControl) error {
	if c == nil || c.cli == nil {
		return errors.New("sched: nil vmmd sandbox client")
	}
	fields, _ := wire.FromContext(ctx)
	streamCtx, stop := context.WithCancel(wire.WithCorrelationOutgoing(ctx, fields))
	defer stop()
	stream, err := c.cli.RunSandbox(streamCtx)
	if err != nil {
		return liftErr(err)
	}
	if err := stream.Send(&vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Start{Start: &vmmdpb.RunSandboxStart{
		Instance: spec.Instance, SandboxId: spec.SandboxID, AccountId: spec.AccountID, Plan: string(spec.Plan),
		BaseKey: spec.BaseKey, LayerKey: spec.LayerKey, VcpuCount: int32(spec.VCPU), MemSizeMib: int32(spec.MemSizeMiB),
		CpuMillicores: int32(spec.CPUMillicores), QuietPauseAfterMs: spec.QuietPauseAfter.Milliseconds(), NetworkMode: spec.Network,
		WorkspaceKey: spec.WorkspaceKey, ImageKey: spec.ImageKey, StartCmd: spec.StartCmd, ReadyCmd: spec.ReadyCmd,
		ReadyTimeoutMs: spec.ReadyTimeout.Milliseconds(), Snapshot: snapshotToProto(spec.Snapshot), Browser: spec.Browser,
		Task:        sandboxTaskToProto(spec.Task),
		EgressAllow: spec.EgressAllow, EgressDeny: spec.EgressDeny, WebDomainLog: spec.WebDomainLog,
		SessionCapture: spec.SessionCapture, ForkedFrom: spec.ForkedFrom,
		VolumeGb:       int32(spec.Volume.GB), //nolint:gosec // G115: bounded by the plan's volume cap.
		VolumeBackupId: spec.Volume.BackupID, VolumeBackupIntervalMs: spec.Volume.BackupEvery.Milliseconds(),
		VolumeSourceSandboxId: spec.Volume.SourceSandboxID,
		Detachable:            spec.Persistent, Reattach: spec.Reattach,
	}}}); err != nil {
		return liftErr(err)
	}
	closed := make(chan struct{})
	go func() {
		for {
			select {
			case <-streamCtx.Done():
				return
			case req, ok := <-control:
				if !ok {
					return
				}
				msg := sandboxControlFrame(req)
				if msg == nil {
					continue
				}
				if stream.Send(msg) != nil {
					return
				}
				if req == SandboxClose {
					close(closed)
					return
				}
			}
		}
	}()
	var paused bool
	hooks = withPausedMark(hooks, &paused)
	err = receiveSandbox(streamCtx, stream, hooks)
	select {
	case <-closed:
		if errors.Is(err, io.EOF) {
			return nil
		}
	default:
	}
	// vmmd ends the stream after paused: the VM is captured and gone
	// (ADR-20261122132055 §2).
	if paused && errors.Is(err, io.EOF) {
		return nil
	}
	if errors.Is(err, io.EOF) {
		return errors.New("sched: sandbox stream ended before close")
	}
	return err
}

func receiveSandbox(ctx context.Context, stream vmmdpb.Vmmd_RunSandboxClient, hooks SandboxHooks) error {
	call := func(hook func(context.Context) error) error {
		if hook == nil {
			return nil
		}
		return hook(ctx)
	}
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return io.EOF
		}
		if err != nil {
			return liftErr(err)
		}
		switch {
		case event.GetBooted() != nil:
			if hooks.Booted != nil {
				b := event.GetBooted()
				err = hooks.Booted(ctx, SandboxBooted{Method: b.GetMethod().String(), BootMs: b.GetBootMs(), Actions: b.GetActions(), EgressPolicy: b.GetEgressPolicy(),
					Reattached: b.GetReattached()})
			}
		case event.GetActivity() != nil:
			if hooks.Activity != nil {
				a := SandboxActivity{CallsInFlight: int(event.GetActivity().GetCallsInFlight())}
				if ms := event.GetActivity().GetLastCallEndedUnixMs(); ms > 0 {
					a.LastCallEnded = time.UnixMilli(ms)
				}
				err = hooks.Activity(ctx, a)
			}
		case event.GetFrozen() != nil:
			err = call(hooks.Frozen)
		case event.GetFreezeRefused() != nil:
			if hooks.FreezeRefused != nil {
				err = hooks.FreezeRefused(ctx, event.GetFreezeRefused().GetReason())
			}
		case event.GetThawed() != nil:
			err = call(hooks.Thawed)
		case event.GetPersistRequested() != nil:
			if hooks.PersistRequested != nil {
				err = hooks.PersistRequested(ctx, event.GetPersistRequested().GetRequestId())
			}
		case event.GetPersistDone() != nil:
			if hooks.PersistDone != nil {
				d := event.GetPersistDone()
				err = hooks.PersistDone(ctx, SandboxPersistDone{RequestID: d.GetRequestId(), SnapshotID: d.GetSnapshotId(),
					Bytes: d.GetBytes(), SHA256: d.GetSha256(), Failed: d.GetFailed()})
			}
		case event.GetUsage() != nil:
			if hooks.Usage != nil {
				err = hooks.Usage(ctx, agentRunUsageFromProto(event.GetUsage()))
			}
		case event.GetTaskDone() != nil:
			if hooks.TaskDone != nil {
				d := event.GetTaskDone()
				err = hooks.TaskDone(ctx, SandboxTaskDone{ExitCode: int(d.GetExitCode()), Output: d.GetOutput(), TimedOut: d.GetTimedOut(),
					Truncated: d.GetTruncated(), Error: d.GetError()})
			}
		case event.GetActionDone() != nil:
			if hooks.ActionDone != nil {
				d := event.GetActionDone()
				err = hooks.ActionDone(ctx, SandboxActionDone{ActionID: d.GetActionId(), ExitCode: int(d.GetExitCode()), Output: d.GetOutput(),
					TimedOut: d.GetTimedOut(), Truncated: d.GetTruncated(), Error: d.GetError(), ScreenshotBytes: d.GetScreenshotBytes()})
			}
		case event.GetWebDomains() != nil:
			if hooks.WebDomains != nil {
				err = hooks.WebDomains(ctx, sandboxWebDomainsFromProto(event.GetWebDomains()))
			}
		case event.GetPaused() != nil:
			if hooks.Paused != nil {
				p := event.GetPaused()
				s := p.GetSnapshot()
				err = hooks.Paused(ctx, SandboxPaused{Snapshot: AgentRunSnapshot{StorageKey: s.GetStorageKey(),
					VMStateStorageKey: s.GetVmstateStorageKey(), FCVersion: s.GetFcVersion(), MemBytes: s.GetMemBytes()},
					VMStateBytes: p.GetVmstateBytes(), PauseMs: p.GetPauseMs(), HasBacking: p.GetHasBacking(),
					VolumeBackupID: p.GetVolumeBackupId()})
			}
		case event.GetVolumeBackup() != nil:
			if hooks.VolumeBackup != nil {
				b := event.GetVolumeBackup()
				err = hooks.VolumeBackup(ctx, SandboxVolumeBackup{ID: b.GetBackupId(), SizeBytes: b.GetSizeBytes(),
					DataChunks: int(b.GetDataChunks()), UploadedBytes: b.GetUploadedBytes(), BackupMs: b.GetBackupMs(),
					Incremental: b.GetIncremental()})
			}
		case event.GetPauseRefused() != nil:
			if hooks.PauseRefused != nil {
				err = hooks.PauseRefused(ctx, event.GetPauseRefused().GetReason())
			}
		case event.GetForked() != nil:
			if hooks.Forked != nil {
				f := event.GetForked()
				s := f.GetSnapshot()
				err = hooks.Forked(ctx, SandboxForked{Snapshot: AgentRunSnapshot{StorageKey: s.GetStorageKey(),
					VMStateStorageKey: s.GetVmstateStorageKey(), FCVersion: s.GetFcVersion(), MemBytes: s.GetMemBytes()},
					VMStateBytes: f.GetVmstateBytes(), ForkMs: f.GetForkMs(), HasBacking: f.GetHasBacking(),
					VolumeBackupID: f.GetVolumeBackupId()})
			}
		case event.GetForkRefused() != nil:
			if hooks.ForkRefused != nil {
				err = hooks.ForkRefused(ctx, event.GetForkRefused().GetReason())
			}
		case event.GetEvacuate() != nil:
			if hooks.Evacuate != nil {
				err = hooks.Evacuate(ctx, event.GetEvacuate().GetReason())
			}
		default:
			return errors.New("sched: sandbox stream returned an empty event")
		}
		if err != nil {
			return err
		}
	}
}

// sandboxControlFrame is the vmmd frame for one control, or nil for one
// without a payload it needs.
func sandboxControlFrame(req SandboxControl) *vmmdpb.RunSandboxRequest {
	switch req.Op {
	case SandboxOpFreeze:
		return &vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Freeze{Freeze: &vmmdpb.RunSandboxFreeze{}}}
	case SandboxOpClose:
		return &vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Close{Close: &vmmdpb.RunSandboxClose{}}}
	case SandboxOpPersistGrant:
		if g := req.PersistGrant; g != nil {
			return &vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_PersistGrant{PersistGrant: &vmmdpb.RunSandboxPersistGrant{
				RequestId: g.RequestID, SnapshotId: g.SnapshotID, StorageKey: g.StorageKey, MaxBytes: g.MaxBytes,
				ExpiresAtUnixMs: g.ExpiresAtUnixMs, Refusal: g.Refusal}}}
		}
	case SandboxOpPersistRecorded:
		if r := req.PersistRecorded; r != nil {
			return &vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_PersistRecorded{PersistRecorded: &vmmdpb.RunSandboxPersistRecorded{
				RequestId: r.RequestID, Recorded: r.Recorded}}}
		}
	case SandboxOpPause:
		if req.CaptureID != "" {
			return &vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Pause{Pause: &vmmdpb.RunSandboxPause{CaptureId: req.CaptureID}}}
		}
	case SandboxOpFork:
		if req.CaptureID != "" {
			return &vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Fork{Fork: &vmmdpb.RunSandboxFork{CaptureId: req.CaptureID}}}
		}
	case SandboxOpAction:
		if a := req.Action; a != nil {
			return &vmmdpb.RunSandboxRequest{Frame: &vmmdpb.RunSandboxRequest_Action{Action: &vmmdpb.RunSandboxAction{
				ActionId: a.ID, Kind: a.Kind, Url: a.URL, Ref: int32(a.Ref), Text: a.Text, Submit: a.Submit, Key: a.Key,
				Direction: a.Direction, MaxChars: int32(a.MaxChars), TimeoutMs: a.Timeout.Milliseconds(), MaxOutputBytes: a.MaxOutputBytes,
				Screenshot: a.Screenshot}}}
		}
	}
	return nil
}

// withPausedMark sets *paused once vmmd reports the pause, so the stream's
// end after it is clean.
func withPausedMark(hooks SandboxHooks, paused *bool) SandboxHooks {
	inner := hooks.Paused
	hooks.Paused = func(ctx context.Context, p SandboxPaused) error {
		*paused = true
		if inner != nil {
			return inner(ctx, p)
		}
		return nil
	}
	return hooks
}

func sandboxWebDomainsFromProto(f *vmmdpb.RunSandboxWebDomains) []SandboxWebDomain {
	out := make([]SandboxWebDomain, 0, len(f.GetDomains()))
	for _, d := range f.GetDomains() {
		out = append(out, SandboxWebDomain{Domain: d.GetDomain(), Refused: d.GetRefused(), At: time.UnixMilli(d.GetAtUnixMs())})
	}
	return out
}

func sandboxTaskToProto(t *SandboxTask) *vmmdpb.RunSandboxTask {
	if t == nil {
		return nil
	}
	return &vmmdpb.RunSandboxTask{Kind: t.Kind, Url: t.URL, MaxChars: int32(t.MaxChars), TimeoutMs: t.Timeout.Milliseconds(),
		MaxOutputBytes: t.MaxOutputBytes}
}

// poolNodeState reports whether the node is still one of this pool's, and
// whether it takes work now. A node the store cannot read now counts as
// known and inactive: only a node that is gone is given up on.
func (r *AgentRunner) poolNodeState(ctx context.Context, nodeID string) (known, active bool) {
	n, ok := r.engine.breakerNode(ctx, nodeID)
	if !ok {
		if _, err := r.engine.store.ComputeNodeByID(ctx, nodeID); err != nil && !errors.Is(err, state.ErrNotFound) {
			return true, false
		}
		return false, false
	}
	if !r.pool[n.ID] && !r.pool[n.Name] {
		return false, false
	}
	return true, n.Active
}

// errCaptureNodeUnavailable refuses a resume whose capture only its node
// holds while that node cannot take it; the resume is retried.
func errCaptureNodeUnavailable(nodeID string) error {
	return api.NewProblem(503, api.CodeCapacity, "Capture node busy",
		"the paused session's capture is only on node "+nodeID+", which cannot take it now")
}
