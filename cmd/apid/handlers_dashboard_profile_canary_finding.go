package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

type profileCanaryFinding struct {
	HasEvidence bool
	RequestMix  dashboard.ProfileRequestMixView
	Signal      api.CanaryProfileSignal
	Evidence    api.ProfileRegressionEvidence
	Attribution profileSourceAttribution
	Traffic     dashboard.ProfileTrafficView
}

func (s *server) canaryFindingRequest(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) (*http.Request, *profileCanaryFinding, bool) {
	q := r.URL.Query()
	if !q.Has("canary_deployment") && !q.Has("canary_finding") {
		return r, nil, true
	}
	store, ok := s.store.(state.ProfileCanaryCheckStore)
	step, e1 := strconv.Atoi(q.Get("canary_step"))
	revision, e2 := strconv.ParseInt(q.Get("canary_revision"), 10, 64)
	started, e3 := time.Parse(time.RFC3339Nano, q.Get("canary_started_at"))
	index, e4 := 0, error(nil)
	if q.Has("canary_finding") {
		index, e4 = strconv.Atoi(q.Get("canary_finding"))
	}
	if !ok || e1 != nil || e2 != nil || e3 != nil || e4 != nil || step < 0 || revision <= 0 || index < 0 || q.Has("investigation_id") {
		http.NotFound(w, r)
		return r, nil, false
	}
	signal, err := store.GetProfileCanaryCheck(r.Context(), acct.ID, app.ID, state.ProfileCanaryCheckKey{DeploymentID: q.Get("canary_deployment"), CanaryStep: step, CanaryStepStartedAt: started, PolicyRevision: revision})
	if err != nil || (q.Has("canary_finding") && index >= len(signal.Evidence)) || profileCanaryComparisonURL(app.Slug, &signal) == "" {
		http.NotFound(w, r)
		return r, nil, false
	}
	finding := &profileCanaryFinding{HasEvidence: q.Has("canary_finding")}
	if finding.HasEvidence {
		finding.Attribution = s.attributeCanaryFinding(r.Context(), acct, app, &signal, signal.Evidence[index])
		finding.Evidence = signal.Evidence[index]
	} else {
		s.enrichCanaryProfileSignal(r.Context(), app, &signal, nil)
	}
	finding.Signal = signal
	finding.Traffic = dashboard.BuildProfileTraffic(signal)
	finding.RequestMix = s.canaryRequestMix(r, acct, app, signal)
	return profileInvestigationRequest(r, api.ProfileInvestigationInput{Baseline: *signal.Baseline, Candidate: *signal.Candidate}), finding, true
}
