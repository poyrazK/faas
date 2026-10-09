package main

// ADR-905 §2: versioned edge-rule sets. The store records a version of an
// app's whole rule set on every committed change; these handlers list and
// read versions, roll back to one, and implement ETag / If-Match so two
// editors cannot silently overwrite each other.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const edgeRuleVersionListLimit = 50

func (s *server) edgeRuleVersions() (state.EdgeRuleSetVersionStore, bool) {
	store, ok := s.store.(state.EdgeRuleSetVersionStore)
	return store, ok
}

// setEdgeRuleSetETag stamps the app's current rule-set version as the ETag.
// Best effort: a store without versioning, or a read error, omits it.
func (s *server) setEdgeRuleSetETag(ctx context.Context, w http.ResponseWriter, appID string) {
	store, ok := s.edgeRuleVersions()
	if !ok {
		return
	}
	if version, err := store.LatestEdgeRuleSetVersion(ctx, appID); err == nil {
		w.Header().Set("ETag", api.EdgeRuleSetETag(version))
	}
}

// edgeRuleIfMatchFailed enforces an optional If-Match on an edge-rule
// mutation. It runs after prepareEdgeRuleMutation, which holds the per-app
// mutation lock, so the check and the write cannot interleave with another
// apid's mutation of the same app. It writes the 412 and returns true when
// the precondition fails.
func (s *server) edgeRuleIfMatchFailed(ctx context.Context, w http.ResponseWriter, r *http.Request, appID string) bool {
	header := strings.TrimSpace(r.Header.Get("If-Match"))
	if header == "" || header == "*" {
		return false
	}
	store, ok := s.edgeRuleVersions()
	if !ok {
		return false
	}
	current, err := store.LatestEdgeRuleSetVersion(ctx, appID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the edge-rule set version"))
		return true
	}
	want := api.EdgeRuleSetETag(current)
	for _, candidate := range strings.Split(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(candidate), "W/") == want {
			return false
		}
	}
	w.Header().Set("ETag", want)
	api.WriteProblem(w, api.ErrEdgeRulesVersionMismatch(header, current))
	return true
}

func edgeRuleSetVersionResponse(v state.EdgeRuleSetVersion, latest int) api.EdgeRuleSetVersionResponse {
	out := api.EdgeRuleSetVersionResponse{
		Version: v.Version, RuleCount: v.RuleCount, RulesSHA256: v.RulesSHA256,
		CreatedAt: v.CreatedAt, Current: v.Version == latest,
	}
	for _, rule := range v.Rules {
		out.Rules = append(out.Rules, edgeRuleResponse(rule))
	}
	return out
}

// GET /v1/apps/{slug}/edge-rules/versions — newest first, without rules.
func (s *server) listEdgeRuleSetVersions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.edgeRuleVersions()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("edge-rule versioning is unavailable"))
		return
	}
	versions, err := store.ListEdgeRuleSetVersions(r.Context(), app.ID, edgeRuleVersionListLimit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list edge-rule versions"))
		return
	}
	latest := 0
	if len(versions) > 0 {
		latest = versions[0].Version
	}
	out := make([]api.EdgeRuleSetVersionResponse, 0, len(versions))
	for _, v := range versions {
		out = append(out, edgeRuleSetVersionResponse(v, latest))
	}
	w.Header().Set("ETag", api.EdgeRuleSetETag(latest))
	writeJSON(w, http.StatusOK, out)
}

// GET /v1/apps/{slug}/edge-rules/versions/{version} — one version with rules.
func (s *server) getEdgeRuleSetVersion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		api.WriteProblem(w, api.ErrValidation("version must be a positive integer"))
		return
	}
	store, ok := s.edgeRuleVersions()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("edge-rule versioning is unavailable"))
		return
	}
	v, err := store.GetEdgeRuleSetVersion(r.Context(), app.ID, version)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such edge-rule version")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read edge-rule version"))
		return
	}
	latest, _ := store.LatestEdgeRuleSetVersion(r.Context(), app.ID)
	writeJSON(w, http.StatusOK, edgeRuleSetVersionResponse(v, latest))
}

// POST /v1/apps/{slug}/edge-rules/rollback — restore a recorded version.
func (s *server) rollbackEdgeRules(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.RollbackEdgeRulesRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Version < 1 {
		api.WriteProblem(w, api.ErrValidation("body must be {\"version\": <positive integer>}"))
		return
	}
	store, ok := s.edgeRuleVersions()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("edge-rule versioning is unavailable"))
		return
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("plan limits not loaded"))
		return
	}
	target, err := store.GetEdgeRuleSetVersion(r.Context(), app.ID, req.Version)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such edge-rule version")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read edge-rule version"))
		return
	}
	hosts, err := s.edgeRuleRollbackHosts(r.Context(), app.ID, target.Rules)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read current edge rules"))
		return
	}
	convergence, err := s.prepareEdgeRuleMutation(r.Context(), app.ID, "", "rolled_back", hosts...)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("edge-rule fleet convergence is unavailable; nothing was rolled back"))
		return
	}
	if s.edgeRuleIfMatchFailed(r.Context(), w, r, app.ID) {
		convergence.abort(r.Context())
		return
	}
	restored, err := store.RestoreEdgeRuleSetVersion(r.Context(), app.ID, req.Version, limits)
	if err != nil {
		convergence.abort(r.Context())
		s.writeEdgeRuleRestoreError(w, err)
		return
	}
	s.log.Info("edge rules rolled back", "app", app.ID, "account", acct.ID, "version", req.Version, "rules", len(restored.Rules))
	s.audit.Emit(r.Context(), "edge_rule.rolled_back", &acct.ID, map[string]any{
		auditKeyAppID: app.ID,
		"version":     req.Version,
		"rule_count":  len(restored.Rules),
	})
	if err := convergence.apply(r.Context(), ""); err != nil {
		convergence.setResponseState(w, "converging")
		s.log.Error("edge rules rolled back but fleet convergence is incomplete", "app", app.ID, "generation", convergence.generation, "err", err)
		api.WriteProblem(w, api.ErrCapacity("edge rules were rolled back but the serving fleet has not acknowledged it; read the current rules before retrying"))
		return
	}
	convergence.setResponseState(w, "active")
	s.setEdgeRuleSetETag(r.Context(), w, app.ID)
	out := make([]api.EdgeRuleResponse, 0, len(restored.Rules))
	for _, rule := range restored.Rules {
		out = append(out, edgeRuleResponse(rule))
	}
	writeJSON(w, http.StatusOK, out)
}

// edgeRuleRollbackHosts is the fence scope of a rollback: every host a
// current rule or a restored rule matches, since both sets change policy.
func (s *server) edgeRuleRollbackHosts(ctx context.Context, appID string, restored []state.EdgeRule) ([]string, error) {
	current, err := s.store.ListEdgeRulesForApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	var hosts []string
	for _, set := range [][]state.EdgeRule{current, restored} {
		for _, rule := range set {
			if _, ok := seen[rule.MatchHost]; !ok {
				seen[rule.MatchHost] = struct{}{}
				hosts = append(hosts, rule.MatchHost)
			}
		}
	}
	return hosts, nil
}

func (s *server) writeEdgeRuleRestoreError(w http.ResponseWriter, err error) {
	var qe *state.EdgeRuleQuotaError
	switch {
	case errors.As(err, &qe):
		api.WriteProblem(w, api.ErrValidation(edgeRuleRestoreQuotaDetail(qe)))
	case errors.Is(err, state.ErrEdgeRuleSetVersionReference):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Version cannot be restored",
			"the version references a CORS preset that no longer exists"))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such edge-rule version")
	default:
		api.WriteProblem(w, api.ErrCapacity("could not roll back edge rules"))
	}
}

func edgeRuleRestoreQuotaDetail(qe *state.EdgeRuleQuotaError) string {
	if qe.Kind != "" {
		return "the version has " + strconv.Itoa(qe.Observed) + " kind=" + qe.Kind + " rules; the current plan allows " + strconv.Itoa(qe.Limit)
	}
	return "the version has " + strconv.Itoa(qe.Observed) + " rules; the current plan allows " + strconv.Itoa(qe.Limit)
}
