package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) routeRemovalTarget(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.RouteRemovalStore, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return state.App{}, nil, false
	}
	store, ok := s.store.(state.RouteRemovalStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("server route removal policy is unavailable"))
	}
	return app, store, ok
}
func (s *server) getRouteRemovalPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.routeRemovalTarget(w, r, acct)
	if !ok {
		return
	}
	p, err := store.GetRouteRemovalPolicy(r.Context(), acct.ID, app.ID)
	if err != nil {
		s.routeRemovalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}
func routeRemovalActor(w http.ResponseWriter, r *http.Request, acct state.Account) (string, bool) {
	p, ok := principalFrom(r)
	if !ok || p.Acct.ID != acct.ID {
		api.WriteProblem(w, api.ErrInternal("authenticated approval identity is unavailable"))
		return "", false
	}
	_, _, membership, _ := authmw.PrincipalFrom(r)
	if membership != nil && membership.Role != state.OrgRoleOwner && membership.Role != state.OrgRoleAdmin {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Owner approval required", "Only the app account owner or an organization owner/admin can change or approve this policy."))
		return "", false
	}
	if p.Key != nil {
		if p.Key.ID == "" || p.Key.PlatformTenantID != "" || p.Key.AppID != "" {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Account admin credential required", "Use an account admin key or owner session."))
			return "", false
		}
		return "account:" + acct.ID + "/key:" + p.Key.ID, true
	}
	return "account:" + acct.ID + "/session", true
}
func (s *server) putRouteRemovalPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	actor, ok := routeRemovalActor(w, r, acct)
	if !ok {
		return
	}
	var request api.SetRouteRemovalPolicyRequest
	if decodeJSONSized(r, &request, 16*1024) != nil {
		api.WriteProblem(w, api.ErrValidation("invalid route removal policy request"))
		return
	}
	app, store, ok := s.routeRemovalTarget(w, r, acct)
	if !ok {
		return
	}
	p, err := store.SetRouteRemovalPolicy(state.WithRouteRemovalActor(r.Context(), actor), acct.ID, app.ID, request)
	if err != nil {
		s.routeRemovalError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "route_removal.policy_saved", &acct.ID, map[string]any{"app_id": app.ID, "actor": actor, "policy": p})
	writeJSON(w, http.StatusOK, p)
}
func (s *server) postRouteRemovalApproval(w http.ResponseWriter, r *http.Request, acct state.Account) {
	actor, ok := routeRemovalActor(w, r, acct)
	if !ok {
		return
	}
	var request api.ApproveRouteRemovalRequest
	if decodeJSONSized(r, &request, 2*1024*1024) != nil {
		api.WriteProblem(w, api.ErrValidation("invalid route removal approval request"))
		return
	}
	app, store, ok := s.routeRemovalTarget(w, r, acct)
	if !ok {
		return
	}
	policy, err := store.GetRouteRemovalPolicy(r.Context(), acct.ID, app.ID)
	if err != nil {
		s.routeRemovalError(w, err)
		return
	}
	grace, _ := time.ParseDuration(policy.GracePeriod)
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled || grace+3*time.Minute > time.Duration(limits.DebugTelemetryRetentionDays)*24*time.Hour {
		api.WriteProblem(w, api.ErrValidation("the configured grace period requires retained server telemetry; allow three minutes of aggregation margin within your telemetry retention or upgrade the plan"))
		return
	}
	approval, err := store.ApproveRouteRemoval(r.Context(), acct.ID, app.ID, actor, request, validateRouteRemovalCompatibility)
	if err != nil {
		s.routeRemovalError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "route_removal.approved", &acct.ID, map[string]any{"app_id": app.ID, "approval_id": approval.ID, "actor": approval.ApprovedBy, "policy_revision": approval.PolicyRevision, "baseline_deployment_id": approval.BaselineDeploymentID, "candidate_deployment_id": approval.CandidateDeploymentID, "mapping_sha256": approval.MappingSHA256, "valid_until": approval.ValidUntil})
	writeJSON(w, http.StatusCreated, approval)
}
func validateRouteRemovalCompatibility(b, c state.RouteRemovalContract, mappings []api.RouteRemovalMapping) error {
	before, err := state.RouteRemovalOperations(b.Document)
	if err != nil {
		return err
	}
	after, err := state.RouteRemovalOperations(c.Document)
	if err != nil {
		return err
	}
	removed := map[string]bool{}
	for k := range before {
		if !after[k] {
			removed[k] = true
		}
	}
	if len(removed) != len(mappings) {
		return &state.RouteRemovalBlockedError{Reason: "mapping_must_cover_exact_removed_operations"}
	}
	baseline, err := openapidiff.LoadBytes(b.Document)
	if err != nil {
		return &state.RouteRemovalBlockedError{Reason: "baseline_contract_unavailable"}
	}
	candidate, err := openapidiff.LoadBytes(c.Document)
	if err != nil {
		return &state.RouteRemovalBlockedError{Reason: "candidate_contract_unavailable"}
	}
	for _, m := range mappings {
		if !removed[m.Method+" "+m.Path] || !before[m.SuccessorMethod+" "+m.SuccessorPath] || !after[m.SuccessorMethod+" "+m.SuccessorPath] {
			return &state.RouteRemovalBlockedError{Reason: "successor_must_exist_in_both_staged_baseline_and_candidate"}
		}
		for _, endpoint := range []struct {
			spec         *openapidiff.Spec
			method, path string
		}{
			{baseline, m.Method, m.Path}, {baseline, m.SuccessorMethod, m.SuccessorPath}, {candidate, m.SuccessorMethod, m.SuccessorPath},
		} {
			item := endpoint.spec.Paths[endpoint.path]
			if item == nil {
				return &state.RouteRemovalBlockedError{Reason: "operation_contract_unavailable"}
			}
			op := item.Methods[strings.ToLower(endpoint.method)]
			if op == nil || len(op.Responses) == 0 {
				return &state.RouteRemovalBlockedError{Reason: "operation_response_contract_unavailable"}
			}
			if _, referenced := op.Raw["$ref"]; referenced {
				return &state.RouteRemovalBlockedError{Reason: "operation_reference_unsupported"}
			}
		}
		for _, target := range []*openapidiff.Spec{baseline, candidate} {
			pair, err := openapidiff.CompareRoutePair(baseline, m.Method, m.Path, target, m.SuccessorMethod, m.SuccessorPath)
			if err != nil || pair.Status != "no_supported_breaks" {
				return &state.RouteRemovalBlockedError{Reason: fmt.Sprintf("successor_contract_not_compatible: %s %s", m.Method, m.Path)}
			}
		}
	}
	return nil
}
func (s *server) getRouteRemovalCheck(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.routeRemovalTarget(w, r, acct)
	if !ok {
		return
	}
	id := r.URL.Query().Get("deployment_id")
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || parsed.String() != id {
		api.WriteProblem(w, api.ErrValidation("deployment_id must be a canonical UUID"))
		return
	}
	check, err := store.CheckRouteRemoval(r.Context(), acct.ID, app.ID, id)
	if err != nil {
		s.routeRemovalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, check)
}
func routeRemovalBlockedProblem(err error) *api.Problem {
	var gate *state.RouteGateBlockedError
	var lifecycleDB *pgconn.PgError
	if errors.As(err, &gate) {
		return api.NewProblem(http.StatusConflict, api.CodeRouteGateBlocked, "Route lifecycle blocked traffic", strings.Join(gate.Decision.Reasons, ", ")).WithHint("Resolve lifecycle findings and approve any changed successor for the exact captures before retrying production traffic.")
	}
	if errors.As(err, &lifecycleDB) && lifecycleDB.ConstraintName == "production_lifecycle_required" {
		return api.NewProblem(http.StatusConflict, api.CodeRouteGateBlocked, "Route lifecycle review required", "The transaction has no current lifecycle authorization.").WithHint("Refresh capture and lifecycle review, then retry the production traffic change.")
	}

	var blocked *state.RouteRemovalBlockedError
	if !errors.As(err, &blocked) {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "route_removal_required" {
			return nil
		}
		blocked = &state.RouteRemovalBlockedError{Reason: pgErr.Detail}
	}
	return api.NewProblem(http.StatusConflict, api.CodeRouteRemovalRequired, "Route removal policy blocked traffic", strings.TrimSpace(blocked.Reason)).WithHint("Read /v1/apps/{slug}/route-removal/check?deployment_id=UUID; obtain a fresh authenticated approval for the exact production baseline, candidate contracts and successor mapping before increasing traffic.")
}
func (s *server) routeRemovalError(w http.ResponseWriter, err error) {
	if p := routeRemovalBlockedProblem(err); p != nil {
		api.WriteProblem(w, p)
		return
	}
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "app or deployment")
	case errors.Is(err, state.ErrRouteRemovalPolicyRevision):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeRouteRemovalPolicyChanged, "Route removal policy changed", "Read the current policy revision and retry."))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid route removal request; supply a current policy revision, exact contract hashes, unique mappings and explicit observed-only acknowledgement"))
	default:
		api.WriteProblem(w, api.ErrCapacity("server route removal policy could not be read or updated"))
	}
}
