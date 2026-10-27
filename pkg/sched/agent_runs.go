package sched

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"

	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ErrAgentRunsDisabled means this schedd does not admit agent runs: its
// agent_runs switch is off or it names no agent-pool node (ADR-20261027090010 §1).
var ErrAgentRunsDisabled = errors.New("sched: agent runs are not enabled on this schedd")

// AgentRunBaseRuntime is the agent base's name in the runtime base key
// space: imaged stages images/agent-base at BaseKeyForArch(AgentRunBaseRuntime,
// arch), with the scan sidecar vmmd's admission gate reads (issue #299).
const AgentRunBaseRuntime = "agent"

// AgentRunLayerKeyForArch is where imaged publishes the platform agent
// layer, drive1 holding only the agent marker (ADR-20261027090010 §2). The version
// names the layer's content; a new layout takes a new key.
func AgentRunLayerKeyForArch(arch string) string {
	return "base/agent-layer-v1-" + arch + ".ext4"
}

// AgentRunConfig is how this schedd runs agents: the release-pinned agent
// base (drive0) and platform agent layer (drive1), and the nodes that form
// the agent pool. Until compute_nodes carries the agents role (ADR-20261027090014 §2)
// the pool is this list of node IDs or names.
type AgentRunConfig struct {
	BaseKey  string
	LayerKey string
	Nodes    []string
	// Snapshots restores runs from a preset snapshot of their node and
	// machine shape, captured after the shape's first cold-boot run there
	// (ADR-20261027090010 §2). Off: every run cold-boots.
	Snapshots bool
}

// AgentRunAdmission is one run's machine (ADR-20261027090010 §5), already checked
// against the account's plan by agentd.
type AgentRunAdmission struct {
	RunID         string
	AccountID     string
	Plan          api.Plan
	RAMMB         int
	VCPU          int
	CPUMillicores int
	// Relay is the run's relay principal; nil gives the run no relay.
	Relay *AgentRunRelay
	// InputKey is the run's input drive; empty: none.
	InputKey string
}

// AgentRunRelay is the placement data vmmd mints a run's relay assertion
// from (ADR-20261027090010 §7, ADR-20261027090016). agentd's lease token fences it.
type AgentRunRelay struct {
	RunID        string
	LeaseToken   string
	TenantID     string
	AgentID      string
	AgentVersion int
}

// Valid reports whether every identifier is well formed.
func (r AgentRunRelay) Valid() bool {
	for _, id := range []string{r.RunID, r.LeaseToken, r.AgentID} {
		if _, err := uuid.Parse(id); err != nil {
			return false
		}
	}
	if r.TenantID != "" {
		if _, err := uuid.Parse(r.TenantID); err != nil {
			return false
		}
	}
	return r.AgentVersion >= 1
}

// AgentRunPlacement is where a run was admitted.
type AgentRunPlacement struct {
	Instance string
	NodeID   string
}

// agentRunRouter is the VMMRouter surface the runner uses.
type agentRunRouter interface {
	RunAgent(ctx context.Context, nodeID string, spec AgentRunSpec, run agentrunproto.Request, hooks AgentRunHooks, cancel <-chan struct{}) (agentrunproto.Result, error)
	CaptureAgentSnapshot(ctx context.Context, nodeID string, spec AgentRunSpec, namespace, captureID string) (*AgentRunSnapshot, error)
}

// agentSnapshotShape is what a preset snapshot restores: one node's
// machine of one size (the image is the runner's).
type agentSnapshotShape struct {
	node                   string
	vcpu, memMiB, cpuMilli int
}

// agentSnapshotNamespace is the storage segment of preset snapshot keys.
const agentSnapshotNamespace = "agent-preset"

// agentSnapshotCaptureTimeout bounds one capture: a cold boot, guest-init
// and the memory, state and drive writes.
const agentSnapshotCaptureTimeout = 3 * time.Minute

// AgentRunner admits agent runs on the agent pool and runs them through the
// node's vmmd. It holds no run state: agentd owns agent_runs, and the VM
// lives only as long as the RunAgent call, so a schedd restart cannot leave
// a run VM or a reservation behind.
type AgentRunner struct {
	engine *Engine
	router agentRunRouter
	cfg    AgentRunConfig
	pool   map[string]bool

	// snapshots and capturing are per schedd process: a restart captures
	// again, overwriting the same keys, and a stale snapshot is dropped
	// when vmmd cold-boots instead of restoring it.
	snapMu    sync.Mutex
	snapshots map[agentSnapshotShape]*AgentRunSnapshot
	capturing map[agentSnapshotShape]bool
}

// NewAgentRunner returns a runner, or ErrAgentRunsDisabled when the config
// names no base, layer or agent-pool node.
func NewAgentRunner(engine *Engine, router agentRunRouter, cfg AgentRunConfig) (*AgentRunner, error) {
	pool := map[string]bool{}
	for _, node := range cfg.Nodes {
		if node = strings.TrimSpace(node); node != "" {
			pool[node] = true
		}
	}
	if engine == nil || router == nil || cfg.BaseKey == "" || cfg.LayerKey == "" || len(pool) == 0 {
		return nil, ErrAgentRunsDisabled
	}
	return &AgentRunner{engine: engine, router: router, cfg: cfg, pool: pool,
		snapshots: map[agentSnapshotShape]*AgentRunSnapshot{}, capturing: map[agentSnapshotShape]bool{}}, nil
}

// Run admits one agent run, streams it on the chosen node's vmmd, and
// releases the reservation when vmmd has destroyed the VM. placed runs once
// the reservation is held, before the boot; returning an error there ends
// the run without booting it.
func (r *AgentRunner) Run(ctx context.Context, a AgentRunAdmission, run agentrunproto.Request, placed func(AgentRunPlacement) error, hooks AgentRunHooks, cancel <-chan struct{}) (agentrunproto.Result, error) {
	if err := a.validate(); err != nil {
		return agentrunproto.Result{}, err
	}
	placement, release, err := r.admit(ctx, a)
	if err != nil {
		return agentrunproto.Result{}, err
	}
	capturing := false
	defer func() {
		if !capturing {
			release()
		}
	}()
	if placed != nil {
		if err := placed(placement); err != nil {
			return agentrunproto.Result{}, err
		}
	}
	shape := agentSnapshotShape{node: placement.NodeID, vcpu: a.VCPU, memMiB: a.RAMMB, cpuMilli: a.CPUMillicores}
	spec := AgentRunSpec{Instance: placement.Instance, AccountID: a.AccountID, Plan: a.Plan, BaseKey: r.cfg.BaseKey,
		LayerKey: r.cfg.LayerKey, VCPU: a.VCPU, MemSizeMiB: a.RAMMB, CPUMillicores: a.CPUMillicores, Relay: a.Relay,
		InputKey: a.InputKey, Snapshot: r.snapshotFor(shape)}
	var method string
	booted := hooks.Booted
	hooks.Booted = func(ctx context.Context, m string, bootMs int64) error {
		method = m
		if booted != nil {
			return booted(ctx, m, bootMs)
		}
		return nil
	}
	result, err := r.router.RunAgent(ctx, placement.NodeID, spec, run, hooks, cancel)
	if spec.Snapshot != nil && method != "" && method != vmmdpb.WakeMethod_WAKE_RESTORE.String() {
		r.dropSnapshot(shape, spec.Snapshot)
	}
	if err != nil {
		return agentrunproto.Result{}, fmt.Errorf("sched: agent run %s on node %s: %w", a.RunID, placement.NodeID, err)
	}
	if method == vmmdpb.WakeMethod_WAKE_COLD_BOOT.String() && r.claimCapture(shape) {
		// The capture runs under this run's reservation, which it releases.
		capturing = true
		go r.capture(context.WithoutCancel(ctx), placement, a.Plan, shape, release)
	}
	return result, nil
}

func (r *AgentRunner) snapshotFor(shape agentSnapshotShape) *AgentRunSnapshot {
	r.snapMu.Lock()
	defer r.snapMu.Unlock()
	return r.snapshots[shape]
}

func (r *AgentRunner) dropSnapshot(shape agentSnapshotShape, snap *AgentRunSnapshot) {
	r.snapMu.Lock()
	defer r.snapMu.Unlock()
	if r.snapshots[shape] == snap {
		delete(r.snapshots, shape)
	}
}

// claimCapture reports whether this caller should capture the shape's
// snapshot: snapshots are on, the shape has none, and no capture runs.
func (r *AgentRunner) claimCapture(shape agentSnapshotShape) bool {
	if !r.cfg.Snapshots {
		return false
	}
	r.snapMu.Lock()
	defer r.snapMu.Unlock()
	if r.snapshots[shape] != nil || r.capturing[shape] {
		return false
	}
	r.capturing[shape] = true
	return true
}

// capture asks the node's vmmd for the shape's preset snapshot. Its keys
// are fixed per image and shape, so a later capture replaces this one's
// objects instead of adding more.
func (r *AgentRunner) capture(ctx context.Context, p AgentRunPlacement, plan api.Plan, shape agentSnapshotShape, release func()) {
	defer release()
	ctx, cancel := context.WithTimeout(ctx, agentSnapshotCaptureTimeout)
	defer cancel()
	spec := AgentRunSpec{Instance: p.Instance, Plan: plan, BaseKey: r.cfg.BaseKey, LayerKey: r.cfg.LayerKey,
		VCPU: shape.vcpu, MemSizeMiB: shape.memMiB, CPUMillicores: shape.cpuMilli}
	snap, err := r.router.CaptureAgentSnapshot(ctx, p.NodeID, spec, agentSnapshotNamespace, r.captureID(shape))
	r.snapMu.Lock()
	delete(r.capturing, shape)
	if err == nil {
		r.snapshots[shape] = snap
	}
	r.snapMu.Unlock()
	if err != nil {
		slog.Default().Warn("sched: agent run snapshot capture failed", "node", p.NodeID, "vcpu", shape.vcpu, "mem_mib", shape.memMiB, "err", err)
	}
}

// captureID names a shape's capture keys from the image and the shape.
func (r *AgentRunner) captureID(shape agentSnapshotShape) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%d", r.cfg.BaseKey, r.cfg.LayerKey, shape.vcpu, shape.memMiB, shape.cpuMilli)))
	return hex.EncodeToString(sum[:16])
}

func (r *AgentRunner) admit(ctx context.Context, a AgentRunAdmission) (AgentRunPlacement, func(), error) {
	req := Request{
		Instance: AgentRunInstanceID(a.RunID), Plan: a.Plan, RAMMB: a.RAMMB, VCPU: a.VCPU,
		CPUMillicores: a.CPUMillicores, Kind: KindAgentRun, agentPool: r.pool,
	}
	placement, err := r.engine.choosePlacementLocked(ctx, req)
	if err != nil {
		return AgentRunPlacement{}, nil, fmt.Errorf("sched: agent run placement: %w", err)
	}
	req.NodeID, req.NodeCeilingMB = placement.NodeID, placement.CeilingMB
	req.VCPUBudget, req.CPUBudgetMillicores = placement.VCPUBudget, placement.CPUBudgetMillicores
	if err := r.engine.ledger.Admit(req); err != nil {
		return AgentRunPlacement{}, nil, fmt.Errorf("sched: agent run admit: %w", err)
	}
	return AgentRunPlacement{Instance: req.Instance, NodeID: placement.NodeID}, func() { r.engine.ledger.Release(req.Instance) }, nil
}

// AgentRunInvalidError is an admission the plan refuses; agentd fails the
// run rather than queueing it again.
type AgentRunInvalidError struct{ Reason string }

func (e *AgentRunInvalidError) Error() string { return "sched: agent run " + e.Reason }

func (a AgentRunAdmission) validate() error {
	idx := a.Plan.PlanIndex()
	switch {
	case a.RunID == "" || a.AccountID == "" || !a.Plan.Valid() || a.Plan == api.PlanFree:
		return &AgentRunInvalidError{Reason: "needs a run, an account and a paid plan"}
	case a.RAMMB < api.AgentRunRAMMBMin || a.RAMMB > api.AgentRunRAMMBMax[idx] || a.RAMMB%api.AgentRunRAMMBMin != 0:
		return &AgentRunInvalidError{Reason: fmt.Sprintf("RAM %d MB is outside the %s plan", a.RAMMB, a.Plan)}
	case a.VCPU < 1 || a.VCPU > api.AgentRunVCPUMax[idx] || a.CPUMillicores < 1:
		return &AgentRunInvalidError{Reason: fmt.Sprintf("vCPU %d is outside the %s plan", a.VCPU, a.Plan)}
	}
	return nil
}

// AgentRunInstanceID names a run's VM. The jailer accepts the result: the
// run ID is a UUID, so it is short and of letters, digits and hyphens.
func AgentRunInstanceID(runID string) string { return "agent-" + runID }

// agentPoolNodes keeps the nodes of the agent pool.
func agentPoolNodes(nodes []state.ComputeNode, pool map[string]bool) []state.ComputeNode {
	out := make([]state.ComputeNode, 0, len(nodes))
	for _, node := range nodes {
		if pool[node.ID] || pool[node.Name] {
			out = append(out, node)
		}
	}
	return out
}
