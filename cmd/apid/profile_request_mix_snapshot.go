package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) captureProfileRequestMix(ctx context.Context, accountID, appID string, assessment api.ProfileRegressionAssessment) *api.ProfileRequestMixSnapshot {
	a, b, options := assessment.Baseline, assessment.Candidate, assessment.Options
	out := &api.ProfileRequestMixSnapshot{CapturedAt: time.Now().UTC(), Status: "unavailable", Reason: "Request telemetry could not be captured.", Warnings: []string{}}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	acct, err := s.store.AccountByID(ctx, accountID)
	if err != nil {
		return out
	}
	limits := api.MustLimitsFor(acct.Plan)
	reader, ok := s.store.(state.ProfileRequestMixReader)
	if !ok || !limits.DebugTelemetryEnabled {
		out.Reason = "Request-mix telemetry is unavailable on this plan or installation."
		return out
	}
	cutoff := time.Now().Add(-time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour)
	if a.DeploymentID == "" || b.DeploymentID == "" || a.Start.Before(cutoff) || b.Start.Before(cutoff) {
		out.Reason = "Comparison windows are unavailable or outside request telemetry retention."
		return out
	}
	read := func(q api.ProfileQuery) *api.ProfileRequestMixWindow {
		mix, err := reader.ProfileRequestMix(ctx, accountID, appID, q)
		if err != nil {
			return nil
		}
		if len(mix.Routes) > api.ProfileRequestMixSnapshotMaxRoutes {
			mix.Routes = mix.Routes[:api.ProfileRequestMixSnapshotMaxRoutes]
			mix.Truncated = true
		}
		if mix.Routes == nil {
			mix.Routes = []api.ProfileRequestMixGroup{}
		}
		if mix.Statuses == nil {
			mix.Statuses = []api.ProfileRequestMixGroup{}
		}
		return &api.ProfileRequestMixWindow{Query: q, Total: mix.Total, Routes: mix.Routes, Statuses: mix.Statuses, Truncated: mix.Truncated}
	}
	out.Baseline, out.Candidate = read(a), read(b)
	out.CapturedAt = time.Now().UTC()
	if out.Baseline == nil || out.Candidate == nil {
		if out.Baseline != nil || out.Candidate != nil {
			out.Status = "partial"
		}
		out.Reason = "One or both request-mix windows could not be captured."
		for _, w := range []*api.ProfileRequestMixWindow{out.Baseline, out.Candidate} {
			if w == nil {
				continue
			}
			for {
				body, _ := json.Marshal(out)
				if len(body) <= api.ProfileRequestMixSnapshotMaxBytes || len(w.Routes) == 0 {
					break
				}
				w.Routes = w.Routes[:len(w.Routes)-1]
				w.Truncated = true
			}
		}
		return out
	}
	// Leave room for the CPU evidence in the enclosing assessment's budget.
	for {
		left, right := out.Baseline, out.Candidate
		view := dashboard.BuildProfileRequestMix(state.ProfileRequestMix{Total: left.Total, Routes: left.Routes, Statuses: left.Statuses, Truncated: left.Truncated}, state.ProfileRequestMix{Total: right.Total, Routes: right.Routes, Statuses: right.Statuses, Truncated: right.Truncated}, api.NormalizeProfileRegressionOptions(options).MinimumRequests)
		if assessment.BaselineRequests != nil && *assessment.BaselineRequests != left.Total || assessment.CandidateRequests != nil && *assessment.CandidateRequests != right.Total {
			view.Warnings = append(view.Warnings, "Snapshot request totals differ from the CPU assessment counts, for example because telemetry arrived between reads. Mix shares use the snapshot totals.")
		}
		out.Warnings, out.RouteDifference, out.StatusDifference = view.Warnings, view.RouteDifference, view.StatusDifference
		if out.Warnings == nil {
			out.Warnings = []string{}
		}
		out.Complete = !left.Truncated && !right.Truncated
		out.Status = "captured"
		if !out.Complete {
			out.Status = "partial"
		}
		out.Reason = "Frozen observed request counts; completeness describes aggregation, not telemetry delivery."
		body, err := json.Marshal(out)
		if err == nil && len(body) <= api.ProfileRequestMixSnapshotMaxBytes {
			break
		}
		target := left
		if len(right.Routes) > len(left.Routes) {
			target = right
		}
		if len(target.Routes) == 0 {
			return &api.ProfileRequestMixSnapshot{CapturedAt: out.CapturedAt, Status: "unavailable", Reason: "Request-mix metadata exceeds its storage budget.", Warnings: []string{}}
		}
		target.Routes = target.Routes[:len(target.Routes)-1]
		target.Truncated = true
	}
	return out
}
