package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RouteProbeTarget is an app with probe selectors and an in-flight canary
// (ADR-847). StableID is set only for ADR-1446 default probe targets; ADR-847
// targets read the predecessor from the route health report. ProbePaths are
// the app's release_probe_paths for a default target (ADR-1449); empty means
// "/".
type RouteProbeTarget struct {
	AccountID, AppID, Slug, CandidateID, StableID string
	ProbePaths                                    []string
}

// RouteProbeObservation is one minute of probe results for one deployment
// and route.
type RouteProbeObservation struct {
	DeploymentID    string
	Method, Path    string
	WindowStart     time.Time
	Requests        int64
	ServerErrors    int64
	Unauthenticated int64
}

// RouteProbeStore persists probe rounds and results. Probe requests never
// reach request telemetry or usage, so these rows are their only record.
type RouteProbeStore interface {
	ListRouteProbeTargets(ctx context.Context) ([]RouteProbeTarget, error)
	ClaimRouteProbeRound(ctx context.Context, appID string, windowStart time.Time) (bool, error)
	RecordRouteProbeObservations(ctx context.Context, accountID, appID string, observations []RouteProbeObservation) error
	PruneRouteProbeData(ctx context.Context, before time.Time) error
}

var (
	_ RouteProbeStore = (*PgStore)(nil)
	_ RouteProbeStore = (*MemStore)(nil)
)

// RouteProbeTotal is the probe evidence for one deployment over a window.
type RouteProbeTotal struct {
	Requests, ServerErrors, Unauthenticated int64
}

// DefaultRouteProbeStore backs the ADR-1446 default probe: canaries of
// safe-policy request apps that apid probes without a route health gate, and
// the per-deployment totals meterd's circuit breaker reads.
type DefaultRouteProbeStore interface {
	ListDefaultRouteProbeTargets(ctx context.Context) ([]RouteProbeTarget, error)
	RouteProbeTotals(ctx context.Context, appID, candidateID, stableID string, since, until time.Time) (candidate, stable RouteProbeTotal, err error)
}

var (
	_ DefaultRouteProbeStore = (*PgStore)(nil)
	_ DefaultRouteProbeStore = (*MemStore)(nil)
)

func (s *PgStore) ListDefaultRouteProbeTargets(ctx context.Context) ([]RouteProbeTarget, error) {
	rows, err := sqlc.New().ListDefaultRouteProbeTargets(ctx, s.pool, int32(api.RouteMonitorBatchSize))
	if err != nil {
		return nil, err
	}
	out := make([]RouteProbeTarget, 0, len(rows))
	for _, r := range rows {
		var paths []string
		if err := json.Unmarshal(r.ProbePaths, &paths); err != nil || len(paths) == 0 {
			// No paths, or a manifest that fails to decode, keeps the default
			// probe of "/" (nil, as MemStore reports it).
			paths = nil
		}
		out = append(out, RouteProbeTarget{AccountID: r.AccountID, AppID: r.AppID, Slug: r.Slug, CandidateID: r.CandidateID, StableID: r.StableID, ProbePaths: paths})
	}
	return singleCanaryProbeTargets(out), nil
}

func (s *PgStore) RouteProbeTotals(ctx context.Context, appID, candidateID, stableID string, since, until time.Time) (RouteProbeTotal, RouteProbeTotal, error) {
	rows, err := sqlc.New().RouteProbeTotalsByDeployment(ctx, s.pool, sqlc.RouteProbeTotalsByDeploymentParams{
		AppID: appID, CandidateID: candidateID, StableID: stableID, Since: NewPgtypeTime(since), Until: NewPgtypeTime(until),
	})
	if err != nil {
		return RouteProbeTotal{}, RouteProbeTotal{}, err
	}
	var candidate, stable RouteProbeTotal
	for _, r := range rows {
		total := RouteProbeTotal{Requests: r.Requests, ServerErrors: r.ServerErrors, Unauthenticated: r.Unauthenticated}
		switch r.DeploymentID {
		case candidateID:
			candidate = total
		case stableID:
			stable = total
		}
	}
	return candidate, stable, nil
}

// singleCanaryProbeTargets keeps apps with exactly one candidate and one
// serving predecessor; the circuit breaker cannot compare anything else.
func singleCanaryProbeTargets(rows []RouteProbeTarget) []RouteProbeTarget {
	perApp := map[string]int{}
	for _, r := range rows {
		perApp[r.AppID]++
	}
	out := make([]RouteProbeTarget, 0, len(rows))
	for _, r := range rows {
		if perApp[r.AppID] == 1 {
			out = append(out, r)
		}
	}
	return out
}

func (s *PgStore) ListRouteProbeTargets(ctx context.Context) ([]RouteProbeTarget, error) {
	rows, err := sqlc.New().ListRouteProbeTargets(ctx, s.pool, int32(api.RouteMonitorBatchSize))
	if err != nil {
		return nil, err
	}
	out := make([]RouteProbeTarget, 0, len(rows))
	for _, r := range rows {
		out = append(out, RouteProbeTarget{AccountID: r.AccountID, AppID: r.AppID, Slug: r.Slug, CandidateID: r.CandidateID})
	}
	return out, nil
}

func (s *PgStore) ClaimRouteProbeRound(ctx context.Context, appID string, windowStart time.Time) (bool, error) {
	_, err := sqlc.New().ClaimRouteProbeRound(ctx, s.pool, sqlc.ClaimRouteProbeRoundParams{AppID: appID, WindowStart: NewPgtypeTime(windowStart)})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *PgStore) RecordRouteProbeObservations(ctx context.Context, accountID, appID string, observations []RouteProbeObservation) error {
	q := sqlc.New()
	for _, o := range observations {
		if err := q.RecordRouteProbeObservation(ctx, s.pool, sqlc.RecordRouteProbeObservationParams{
			AppID: appID, AccountID: accountID, DeploymentID: o.DeploymentID, Method: o.Method, Path: o.Path,
			WindowStart: NewPgtypeTime(o.WindowStart.UTC().Truncate(time.Minute)), Requests: o.Requests, ServerErrors: o.ServerErrors, Unauthenticated: o.Unauthenticated,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *PgStore) PruneRouteProbeData(ctx context.Context, before time.Time) error {
	q := sqlc.New()
	if err := q.PruneRouteProbeObservations(ctx, s.pool, NewPgtypeTime(before)); err != nil {
		return err
	}
	return q.PruneRouteProbeRounds(ctx, s.pool, NewPgtypeTime(before))
}

// pgSyntheticRouteHealth fills synthetic_windows for probed selectors that
// one-minute and pooled organic evidence left sparse, then re-evaluates.
func pgSyntheticRouteHealth(ctx context.Context, db sqlc.DBTX, accountID string, g api.RouteHealthGate, report *api.RouteHealthReport, anchor *time.Time, windows []api.RouteHealthWindowEvidence) error {
	targets := []int{}
	for i, f := range report.Routes {
		if i < len(g.Routes) && g.Routes[i].Probe != nil && f.EvidenceWindow == "" && routeHealthNeedsSynthetic(f) {
			targets = append(targets, i)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	rows, err := sqlc.New().RouteProbeObservations(ctx, db, sqlc.RouteProbeObservationsParams{
		AppID: report.AppID, AccountID: accountID, CandidateID: report.DeploymentID, StableID: report.StableDeploymentID,
		Since: NewPgtypeTime(windows[0].Start), Until: NewPgtypeTime(windows[len(windows)-1].End),
	})
	if err != nil {
		return err
	}
	observations := make([]RouteProbeObservation, 0, len(rows))
	for _, r := range rows {
		observations = append(observations, RouteProbeObservation{DeploymentID: r.DeploymentID, Method: r.Method, Path: r.Path, WindowStart: r.WindowStart.Time, Requests: r.Requests, ServerErrors: r.ServerErrors, Unauthenticated: r.Unauthenticated})
	}
	for _, i := range targets {
		report.Routes[i].SyntheticWindows = SyntheticWindows(report.Routes[i].Method, report.Routes[i].Path, report.DeploymentID, report.StableDeploymentID, windows, observations)
	}
	return nil
}

// SyntheticWindows sums probe observations into the pooled window bounds.
func SyntheticWindows(method, path, candidateID, stableID string, windows []api.RouteHealthWindowEvidence, observations []RouteProbeObservation) []api.RouteHealthWindowEvidence {
	out := make([]api.RouteHealthWindowEvidence, len(windows))
	for i, w := range windows {
		out[i] = api.RouteHealthWindowEvidence{Start: w.Start, End: w.End}
		for _, o := range observations {
			if o.Method != method || o.Path != path || o.WindowStart.Before(w.Start) || !o.WindowStart.Before(w.End) {
				continue
			}
			side := &out[i].Stable
			switch o.DeploymentID {
			case candidateID:
				side = &out[i].Candidate
			case stableID:
			default:
				continue
			}
			side.Requests += o.Requests
			side.ServerErrors += o.ServerErrors
			side.Unauthenticated += o.Unauthenticated
		}
	}
	return out
}

// routeHealthNeedsSynthetic mirrors routehealth.NeedsPooledEvidence without an
// import cycle: the finding is unknown only because requests were sparse.
func routeHealthNeedsSynthetic(f api.RouteHealthFinding) bool {
	if f.Status != "unknown" {
		return false
	}
	sparse := false
	for _, w := range f.Windows {
		for _, signal := range [][2]string{{w.ErrorStatus, w.ErrorReason}, {w.LatencyStatus, w.LatencyReason}} {
			switch {
			case signal[0] == "regressed":
				return false
			case signal[0] == "unknown" && (signal[1] == "insufficient_requests" || signal[1] == "insufficient_latency_requests"):
				sparse = true
			case signal[0] == "unknown":
				return false
			}
		}
	}
	return sparse
}

// MemStore keeps probe rounds and results in memory for tests.
type memRouteProbes struct {
	mu           sync.Mutex
	rounds       map[string]bool
	observations map[string]*RouteProbeObservation
	appAccounts  map[string]string
}

func (m *MemStore) routeProbes() *memRouteProbes {
	m.routeProbeOnce.Do(func() {
		m.routeProbeData = &memRouteProbes{rounds: map[string]bool{}, observations: map[string]*RouteProbeObservation{}, appAccounts: map[string]string{}}
	})
	return m.routeProbeData
}

func (m *MemStore) ListRouteProbeTargets(_ context.Context) ([]RouteProbeTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []RouteProbeTarget{}
	for appID, g := range m.routeHealthGates {
		app, ok := m.apps[appID]
		if !ok || app.Status == AppDeleted {
			continue
		}
		probed := false
		for _, r := range g.Routes {
			probed = probed || r.Probe != nil
		}
		if !probed {
			continue
		}
		for _, d := range m.deployments {
			if d.AppID == appID && d.Status == DeployLive && d.TrafficPercent > 0 && d.CanaryTotalSteps > 0 && d.CanaryStep < d.CanaryTotalSteps && (d.Scope == "" || d.Scope == "default") {
				out = append(out, RouteProbeTarget{AccountID: app.AccountID, AppID: appID, Slug: app.Slug, CandidateID: d.ID})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AppID+out[i].CandidateID < out[j].AppID+out[j].CandidateID })
	return out, nil
}

func (m *MemStore) ListDefaultRouteProbeTargets(_ context.Context) ([]RouteProbeTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inScope := func(d Deployment, appID string) bool {
		return d.AppID == appID && d.Status == DeployLive && d.TrafficPercent > 0 && (d.Scope == "" || d.Scope == "default")
	}
	out := []RouteProbeTarget{}
	for appID, app := range m.apps {
		if app.Status == AppDeleted || app.PreviewOfSlug != "" || app.Manifest.ReleasePolicy == api.ReleasePolicyImmediate ||
			(app.Manifest.ExecutionMode != "" && app.Manifest.ExecutionMode != api.ExecutionModeRequest) {
			continue
		}
		for _, d := range m.deployments {
			if !inScope(d, appID) || d.CanaryTotalSteps <= 0 || d.CanaryStep >= d.CanaryTotalSteps {
				continue
			}
			for _, stable := range m.deployments {
				if stable.ID != d.ID && inScope(stable, appID) {
					out = append(out, RouteProbeTarget{AccountID: app.AccountID, AppID: appID, Slug: app.Slug, CandidateID: d.ID, StableID: stable.ID,
						ProbePaths: append([]string(nil), app.Manifest.ReleaseProbePaths...)})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AppID+out[i].CandidateID < out[j].AppID+out[j].CandidateID })
	return singleCanaryProbeTargets(out), nil
}

func (m *MemStore) RouteProbeTotals(_ context.Context, appID, candidateID, stableID string, since, until time.Time) (RouteProbeTotal, RouteProbeTotal, error) {
	p := m.routeProbes()
	p.mu.Lock()
	defer p.mu.Unlock()
	var candidate, stable RouteProbeTotal
	for key, o := range p.observations {
		if !strings.HasPrefix(key, appID+"\x00") || o.WindowStart.Before(since) || !o.WindowStart.Before(until) {
			continue
		}
		var total *RouteProbeTotal
		switch o.DeploymentID {
		case candidateID:
			total = &candidate
		case stableID:
			total = &stable
		default:
			continue
		}
		total.Requests += o.Requests
		total.ServerErrors += o.ServerErrors
		total.Unauthenticated += o.Unauthenticated
	}
	return candidate, stable, nil
}

func (m *MemStore) ClaimRouteProbeRound(_ context.Context, appID string, windowStart time.Time) (bool, error) {
	p := m.routeProbes()
	p.mu.Lock()
	defer p.mu.Unlock()
	key := appID + "\x00" + windowStart.UTC().Format(time.RFC3339)
	if p.rounds[key] {
		return false, nil
	}
	p.rounds[key] = true
	return true, nil
}

func (m *MemStore) RecordRouteProbeObservations(_ context.Context, accountID, appID string, observations []RouteProbeObservation) error {
	p := m.routeProbes()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, o := range observations {
		o.WindowStart = o.WindowStart.UTC().Truncate(time.Minute)
		key := appID + "\x00" + o.DeploymentID + "\x00" + o.Method + "\x00" + o.Path + "\x00" + o.WindowStart.Format(time.RFC3339)
		if existing, ok := p.observations[key]; ok {
			existing.Requests += o.Requests
			existing.ServerErrors += o.ServerErrors
			existing.Unauthenticated += o.Unauthenticated
			continue
		}
		copy := o
		p.observations[key] = &copy
		p.appAccounts[key] = accountID
	}
	return nil
}

func (m *MemStore) PruneRouteProbeData(_ context.Context, before time.Time) error {
	p := m.routeProbes()
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, o := range p.observations {
		if o.WindowStart.Before(before) {
			delete(p.observations, key)
			delete(p.appAccounts, key)
		}
	}
	return nil
}

// RouteProbeObservationsForTest returns recorded probe rows for one app.
func (m *MemStore) RouteProbeObservationsForTest(appID string) []RouteProbeObservation {
	p := m.routeProbes()
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []RouteProbeObservation{}
	for key, o := range p.observations {
		if len(key) > len(appID) && key[:len(appID)] == appID {
			out = append(out, *o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].DeploymentID+out[i].Path+out[i].WindowStart.String() < out[j].DeploymentID+out[j].Path+out[j].WindowStart.String()
	})
	return out
}
