// adr: 20261106214916

package sched

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// Snapshot refresh pass outcomes, one per candidate app
// (schedd_snapshot_refresh_total{outcome}).
// SnapshotRefreshDetailVCPUs is a would_refresh candidate's detail when its
// snapshot was captured at another guest vCPU count than its app boots with
// now: vmmd would refuse the restore (fcvm.ErrSnapshotVCPUMismatch) and the
// wake would cold-boot (ADR-20261109172421).
const SnapshotRefreshDetailVCPUs = "vcpu_count_changed"

const (
	// SnapshotRefreshWouldRefresh: the app's snapshot on the node is stale;
	// a shadow pass counts it, an on pass would refresh it.
	SnapshotRefreshWouldRefresh = "would_refresh"
	// SnapshotRefreshCurrent: a wake on the node would restore the snapshot.
	SnapshotRefreshCurrent = "current"
	// SnapshotRefreshUnknown: the check could not tell; a wake decides.
	SnapshotRefreshUnknown = "unknown"
	// SnapshotRefreshNoSnapshot: the app has no live init snapshot on the node.
	SnapshotRefreshNoSnapshot = "no_snapshot"
	// SnapshotRefreshSkippedLive: the app runs; it is recaptured when it parks.
	SnapshotRefreshSkippedLive = "skipped_live"
	// SnapshotRefreshSkippedIneligible: the account, app or deployment may
	// not be booted by the platform now.
	SnapshotRefreshSkippedIneligible = "skipped_ineligible"
)

var snapshotRefreshOutcomes = []string{
	SnapshotRefreshWouldRefresh, SnapshotRefreshCurrent, SnapshotRefreshUnknown,
	SnapshotRefreshNoSnapshot, SnapshotRefreshSkippedLive, SnapshotRefreshSkippedIneligible,
	SnapshotRefreshRefreshed, SnapshotRefreshDeferred, SnapshotRefreshFailed,
}

// snapshotRefreshInterval is how often the refresher looks for nodes whose
// release changed.
const snapshotRefreshInterval = time.Minute

// snapshotBackingRouter is the part of the routed VMM a pass asks.
type snapshotBackingRouter interface {
	SnapshotBackingCurrent(ctx context.Context, nodeID string, snap SnapshotRef, baseKey string) (SnapshotBackingVerdict, string, error)
}

// SnapshotRefresher finds, after a node takes a release, the recently active
// apps whose snapshots on that node the release made stale, and refreshes
// them (ADR-20261106214916). It runs one pass per node per release it sees,
// including once per node when schedd starts, since schedd restarts during a
// release, and again when the CPU shape settings change the guest vCPU count
// apps boot with (ADR-20261101160231). A pass refreshes a snapshot captured
// at another vCPU count than its app boots with now, and asks the node's vmmd
// whether each other candidate's snapshot would restore. In shadow it only counts the answers; on, it boots each
// stale one on the node, captures a fresh snapshot and parks it, one at a
// time per node, while the node keeps its headroom.
type SnapshotRefresher struct {
	e       *Engine
	log     *slog.Logger
	now     func() time.Time
	metrics *snapshotRefreshMetrics

	mu          sync.Mutex
	mode        string
	windowHours int
	// passed is the release and CPU shape each node's last completed pass
	// was for.
	passed map[string]string
}

// NewSnapshotRefresher builds a refresher over e, off until SetMode turns it
// on. reg may be nil.
func NewSnapshotRefresher(e *Engine, reg prometheus.Registerer, log *slog.Logger) *SnapshotRefresher {
	if log == nil {
		log = slog.Default()
	}
	return &SnapshotRefresher{
		e: e, log: log, now: time.Now, metrics: newSnapshotRefreshMetrics(reg),
		mode: api.DefaultSnapshotRefresh, windowHours: api.DefaultSnapshotRefreshWindowHours,
		passed: map[string]string{},
	}
}

// SetMode applies `snapshot_refresh`.
func (r *SnapshotRefresher) SetMode(mode string) error {
	if !api.ValidSnapshotRefresh(mode) {
		return fmt.Errorf("sched: snapshot_refresh: %q is not off, shadow or on", mode)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mode = mode
	return nil
}

// SetWindowHours applies `snapshot_refresh_window_hours`.
func (r *SnapshotRefresher) SetWindowHours(hours int) error {
	if !api.ValidSnapshotRefreshWindowHours(hours) {
		return fmt.Errorf("sched: snapshot_refresh_window_hours: %d is outside %d..%d", hours, api.SnapshotRefreshWindowHoursMin, api.SnapshotRefreshWindowHoursMax)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.windowHours = hours
	return nil
}

func (r *SnapshotRefresher) settings() (string, time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mode, time.Duration(r.windowHours) * time.Hour
}

// Run ticks until ctx ends.
func (r *SnapshotRefresher) Run(ctx context.Context) error {
	ticker := time.NewTicker(snapshotRefreshInterval)
	defer ticker.Stop()
	for {
		r.Tick(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Tick runs a pass for each active node whose release it has not passed yet.
// A node whose pass could not finish (its vmmd did not answer) is tried again
// on the next tick.
func (r *SnapshotRefresher) Tick(ctx context.Context) {
	mode, _ := r.settings()
	if mode == api.SnapshotRefreshOff {
		return
	}
	nodes, err := r.e.store.ListComputeNodes(ctx, false)
	if err != nil {
		r.log.Warn("snapshot refresh: list nodes", "err", err)
		return
	}
	var wg sync.WaitGroup
	for _, node := range nodes {
		if !node.Active || ctx.Err() != nil {
			continue
		}
		wg.Add(1)
		go func(node state.ComputeNode) {
			defer wg.Done()
			r.tickNode(ctx, node)
		}(node)
	}
	wg.Wait()
}

// tickNode runs nodeID's pass when its release or the mode has not been
// passed yet: turning shadow into on refreshes what the shadow pass found
// without waiting for the next release. A pass
// that deferred a refresh, or whose vmmd did not answer, runs again on the
// next tick.
func (r *SnapshotRefresher) tickNode(ctx context.Context, node state.ComputeNode) {
	r.reapOrphanedRefreshes(ctx, node.ID)
	release := ""
	if node.ReleaseID != nil {
		release = *node.ReleaseID
	}
	mode, _ := r.settings()
	key := release + "\x00" + mode
	r.mu.Lock()
	done, seen := r.passed[node.ID]
	r.mu.Unlock()
	if seen && done == key {
		return
	}
	pass, err := r.Pass(ctx, node.ID, release)
	if err != nil {
		r.log.Warn("snapshot refresh: pass did not finish; retrying next tick", "node_id", node.ID, "release_id", release, "err", err)
		return
	}
	if pass.Outcomes[SnapshotRefreshDeferred] > 0 {
		return
	}
	r.mu.Lock()
	r.passed[node.ID] = key
	r.mu.Unlock()
}

// SnapshotRefreshPass is one node's pass: candidates by outcome.
type SnapshotRefreshPass struct {
	NodeID     string
	ReleaseID  string
	Candidates int
	Outcomes   map[string]int
}

// Pass checks nodeID's candidates. It returns an error when it cannot list
// them or the node's vmmd does not answer, so the caller tries again; a vmmd
// that predates the check ends the pass for this release.
func (r *SnapshotRefresher) Pass(ctx context.Context, nodeID, releaseID string) (SnapshotRefreshPass, error) {
	mode, window := r.settings()
	pass := SnapshotRefreshPass{NodeID: nodeID, ReleaseID: releaseID, Outcomes: map[string]int{}}
	reader, ok := r.e.store.(state.SnapshotRefreshCandidateReader)
	if !ok {
		return pass, nil
	}
	cands, err := reader.ListSnapshotRefreshCandidates(ctx, nodeID, r.now().Add(-window), api.SnapshotRefreshPassMaxApps)
	if err != nil {
		return pass, fmt.Errorf("sched: snapshot refresh: list candidates: %w", err)
	}
	for _, c := range cands {
		outcome, detail, target, err := r.classify(ctx, nodeID, c)
		if errors.Is(err, ErrSnapshotBackingUnsupported) {
			r.log.Info("snapshot refresh: node's vmmd predates the backing check; pass ends", "node_id", nodeID, "release_id", releaseID)
			break
		}
		if err != nil {
			return pass, err
		}
		if outcome == "" {
			continue // another scheduler owns the app
		}
		if outcome == SnapshotRefreshWouldRefresh && mode == api.SnapshotRefreshOn && ctx.Err() == nil {
			outcome = r.refresh(ctx, nodeID, target)
		}
		pass.Candidates++
		pass.Outcomes[outcome]++
		r.metrics.outcome(outcome)
		if outcome != SnapshotRefreshCurrent && outcome != SnapshotRefreshSkippedLive && outcome != SnapshotRefreshNoSnapshot && outcome != SnapshotRefreshSkippedIneligible {
			r.log.Info("snapshot refresh: candidate", "mode", mode, "node_id", nodeID, "release_id", releaseID,
				"app_id", c.AppID, "deployment_id", c.DeploymentID, "parked_at", c.ParkedAt, "outcome", outcome, "detail", detail)
		}
	}
	r.metrics.pending(nodeID, pass.Outcomes[SnapshotRefreshWouldRefresh]+pass.Outcomes[SnapshotRefreshDeferred]+pass.Outcomes[SnapshotRefreshFailed])
	r.log.Info("snapshot refresh: pass", "mode", mode, "node_id", nodeID, "release_id", releaseID,
		"window_hours", int(window/time.Hour), "candidates", pass.Candidates, "outcomes", pass.Outcomes)
	return pass, nil
}

// classify decides one candidate without changing anything, and for a stale
// one returns what its refresh boots. An error means the node's vmmd did not
// answer.
func (r *SnapshotRefresher) classify(ctx context.Context, nodeID string, c state.SnapshotRefreshCandidate) (outcome, detail string, t refreshTarget, err error) {
	e := r.e
	t.app, t.acct, t.limits, err = e.resolveAppAccount(ctx, c.AppID)
	if err != nil {
		return lookupOutcome(SnapshotRefreshSkippedIneligible, err, t)
	}
	// Every scheduler sees every candidate: only the owner counts it.
	if !e.ownsApp(t.app) {
		return "", "", t, nil
	}
	if c.Live {
		return SnapshotRefreshSkippedLive, "", t, nil
	}
	var reason string
	if t.dep, reason = r.refreshableDeployment(ctx, t.app, c.DeploymentID); reason != "" {
		return SnapshotRefreshSkippedIneligible, reason, t, nil
	}
	var ok bool
	if t.snap, ok, err = r.initSnapshotOnNode(ctx, t.dep.ID, nodeID); err != nil {
		return lookupOutcome(SnapshotRefreshUnknown, err, t)
	} else if !ok {
		return SnapshotRefreshNoSnapshot, "", t, nil
	}
	// The checks a wake makes before it asks vmmd.
	if t.snap.FCVersion != e.fcVer {
		return SnapshotRefreshWouldRefresh, ColdReasonFCVersion, t, nil
	}
	if (t.app.AppProtocol == api.AppProtocolHTTP2 || t.app.AppProtocol == api.AppProtocolGRPC) && t.snap.BaseImageVersion != fcvm.FAAS_BASE_IMAGE_VERSION {
		return SnapshotRefreshWouldRefresh, ColdReasonBaseImage, t, nil
	}
	// The check vmmd makes after the restore (fcvm.ErrSnapshotVCPUMismatch):
	// the app's last instance booked the vCPU count it booted or restored
	// with, and so does the snapshot it captured or restored from. An
	// instance booted before a change and parked after it captured the old
	// count.
	if c.VCPUs > 0 && c.VCPUs != appVCPUs(t.app) {
		return SnapshotRefreshWouldRefresh, SnapshotRefreshDetailVCPUs, t, nil
	}
	verdict, detail, err := r.askBacking(ctx, nodeID, t)
	if err != nil {
		return "", "", t, err
	}
	switch verdict {
	case SnapshotBackingCurrent:
		return SnapshotRefreshCurrent, "", t, nil
	case SnapshotBackingStale:
		return SnapshotRefreshWouldRefresh, detail, t, nil
	default:
		return SnapshotRefreshUnknown, detail, t, nil
	}
}

// askBacking asks nodeID's vmmd whether t's snapshot would restore there
// with the base a wake of t's deployment attaches.
func (r *SnapshotRefresher) askBacking(ctx context.Context, nodeID string, t refreshTarget) (SnapshotBackingVerdict, string, error) {
	e := r.e
	baseKey, err := e.deploymentBaseKey(ctx, t.app, t.dep, layerKey(t.dep.RootfsKey, t.dep.ID))
	if err != nil {
		// The base this wake would attach is unknown: so is the verdict.
		return unknownBacking(err)
	}
	router, ok := e.vmm.(snapshotBackingRouter)
	if !ok {
		return SnapshotBackingUnknown, "", ErrSnapshotBackingUnsupported
	}
	verdict, detail, err := router.SnapshotBackingCurrent(ctx, e.nodeForRoute(nodeID), SnapshotRef{
		DeploymentID: t.dep.ID, FCVersion: t.snap.FCVersion, StorageKey: t.snap.StorageKey, MemBytes: t.snap.MemBytes,
	}, baseKey)
	if err != nil {
		return SnapshotBackingUnknown, "", fmt.Errorf("sched: snapshot refresh: ask vmmd on %s: %w", nodeID, err)
	}
	return verdict, detail, nil
}

// lookupOutcome is a candidate's outcome when a lookup about it failed: the
// failure decides that one candidate and is its detail; the pass goes on.
func lookupOutcome(outcome string, failure error, t refreshTarget) (string, string, refreshTarget, error) {
	return outcome, failure.Error(), t, nil
}

// unknownBacking is the verdict when schedd cannot name what vmmd should
// check; the failure is its detail.
func unknownBacking(failure error) (SnapshotBackingVerdict, string, error) {
	return SnapshotBackingUnknown, failure.Error(), nil
}

// refreshableDeployment is the candidate's deployment when the platform may
// boot it for a refresh, else the reason it may not.
func (r *SnapshotRefresher) refreshableDeployment(ctx context.Context, app state.App, deploymentID string) (state.Deployment, string) {
	e := r.e
	switch {
	case app.Status != state.AppActive || app.DeletedAt != nil:
		return state.Deployment{}, "app_inactive"
	case executionModeForApp(app) != api.ExecutionModeRequest:
		return state.Deployment{}, "execution_mode"
	}
	if err := e.checkManagedPostgresAdmission(ctx, app.ID); err != nil {
		return state.Deployment{}, "managed_postgres_admission"
	}
	dep, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		return state.Deployment{}, "deployment_lookup"
	}
	switch {
	case dep.AppID != app.ID || dep.Status != state.DeployLive || dep.DeletedAt != nil:
		return state.Deployment{}, "deployment_not_live"
	case dep.EnvironmentWorkloadHeld():
		return state.Deployment{}, "environment_held"
	case securityQuarantineErr(dep) != nil:
		return state.Deployment{}, "security_quarantine"
	}
	if allowed, err := e.releaseAllowsPrime(ctx, dep); err != nil || !allowed {
		return state.Deployment{}, "release_task"
	}
	return dep, ""
}

// initSnapshotOnNode is the deployment's newest live init snapshot that a
// wake would restore on nodeID: one the node produced or holds a ready
// replica of, or any when locality is not recorded (a single node).
func (r *SnapshotRefresher) initSnapshotOnNode(ctx context.Context, deploymentID, nodeID string) (state.Snapshot, bool, error) {
	rows, _, err := r.e.liveTier(ctx, deploymentID, state.SnapshotTierInit)
	if errors.Is(err, state.ErrNotFound) {
		return state.Snapshot{}, false, nil
	}
	if err != nil {
		return state.Snapshot{}, false, err
	}
	for _, snap := range rows {
		read := r.e.readSnapshotLocality(ctx, snap.ID)
		if read.err != nil {
			return state.Snapshot{}, false, read.err
		}
		loc := read.locality
		if (loc.OriginNodeID == "" && len(loc.ReadyNodeIDs) == 0) || loc.OriginNodeID == nodeID || containsNodeID(loc.ReadyNodeIDs, nodeID) {
			return snap, true, nil
		}
	}
	return state.Snapshot{}, false, nil
}

type snapshotRefreshMetrics struct {
	outcomes *prometheus.CounterVec
	pend     *prometheus.GaugeVec
}

func newSnapshotRefreshMetrics(reg prometheus.Registerer) *snapshotRefreshMetrics {
	m := &snapshotRefreshMetrics{
		outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "schedd_snapshot_refresh_total",
			Help: "Snapshot refresh pass decisions per candidate app (ADR-20261106214916). would_refresh counts stale snapshots a shadow pass found.",
		}, []string{"outcome"}),
		pend: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "schedd_snapshot_refresh_pending",
			Help: "Stale snapshots the node's last refresh pass found and the platform has not refreshed yet.",
		}, []string{"node"}),
	}
	for _, o := range snapshotRefreshOutcomes {
		m.outcomes.WithLabelValues(o)
	}
	if reg != nil {
		reg.MustRegister(m.outcomes, m.pend)
	}
	return m
}

func (m *snapshotRefreshMetrics) outcome(o string) { m.outcomes.WithLabelValues(o).Inc() }

func (m *snapshotRefreshMetrics) pending(node string, n int) {
	m.pend.WithLabelValues(node).Set(float64(n))
}
