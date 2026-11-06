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
}

// snapshotRefreshInterval is how often the refresher looks for nodes whose
// release changed.
const snapshotRefreshInterval = time.Minute

// snapshotBackingRouter is the part of the routed VMM a pass asks.
type snapshotBackingRouter interface {
	SnapshotBackingCurrent(ctx context.Context, nodeID string, snap SnapshotRef, baseKey string) (SnapshotBackingVerdict, string, error)
}

// SnapshotRefresher finds, after a node takes a release, the recently active
// apps whose snapshots on that node the release made stale
// (ADR-20261106214916). It runs one pass per node per release it sees,
// including once per node when schedd starts, since schedd restarts during a
// release. A pass only reads: it asks the node's vmmd whether each candidate's
// snapshot would restore and counts the answers. Booting the refresh is
// step 3b; until then `snapshot_refresh` is off or shadow.
type SnapshotRefresher struct {
	e       *Engine
	log     *slog.Logger
	now     func() time.Time
	metrics *snapshotRefreshMetrics

	mu          sync.Mutex
	mode        string
	windowHours int
	// passed is the release each node's last completed pass was for.
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
		return fmt.Errorf("sched: snapshot_refresh: %q is not off or shadow", mode)
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
	for _, node := range nodes {
		if !node.Active || ctx.Err() != nil {
			continue
		}
		release := ""
		if node.ReleaseID != nil {
			release = *node.ReleaseID
		}
		r.mu.Lock()
		done, seen := r.passed[node.ID]
		r.mu.Unlock()
		if seen && done == release {
			continue
		}
		if _, err := r.Pass(ctx, node.ID, release); err != nil {
			r.log.Warn("snapshot refresh: pass did not finish; retrying next tick", "node_id", node.ID, "release_id", release, "err", err)
			continue
		}
		r.mu.Lock()
		r.passed[node.ID] = release
		r.mu.Unlock()
	}
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
		outcome, detail, err := r.classify(ctx, nodeID, c)
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
		pass.Candidates++
		pass.Outcomes[outcome]++
		r.metrics.outcome(outcome)
		if outcome == SnapshotRefreshWouldRefresh || outcome == SnapshotRefreshUnknown {
			r.log.Info("snapshot refresh: candidate", "mode", mode, "node_id", nodeID, "release_id", releaseID,
				"app_id", c.AppID, "deployment_id", c.DeploymentID, "parked_at", c.ParkedAt, "outcome", outcome, "detail", detail)
		}
	}
	r.metrics.pending(nodeID, pass.Outcomes[SnapshotRefreshWouldRefresh])
	r.log.Info("snapshot refresh: pass", "mode", mode, "node_id", nodeID, "release_id", releaseID,
		"window_hours", int(window/time.Hour), "candidates", pass.Candidates, "outcomes", pass.Outcomes)
	return pass, nil
}

// classify decides one candidate without changing anything. An error means
// the node's vmmd did not answer.
func (r *SnapshotRefresher) classify(ctx context.Context, nodeID string, c state.SnapshotRefreshCandidate) (outcome, detail string, err error) {
	e := r.e
	if c.Live {
		return SnapshotRefreshSkippedLive, "", nil
	}
	app, _, _, err := e.resolveAppAccount(ctx, c.AppID)
	if err != nil {
		return SnapshotRefreshSkippedIneligible, err.Error(), nil
	}
	if !e.ownsApp(app) {
		return "", "", nil
	}
	dep, reason := r.refreshableDeployment(ctx, app, c.DeploymentID)
	if reason != "" {
		return SnapshotRefreshSkippedIneligible, reason, nil
	}
	snap, ok, err := r.initSnapshotOnNode(ctx, dep.ID, nodeID)
	if err != nil {
		return SnapshotRefreshUnknown, err.Error(), nil
	}
	if !ok {
		return SnapshotRefreshNoSnapshot, "", nil
	}
	// The checks a wake makes before it asks vmmd.
	if snap.FCVersion != e.fcVer {
		return SnapshotRefreshWouldRefresh, ColdReasonFCVersion, nil
	}
	if (app.AppProtocol == api.AppProtocolHTTP2 || app.AppProtocol == api.AppProtocolGRPC) && snap.BaseImageVersion != fcvm.FAAS_BASE_IMAGE_VERSION {
		return SnapshotRefreshWouldRefresh, ColdReasonBaseImage, nil
	}
	baseKey, err := e.deploymentBaseKey(ctx, app, dep, layerKey(dep.RootfsKey, dep.ID))
	if err != nil {
		return SnapshotRefreshUnknown, err.Error(), nil
	}
	router, ok := e.vmm.(snapshotBackingRouter)
	if !ok {
		return SnapshotRefreshUnknown, "", ErrSnapshotBackingUnsupported
	}
	verdict, detail, err := router.SnapshotBackingCurrent(ctx, e.nodeForRoute(nodeID), SnapshotRef{
		DeploymentID: dep.ID, FCVersion: snap.FCVersion, StorageKey: snap.StorageKey, MemBytes: snap.MemBytes,
	}, baseKey)
	if err != nil {
		return "", "", fmt.Errorf("sched: snapshot refresh: ask vmmd on %s: %w", nodeID, err)
	}
	switch verdict {
	case SnapshotBackingCurrent:
		return SnapshotRefreshCurrent, "", nil
	case SnapshotBackingStale:
		return SnapshotRefreshWouldRefresh, detail, nil
	default:
		return SnapshotRefreshUnknown, detail, nil
	}
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
