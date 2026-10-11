package main

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/routerequirements"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func (s *server) routeLifecycleApprovalTarget(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.RouteLifecycleApprovalStore, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return app, nil, false
	}
	store, ok := s.store.(state.RouteLifecycleApprovalStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("lifecycle approvals are unavailable"))
	}
	return app, store, ok
}
func (s *server) postRouteLifecycleApproval(w http.ResponseWriter, r *http.Request, acct state.Account) {
	actor, ok := routeRemovalActor(w, r, acct)
	if !ok {
		return
	}
	var request api.ApproveRouteLifecycleRequest
	if decodeJSONSized(r, &request, api.RoutePolicyRequestMaxBytes) != nil {
		api.WriteProblem(w, api.ErrValidation("invalid lifecycle approval request"))
		return
	}
	app, store, ok := s.routeLifecycleApprovalTarget(w, r, acct)
	if !ok {
		return
	}
	receipt, err := store.ApproveRouteLifecycle(r.Context(), acct.ID, app.ID, actor, request, s.routeGateFingerprint, s.validateRouteLifecycleCompatibility)
	if err != nil {
		s.routeLifecycleApprovalError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "route_lifecycle.approved", &acct.ID, map[string]any{"app_id": app.ID, "approval_id": receipt.ID, "actor": actor, "gate_revision": receipt.GateRevision, "baseline_deployment_id": receipt.BaselineDeploymentID, "candidate_deployment_id": receipt.CandidateDeploymentID, "mapping_sha256": receipt.MappingSHA256, "valid_until": receipt.ValidUntil})
	writeJSON(w, http.StatusCreated, receipt)
}
func (s *server) getRouteLifecycleApproval(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("approval_id")
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || parsed.String() != id {
		api.WriteProblem(w, api.ErrValidation("approval_id must be a canonical UUID"))
		return
	}
	app, store, ok := s.routeLifecycleApprovalTarget(w, r, acct)
	if !ok {
		return
	}
	receipt, err := store.GetRouteLifecycleApproval(r.Context(), acct.ID, app.ID, id)
	if err != nil {
		s.routeLifecycleApprovalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}
func (s *server) routeLifecycleApprovalError(w http.ResponseWriter, err error) {
	var blocked *state.RouteLifecycleReviewBlockedError
	switch {
	case errors.Is(err, state.ErrRouteLifecycleReviewChanged):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeRouteLifecycleReviewChanged, "Lifecycle review inputs changed", "Read fresh capture hashes, policy revisions and the saved route-check configuration hash, then review again."))
	case errors.As(err, &blocked):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeRouteLifecycleReviewRequired, "Lifecycle compatibility review required", blocked.Reason))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("supply exact capture hashes, policy revisions, configuration_sha256 and explicit successor mappings"))
	case errors.Is(err, state.ErrRouteGatePlan):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Lifecycle approvals unavailable", "The account plan must support captured contracts and traffic splitting."))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "app, deployment, saved route requirements or lifecycle approval")
	default:
		api.WriteProblem(w, api.ErrCapacity("lifecycle approval could not be read or persisted"))
	}
}
func (s *server) validateRouteLifecycleCompatibility(snapshot state.RoutePolicySnapshot, before, after *state.RoutePolicyContract, mappings []api.RouteLifecycleMapping) error {
	baseline, err := openapidiff.LoadBytes(before.Doc)
	if err != nil {
		return &state.RouteLifecycleReviewBlockedError{Reason: "baseline_contract_unavailable"}
	}
	candidate, err := openapidiff.LoadBytes(after.Doc)
	if err != nil {
		return &state.RouteLifecycleReviewBlockedError{Reason: "candidate_contract_unavailable"}
	}
	for _, captured := range []struct {
		spec     *openapidiff.Spec
		contract *state.RoutePolicyContract
	}{{baseline, before}, {candidate, after}} {
		if routerequirements.CandidateInventory(captured.spec, captured.contract.DeploymentID, captured.contract.SHA256).Status != "available" {
			return &state.RouteLifecycleReviewBlockedError{Reason: "inline_rooted_operation_contracts_required"}
		}
	}
	for _, m := range mappings {
		targetSnapshot := snapshot
		targetContract := after
		verified, claimed := false, false
		if resolved, ok := snapshot.LifecycleSuccessors[m.Method+" "+m.Path]; ok {
			targetSnapshot, targetContract = resolved.Snapshot, resolved.Snapshot.Contract
			verified, claimed = resolved.VerifiedDomain, resolved.Claimed
		} else if m.SuccessorAppID != "" {
			return &state.RouteLifecycleReviewBlockedError{Reason: "successor_destination_unavailable"}
		}
		u, _ := url.Parse(m.SuccessorURL)
		canonical := s.routeRequirementsContext(targetSnapshot).Host
		if u == nil || u.Host != strings.ToLower(u.Host) || u.Port() != "" || claimed || (u.Host != canonical && (!verified || strings.HasSuffix(u.Host, "."+strings.TrimPrefix(s.domain, ".")) || strings.HasSuffix(u.Host, wire.DeployWildcardSuffix))) {
			return &state.RouteLifecycleReviewBlockedError{Reason: "successor_host_unverified_or_ambiguous"}
		}
		if targetSnapshot.App.OnlyAllowDeclaredRoutes {
			return &state.RouteLifecycleReviewBlockedError{Reason: "successor_declared_route_policy_requires_review"}
		}
		if targetSnapshot.App.MaintenanceMode || (targetSnapshot.App.ConsumerAuthMode == api.ConsumerAuthModeRequired && snapshot.App.ConsumerAuthMode != api.ConsumerAuthModeRequired) {
			return &state.RouteLifecycleReviewBlockedError{Reason: "successor_destination_unavailable"}
		}
		for _, rule := range targetSnapshot.Rules {
			switch rule.Kind {
			case "route", "redirect", "rewrite", "async", "maintenance":
				// A URL may name a template, and request headers are unspecified. Reject
				// potentially selecting rules rather than guessing one path/header sample.
				if rule.Enabled && edgeruletrace.HostMatches(rule.MatchHost, u.Host) && edgeruletrace.MethodMatches(rule.MatchMethods, m.SuccessorMethod) {
					return &state.RouteLifecycleReviewBlockedError{Reason: "successor_edge_routing_requires_review"}
				}
			}
		}
		successor, err := openapidiff.LoadBytes(targetContract.Doc)
		if err != nil || routerequirements.CandidateInventory(successor, targetContract.DeploymentID, targetContract.SHA256).Status != "available" {
			return &state.RouteLifecycleReviewBlockedError{Reason: "inline_rooted_operation_contracts_required"}
		}
		if err := compareLifecycleSuccessorOperations(baseline, successor, m); err != nil {
			return err
		}
	}

	return nil
}

func compareLifecycleSuccessorOperations(baseline, successor *openapidiff.Spec, m api.RouteLifecycleMapping) error {
	for _, op := range []struct {
		spec         *openapidiff.Spec
		method, path string
	}{{baseline, m.Method, m.Path}, {successor, m.SuccessorMethod, m.SuccessorPath}} {
		item := op.spec.Paths[op.path]
		if item == nil || item.Methods[strings.ToLower(op.method)] == nil || len(item.Methods[strings.ToLower(op.method)].Responses) == 0 {
			return &state.RouteLifecycleReviewBlockedError{Reason: "complete_operation_response_contracts_required"}
		}
	}
	pair, err := openapidiff.CompareRoutePair(baseline, m.Method, m.Path, successor, m.SuccessorMethod, m.SuccessorPath)
	if err != nil || pair.Status != "no_supported_breaks" {
		return &state.RouteLifecycleReviewBlockedError{Reason: "successor_contract_not_compatible"}
	}
	return nil
}
