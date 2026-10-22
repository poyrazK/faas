// Package gather reads an app's release gates and the evidence each one
// needs from the state store (ADR-20261019071734). It is separate from
// pkg/releasegate so the canary package can judge gates without importing
// pkg/state.
package gather

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/releasegate"
	"github.com/onebox-faas/faas/pkg/state"
)

// Window is a half-open time range.
type Window struct {
	Since time.Time
	Until time.Time
}

// Request names whose evidence to gather. BaselineID may be empty when the
// release has no predecessor.
type Request struct {
	AppID          string
	ReleaseID      string
	BaselineID     string
	ReleaseWindow  Window
	BaselineWindow Window
	// PriorWindow is the time before the rollout started that a
	// metric_change gate compares with (ADR-20261022082305); zero skips
	// those gates.
	PriorWindow Window
	// Canary is set by the canary circuit breaker. Synthetic gates are
	// judged only there, where both revisions are probed.
	Canary bool
	// Evidence overrides the store's evidence reader (tests).
	Evidence state.ReleaseGateEvidenceStore
}

// Gather loads the app's gates and their evidence. A store without gates
// returns none. A gate whose evidence cannot be read is marked Unavailable,
// and the first such error is returned alongside the rest.
func Gather(ctx context.Context, store any, req Request) ([]releasegate.Gate, map[string]releasegate.Evidence, error) {
	gates, ok := store.(state.ReleaseGateStore)
	if !ok {
		return nil, nil, nil
	}
	rows, err := gates.ListReleaseGates(ctx, req.AppID)
	if err != nil {
		return nil, nil, fmt.Errorf("list release gates: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil, nil
	}
	// Synthetic gates read probe results, which every store keeps; the
	// other kinds need the evidence reader (Postgres).
	reader := req.Evidence
	if reader == nil {
		reader, _ = store.(state.ReleaseGateEvidenceStore)
	}
	out := make([]releasegate.Gate, 0, len(rows))
	evidence := make(map[string]releasegate.Evidence, len(rows))
	var names map[string]string
	var firstErr error
	note := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	for _, row := range rows {
		gate := releasegate.Gate{Name: row.Name, Kind: row.Kind, MaxBurnRate: row.MaxBurnRate,
			MaxNewGroups: row.MaxNewGroups, MinEvents: row.MinEvents, MetricName: row.MetricName,
			Comparison: row.Comparison, Threshold: row.Threshold, MinSamples: row.MinSamples,
			MaxFailurePercent: row.MaxFailurePercent, MaxChangePercent: row.MaxChangePercent}
		var ev releasegate.Evidence
		if reader == nil && row.Kind != state.ReleaseGateSynthetic {
			continue
		}
		switch row.Kind {
		case state.ReleaseGateSynthetic:
			if !req.Canary {
				continue
			}
			var err error
			if names == nil {
				names = checkNames(ctx, store, req.AppID)
			}
			gate.CheckName = names[row.SyntheticCheckID]
			if ev.Probes, ev.BaselineProbes, err = probeEvidence(ctx, store, reader, row.ID, req); err != nil {
				note(fmt.Errorf("release gate %s: read probes: %w", row.Name, err))
				ev.Unavailable = true
			}
		case state.ReleaseGateMetricChange:
			if req.PriorWindow.Since.IsZero() || !req.PriorWindow.Since.Before(req.PriorWindow.Until) {
				continue
			}
			var err error
			if ev.Metric, ev.PriorMetric, err = metricChangeEvidence(ctx, reader, req, row.MetricName); err != nil {
				note(fmt.Errorf("release gate %s: read metric: %w", row.Name, err))
				ev.Unavailable = true
			}
		case state.ReleaseGateSLO:
			slo, err := reader.ReleaseGateSLO(ctx, req.AppID, row.SLOID)
			if errors.Is(err, state.ErrNotFound) {
				continue
			}
			if err != nil {
				note(fmt.Errorf("release gate %s: read SLO: %w", row.Name, err))
				ev.Unavailable = true
				break
			}
			gate.ObjectiveBP, gate.LatencyThresholdMS = slo.ObjectiveBP, slo.LatencyThresholdMS
			release, err := reader.ReleaseGateDeploymentSLI(ctx, req.AppID, req.ReleaseID, req.ReleaseWindow.Since, req.ReleaseWindow.Until, slo.LatencyThresholdMS)
			if err != nil {
				note(fmt.Errorf("release gate %s: read release requests: %w", row.Name, err))
				ev.Unavailable = true
				break
			}
			ev.Release = releasegate.SLISample(release)
			if req.BaselineID != "" {
				baseline, err := reader.ReleaseGateDeploymentSLI(ctx, req.AppID, req.BaselineID, req.BaselineWindow.Since, req.BaselineWindow.Until, slo.LatencyThresholdMS)
				if err != nil {
					note(fmt.Errorf("release gate %s: read baseline requests: %w", row.Name, err))
					ev.Unavailable = true
					break
				}
				ev.Baseline = releasegate.SLISample(baseline)
			}
		case state.ReleaseGateNewErrors:
			groups, err := reader.ReleaseGateNewErrorGroups(ctx, req.AppID, req.ReleaseID, max(row.MinEvents, 1))
			if err != nil {
				note(fmt.Errorf("release gate %s: read error groups: %w", row.Name, err))
				ev.Unavailable = true
				break
			}
			for _, g := range groups {
				ev.NewGroups = append(ev.NewGroups, releasegate.ErrorGroup(g))
			}
		case state.ReleaseGateCustomMetric:
			samples, average, err := reader.ReleaseGateCustomMetricWindow(ctx, req.AppID, row.MetricName, req.ReleaseWindow.Since, req.ReleaseWindow.Until)
			if err != nil {
				note(fmt.Errorf("release gate %s: read metric: %w", row.Name, err))
				ev.Unavailable = true
				break
			}
			ev.Metric = releasegate.MetricWindow{Samples: samples, Average: average}
		default:
			continue
		}
		out = append(out, gate)
		evidence[row.Name] = ev
	}
	return out, evidence, firstErr
}

// checkNames maps an app's synthetic check IDs to names, for summaries.
func checkNames(ctx context.Context, store any, appID string) map[string]string {
	names := map[string]string{}
	checks, ok := store.(state.SyntheticCheckStore)
	if !ok {
		return names
	}
	list, err := checks.ListSyntheticChecks(ctx, appID)
	if err != nil {
		return names
	}
	for _, c := range list {
		names[c.ID] = c.Name
	}
	return names
}

// probeEvidence sums a synthetic gate's probes of the candidate and the
// stable revision. Rounds are recorded by minute, so the window starts at
// its first minute.
func probeEvidence(ctx context.Context, store any, reader state.ReleaseGateEvidenceStore, gateID string, req Request) (releasegate.ProbeCounts, releasegate.ProbeCounts, error) {
	probes, ok := reader.(state.ReleaseGateProbeStore)
	if !ok {
		if probes, ok = store.(state.ReleaseGateProbeStore); !ok {
			return releasegate.ProbeCounts{}, releasegate.ProbeCounts{}, errors.New("probe results are unavailable")
		}
	}
	read := func(deploymentID string, w Window) (releasegate.ProbeCounts, error) {
		if deploymentID == "" {
			return releasegate.ProbeCounts{}, nil
		}
		requests, failures, err := probes.ReleaseGateProbeTotals(ctx, gateID, deploymentID, w.Since.Truncate(time.Minute), w.Until)
		return releasegate.ProbeCounts{Requests: requests, Failures: failures}, err
	}
	release, err := read(req.ReleaseID, req.ReleaseWindow)
	if err != nil {
		return releasegate.ProbeCounts{}, releasegate.ProbeCounts{}, err
	}
	baseline, err := read(req.BaselineID, req.BaselineWindow)
	return release, baseline, err
}

// metricChangeEvidence reads a metric over the release's window and the
// window before the rollout.
func metricChangeEvidence(ctx context.Context, reader state.ReleaseGateEvidenceStore, req Request, metric string) (releasegate.MetricWindow, releasegate.MetricWindow, error) {
	samples, average, err := reader.ReleaseGateCustomMetricWindow(ctx, req.AppID, metric, req.ReleaseWindow.Since, req.ReleaseWindow.Until)
	if err != nil {
		return releasegate.MetricWindow{}, releasegate.MetricWindow{}, err
	}
	priorSamples, priorAverage, err := reader.ReleaseGateCustomMetricWindow(ctx, req.AppID, metric, req.PriorWindow.Since, req.PriorWindow.Until)
	if err != nil {
		return releasegate.MetricWindow{}, releasegate.MetricWindow{}, err
	}
	return releasegate.MetricWindow{Samples: samples, Average: average}, releasegate.MetricWindow{Samples: priorSamples, Average: priorAverage}, nil
}

// Verdict gathers and judges in one step.
func Verdict(ctx context.Context, store any, req Request) (releasegate.Verdict, error) {
	gates, evidence, err := Gather(ctx, store, req)
	return releasegate.Judge(gates, evidence), err
}
