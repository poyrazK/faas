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
	reader := req.Evidence
	if reader == nil {
		reader, ok = store.(state.ReleaseGateEvidenceStore)
		if !ok {
			return nil, nil, nil
		}
	}
	out := make([]releasegate.Gate, 0, len(rows))
	evidence := make(map[string]releasegate.Evidence, len(rows))
	var firstErr error
	note := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	for _, row := range rows {
		gate := releasegate.Gate{Name: row.Name, Kind: row.Kind, MaxBurnRate: row.MaxBurnRate,
			MaxNewGroups: row.MaxNewGroups, MinEvents: row.MinEvents, MetricName: row.MetricName,
			Comparison: row.Comparison, Threshold: row.Threshold, MinSamples: row.MinSamples}
		var ev releasegate.Evidence
		switch row.Kind {
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

// Verdict gathers and judges in one step.
func Verdict(ctx context.Context, store any, req Request) (releasegate.Verdict, error) {
	gates, evidence, err := Gather(ctx, store, req)
	return releasegate.Judge(gates, evidence), err
}
