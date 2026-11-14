package apidserver

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261111114059 amendment 2026-11-14: a source deploy (source ref,
// tarball, upload commit) applies the source's gregale.yaml deployment
// settings itself, so an agent's source deploy is priced for the warm
// capacity they add, as the same settings sent with PATCH /v1/apps/{slug}
// are. Before this, a gregale.yaml that added replicas or a warm floor
// bypassed the agent's budget.

// sourceManifestSettingsRequest is the app update a manifest's deployment
// settings amount to: its lifecycle (execution mode, replicas) and its
// scaling policy.
func sourceManifestSettingsRequest(m *gregalemanifest.Manifest) (api.UpdateAppRequest, bool) {
	if m == nil {
		return api.UpdateAppRequest{}, false
	}
	req, lifecycle := m.LifecycleRequest()
	if policy := m.ScalingPolicyRequest(); policy != nil {
		req.ScalingPolicy, req.SetScalingPolicy = policy, true
		return req, true
	}
	return req, lifecycle
}

// chargeSourceManifest prices m's deployment settings for app against the
// calling agent's budget, before any of them is written. Past the budget it
// answers the approval request as a Problem, which the source deploy
// handlers return; commit records the spend once the settings are applied.
func (s *server) chargeSourceManifest(r *http.Request, acct state.Account, app state.App) func(m *gregalemanifest.Manifest) (func(), *api.Problem) {
	return func(m *gregalemanifest.Manifest) (func(), *api.Problem) {
		req, ok := sourceManifestSettingsRequest(m)
		if !ok || s.authMw == nil || !s.authMw.ChargesAgentBudget(r) {
			return func() {}, nil
		}
		rec := &problemRecorder{header: http.Header{}}
		commit, answered := s.chargeAppUpdate(rec, r, acct, app, req, app.RAMMB)
		if !answered {
			return commit, nil
		}
		return func() {}, rec.problem()
	}
}

// problemRecorder keeps the Problem a budget check wrote, so a handler that
// answers with a *api.Problem can return it unchanged.
type problemRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (p *problemRecorder) Header() http.Header         { return p.header }
func (p *problemRecorder) Write(b []byte) (int, error) { return p.body.Write(b) }
func (p *problemRecorder) WriteHeader(status int)      { p.status = status }

func (p *problemRecorder) problem() *api.Problem {
	var out api.Problem
	if json.Unmarshal(p.body.Bytes(), &out) != nil || out.Status == 0 {
		return api.ErrCapacity("could not check the agent's budget; retry shortly")
	}
	return &out
}
