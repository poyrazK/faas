package state

import (
	"context"
	"sort"
	"time"
)

// SyntheticGateProbeTarget is one synthetic release gate to probe on a
// canary (ADR-20261022082305): the check's request, sent to the candidate
// and its serving predecessor.
type SyntheticGateProbeTarget struct {
	AppID       string
	AccountID   string
	Slug        string
	CandidateID string
	StableID    string
	GateID      string
	Method      string
	Path        string
	// ExpectedStatus 0 means any 2xx, as for the check's scheduled runs.
	ExpectedStatus int
	TimeoutMS      int
}

// ReleaseGateProbeResult counts one round's probes of a gate on one
// revision; WindowStart is the round's minute.
type ReleaseGateProbeResult struct {
	GateID       string
	AppID        string
	DeploymentID string
	WindowStart  time.Time
	Requests     int
	Failures     int
}

// ReleaseGateProbeStore holds synthetic gate probe results.
type ReleaseGateProbeStore interface {
	// ListSyntheticGateProbeTargets lists synthetic gates of canaries rolling
	// out beside a serving predecessor, at most limit rows.
	ListSyntheticGateProbeTargets(ctx context.Context, limit int) ([]SyntheticGateProbeTarget, error)
	// RecordReleaseGateProbeResults adds probe counts to their minute.
	RecordReleaseGateProbeResults(ctx context.Context, results []ReleaseGateProbeResult) error
	// ReleaseGateProbeTotals sums a gate's probes of a revision over the
	// minutes in [since, until).
	ReleaseGateProbeTotals(ctx context.Context, gateID, deploymentID string, since, until time.Time) (requests, failures int64, err error)
	// PruneReleaseGateProbeResults deletes minutes before before.
	PruneReleaseGateProbeResults(ctx context.Context, before time.Time) error
}

// releaseGateProbeCap bounds one minute's counts, like the table's CHECK.
const releaseGateProbeCap = 10000

type releaseGateProbeKey struct {
	gateID, deploymentID string
	minute               time.Time
}

var _ ReleaseGateProbeStore = (*MemStore)(nil)

func (m *MemStore) ListSyntheticGateProbeTargets(_ context.Context, limit int) ([]SyntheticGateProbeTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	isDefaultScope := func(scope string) bool { return scope == "" || scope == "default" }
	var out []SyntheticGateProbeTarget
	for _, gate := range m.releaseGates {
		if gate.Kind != ReleaseGateSynthetic {
			continue
		}
		app, ok := m.apps[gate.AppID]
		if !ok || app.Status == AppDeleted || app.PreviewOfSlug != "" {
			continue
		}
		var check *SyntheticCheck
		for _, c := range m.syntheticChecks[gate.AppID] {
			if c.ID == gate.SyntheticCheckID && c.Enabled {
				c := c
				check = &c
			}
		}
		if check == nil {
			continue
		}
		for _, d := range m.deployments {
			if d.AppID != app.ID || d.Status != DeployLive || d.TrafficPercent <= 0 || d.CanaryTotalSteps <= 0 ||
				d.CanaryStep >= d.CanaryTotalSteps || !isDefaultScope(d.Scope) {
				continue
			}
			for _, s := range m.deployments {
				if s.AppID != app.ID || s.ID == d.ID || s.Status != DeployLive || s.TrafficPercent <= 0 || !isDefaultScope(s.Scope) {
					continue
				}
				out = append(out, SyntheticGateProbeTarget{AppID: app.ID, AccountID: app.AccountID, Slug: app.Slug,
					CandidateID: d.ID, StableID: s.ID, GateID: gate.ID, Method: check.Method, Path: check.Path,
					ExpectedStatus: check.ExpectedStatus, TimeoutMS: check.TimeoutMS})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.AppID != b.AppID {
			return a.AppID < b.AppID
		}
		if a.CandidateID != b.CandidateID {
			return a.CandidateID < b.CandidateID
		}
		if a.StableID != b.StableID {
			return a.StableID < b.StableID
		}
		return a.GateID < b.GateID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) RecordReleaseGateProbeResults(_ context.Context, results []ReleaseGateProbeResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.releaseGateProbes == nil {
		m.releaseGateProbes = map[releaseGateProbeKey]ReleaseGateProbeResult{}
	}
	for _, r := range results {
		if r.Requests < 1 || r.Failures < 0 || r.Failures > r.Requests {
			return ErrInvalidArgument
		}
		key := releaseGateProbeKey{r.GateID, r.DeploymentID, r.WindowStart.UTC().Truncate(time.Minute)}
		cur, ok := m.releaseGateProbes[key]
		if !ok {
			cur = ReleaseGateProbeResult{GateID: r.GateID, AppID: r.AppID, DeploymentID: r.DeploymentID, WindowStart: key.minute}
		}
		cur.Requests = min(cur.Requests+r.Requests, releaseGateProbeCap)
		cur.Failures = min(cur.Failures+r.Failures, cur.Requests)
		m.releaseGateProbes[key] = cur
	}
	return nil
}

func (m *MemStore) ReleaseGateProbeTotals(_ context.Context, gateID, deploymentID string, since, until time.Time) (int64, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var requests, failures int64
	for key, r := range m.releaseGateProbes {
		if key.gateID == gateID && key.deploymentID == deploymentID && !key.minute.Before(since) && key.minute.Before(until) {
			requests += int64(r.Requests)
			failures += int64(r.Failures)
		}
	}
	return requests, failures, nil
}

func (m *MemStore) PruneReleaseGateProbeResults(_ context.Context, before time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range m.releaseGateProbes {
		if key.minute.Before(before) {
			delete(m.releaseGateProbes, key)
		}
	}
	return nil
}
