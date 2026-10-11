package main

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

// profileCanarySignal only reads the assessment captured by the background
// worker. Report and dashboard reads never issue profiling backend queries.
func (s *server) profileCanarySignal(ctx context.Context, acct state.Account, app state.App, candidate state.Deployment) *api.CanaryProfileSignal {
	if candidate.CanaryTotalSteps <= 0 {
		return nil
	}
	policyStore, ok := s.store.(state.ProfileDeploymentCheckStore)
	if !ok {
		return nil
	}
	policy, err := policyStore.GetProfileDeploymentPolicy(ctx, acct.ID, app.ID)
	if err != nil || !policy.Config.Enabled {
		return nil
	}
	checkStore, ok := s.store.(state.ProfileCanaryCheckStore)
	if !ok {
		return nil
	}
	check, err := checkStore.GetLatestProfileCanaryCheck(ctx, acct.ID, app.ID, candidate.ID, policy.Revision)
	if err != nil {
		return nil
	}
	s.enrichCanaryProfileSignal(ctx, app, &check, &candidate)
	return &check
}

// enrichCanaryProfileSignal adds request-time source metadata and a dashboard
// comparison link. The store keeps only validated relative paths and lines.
func (s *server) enrichCanaryProfileSignal(ctx context.Context, app state.App, signal *api.CanaryProfileSignal, candidate *state.Deployment) {
	if signal == nil {
		return
	}
	if signal.Gate != nil && signal.Gate.Status == "collecting" && !time.Now().Before(signal.Gate.Deadline) {
		signal.Gate.Status = "timed_out"
		signal.Gate.Reason = "Insufficient consecutive qualified profile evidence before the gate deadline."
	}
	if signal.Baseline != nil && signal.Candidate != nil {
		signal.RouteChecks = linkedProfileRouteChecks(app.Slug, *signal.Baseline, *signal.Candidate, signal.RouteChecks)
	}
	type deploymentSource struct {
		origin profiling.SourceProvenance
		source api.ProfileSource
	}
	sources := make(map[string]deploymentSource, 2)
	add := func(dep state.Deployment) {
		if dep.ID == "" || dep.AppID != app.ID {
			return
		}
		origin := profileSourceProvenance(app, dep)
		sources[dep.ID] = deploymentSource{origin: origin, source: profiling.SourceRevision(origin)}
	}
	if candidate != nil {
		add(*candidate)
	}
	load := func(query *api.ProfileQuery) {
		if query == nil || sources[query.DeploymentID].source.Available {
			return
		}
		dep, err := s.store.DeploymentByID(ctx, query.DeploymentID)
		if err == nil {
			add(dep)
		}
	}
	load(signal.Baseline)
	load(signal.Candidate)
	setSource := func(query *api.ProfileQuery) *api.ProfileSource {
		if query == nil {
			return nil
		}
		resolved, ok := sources[query.DeploymentID]
		if !ok {
			return nil
		}
		return &resolved.source
	}
	signal.BaselineSource = setSource(signal.Baseline)
	signal.CandidateSource = setSource(signal.Candidate)
	if signal.CandidateSource == nil && candidate != nil && candidate.AppID == app.ID {
		resolved, ok := sources[candidate.ID]
		if ok {
			signal.CandidateSource = &resolved.source
		}
	}
	signal.ComparisonURL = profileCanaryComparisonURL(app.Slug, signal)
	linkBudget := api.ProfileMaxViewSymbolBytes
	link := func(origin profiling.SourceProvenance, path string, line int64) *api.ProfileSourceLocation {
		location, _ := profiling.LinkSourcePath(origin, path, line)
		if location == nil {
			return nil
		}
		cost := len(location.URL) + len(location.Path)
		if cost > linkBudget {
			return nil
		}
		linkBudget -= cost
		return location
	}
	allEvidence := append([]api.ProfileRegressionEvidence(nil), signal.Evidence...)
	for _, check := range signal.RouteChecks {
		allEvidence = append(allEvidence, check.CodeEvidence...)
	}
	for evidenceIndex := range allEvidence {
		for frameIndex := range allEvidence[evidenceIndex].Frames {
			frame := &allEvidence[evidenceIndex].Frames[frameIndex]
			if signal.Baseline != nil {
				baseline, ok := sources[signal.Baseline.DeploymentID]
				if ok && frame.BaselinePath != "" {
					frame.BaselineSource = link(baseline.origin, frame.BaselinePath, frame.BaselineLine)
				}
			}
			if signal.Candidate != nil {
				candidateSource, ok := sources[signal.Candidate.DeploymentID]
				if ok && frame.CandidatePath != "" {
					frame.CandidateSource = link(candidateSource.origin, frame.CandidatePath, frame.CandidateLine)
				}
			}
		}
	}
}

func profileCanaryComparisonURL(slug string, signal *api.CanaryProfileSignal) string {
	if slug == "" || signal == nil || signal.CompletedAt == nil || signal.Baseline == nil || signal.Candidate == nil ||
		signal.Status != "regressed" && signal.Status != "no_regression_detected" && signal.Status != "inconclusive" {
		return ""
	}
	baseline, candidate := *signal.Baseline, *signal.Candidate
	if baseline.DeploymentID == "" || candidate.DeploymentID == "" || candidate.Runtime == "" || baseline.Runtime != candidate.Runtime ||
		baseline.Start.IsZero() || !baseline.End.After(baseline.Start) || candidate.Start.IsZero() || !candidate.End.After(candidate.Start) {
		return ""
	}
	values := url.Values{
		"canary_deployment": {candidate.DeploymentID},
		"canary_step":       {strconv.Itoa(signal.CanaryStep)},
		"canary_started_at": {signal.CanaryStepStartedAt.Format(time.RFC3339Nano)},
		"canary_revision":   {strconv.FormatInt(signal.PolicyRevision, 10)},
		"deployment_id":     {candidate.DeploymentID},
		"runtime":           {candidate.Runtime},
		"start":             {candidate.Start.Format(time.RFC3339Nano)},
		"end":               {candidate.End.Format(time.RFC3339Nano)},
		"baseline_id":       {baseline.DeploymentID},
		"baseline_start":    {baseline.Start.Format(time.RFC3339Nano)},
		"baseline_end":      {baseline.End.Format(time.RFC3339Nano)},
	}
	return "/dashboard/apps/" + url.PathEscape(slug) + "/profiles?" + values.Encode() + "#diff-flamegraph"
}

func profileDeploymentScope(scope string) string {
	if scope == "" {
		return api.DefaultEnvScope
	}
	return scope
}
