package edgerules

// Edge-rule mutation cores (ADR-20261020161310). The create and update
// handlers and the route-insight fixes share them, so a programmatic rule
// gets the same validation, plan quotas, fleet convergence and audit as one
// made through the API.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// ruleMutation is a create or update outcome.
type ruleMutation struct {
	row         state.EdgeRule
	convergence *EdgeRuleConvergence
	// converging: the rule was saved but the fleet has not acknowledged it.
	converging bool
	// etag is the current rule-set ETag when the precondition failed.
	etag string
}

// precondition runs once the convergence fence is prepared and before the
// write; a problem aborts the mutation (the HTTP If-Match check).
type precondition func(ctx context.Context) (etag string, prob *api.Problem)

// writeRuleMutation writes a mutation outcome: the If-Match ETag or the
// convergence state with a problem, or the rule with the rule-set ETag.
func (s *Service) writeRuleMutation(ctx context.Context, w http.ResponseWriter, m ruleMutation, prob *api.Problem, status int) {
	if prob != nil {
		if m.etag != "" {
			w.Header().Set("ETag", m.etag)
		}
		if m.converging {
			m.convergence.SetResponseState(w, "converging")
		}
		api.WriteProblem(w, prob)
		return
	}
	m.convergence.SetResponseState(w, "active")
	s.setEdgeRuleSetETag(ctx, w, m.row.AppID)
	httpkit.WriteJSON(w, status, EdgeRuleResponse(m.row))
}

// CreateRule creates an edge rule for app through the same path as
// POST /v1/apps/{slug}/edge-rules, without an If-Match precondition.
func (s *Service) CreateRule(ctx context.Context, acct state.Account, app state.App, req api.CreateEdgeRuleRequest) (api.EdgeRuleResponse, *api.Problem) {
	if prob := planKindProblem(acct, req.Kind); prob != nil {
		return api.EdgeRuleResponse{}, prob
	}
	m, prob := s.createRule(ctx, acct, app, req, nil)
	if prob != nil {
		return api.EdgeRuleResponse{}, prob
	}
	return EdgeRuleResponse(m.row), nil
}

// UpdateRule changes one of the account's edge rules through the same path
// as PATCH /v1/edge-rules/{id}, without an If-Match precondition.
func (s *Service) UpdateRule(ctx context.Context, acct state.Account, ruleID string, req api.UpdateEdgeRuleRequest) (api.EdgeRuleResponse, *api.Problem) {
	row, prob := s.ruleForUpdate(ctx, acct, ruleID)
	if prob != nil {
		return api.EdgeRuleResponse{}, prob
	}
	m, prob := s.updateRule(ctx, acct, row, req, nil)
	if prob != nil {
		return api.EdgeRuleResponse{}, prob
	}
	return EdgeRuleResponse(m.row), nil
}

// planKindProblem is the plan-kind gate. Hobby+ unlocks jwt and ip; Free
// customers get a clean 402 before the app is loaded (mirrors
// ErrPlanAlertRulesNotAllowed) so a Free customer doesn't get a 404 leak.
//
// EdgeRuleKindGeo is NOT paid-only (ADR-091 D21 sub-decision hop) — Free
// customers can post a single geo rule (cap=1). The per-kind cap is enforced
// inside the store's CreateEdgeRuleIfUnderQuota FOR UPDATE lock and surfaced
// via qe.PerKind / ErrPlanEdgeRuleKindQuotaReached.
func planKindProblem(acct state.Account, kind string) *api.Problem {
	if !state.EdgeRuleKind(kind).IsPaidOnly() {
		return nil
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return api.ErrCapacity("plan limits not loaded")
	}
	switch {
	case kind == string(state.EdgeRuleKindJWT) && !limits.EdgeRulesJWTAllowed,
		kind == string(state.EdgeRuleKindIP) && !limits.EdgeRulesIPAllowed,
		kind == string(state.EdgeRuleKindAsync) && !limits.AsyncInvokeAllowed:
		return api.ErrPlanEdgeRuleKindNotAllowed(acct.Plan, kind)
	}
	return nil
}

// ruleForUpdate loads one of the account's rules for an update; respond
// rules stay on preview applications.
func (s *Service) ruleForUpdate(ctx context.Context, acct state.Account, id string) (state.EdgeRule, *api.Problem) {
	notFound := api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such edge rule")
	row, err := s.Host.Store().GetEdgeRuleByID(ctx, id)
	if err != nil || row.AccountID != acct.ID {
		return state.EdgeRule{}, notFound
	}
	if row.Kind == state.EdgeRuleKindRespond {
		app, appErr := s.Host.Store().AppByID(ctx, row.AppID)
		if appErr != nil {
			return state.EdgeRule{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such app")
		}
		if app.PreviewOfSlug == "" {
			return state.EdgeRule{}, api.ErrValidation("respond edge rules are only allowed on preview applications")
		}
	}
	return row, nil
}

func (s *Service) createRule(ctx context.Context, acct state.Account, app state.App, req api.CreateEdgeRuleRequest, check precondition) (ruleMutation, *api.Problem) {
	var m ruleMutation
	if req.Kind == string(state.EdgeRuleKindRespond) && app.PreviewOfSlug == "" {
		return m, api.ErrValidation("respond edge rules are only allowed on preview applications")
	}
	if req.Kind == string(state.EdgeRuleKindAsync) && !app.AcceptsRequestInvocations() {
		return m, api.ErrInvocationWorkloadClass(string(app.WorkloadClass), app.Manifest.ExecutionMode)
	}
	if prob := TemplatedMatchPathProblem(req.MatchPath); prob != nil {
		return m, prob
	}
	if prob := ValidateEdgeRuleBody(&req, acct.Plan); prob != nil {
		return m, prob
	}
	if prob := s.createReleaseProblem(acct, req); prob != nil {
		return m, prob
	}
	if prob := s.validateEdgeRuleMatchLists(ctx, acct.ID, req.Match); prob != nil {
		return m, prob
	}
	if prob := s.ValidateEdgeRuleAsyncDestinations(ctx, app.ID, acct.ID, req.Kind, req.Action); prob != nil {
		return m, prob
	}
	// Default priority=100, enabled=true when unset.
	priority := 100
	if req.Priority != nil {
		priority = *req.Priority
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	matchPath := req.MatchPath
	if matchPath == "" {
		matchPath = "/"
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return m, api.ErrCapacity("plan limits not loaded")
	}
	convergence, err := s.PrepareEdgeRuleMutation(ctx, app.ID, "", "created", req.MatchHost)
	if err != nil {
		return m, api.ErrCapacity("edge-rule fleet convergence is unavailable; no rule was created")
	}
	if check != nil {
		etag, checkProb := check(ctx)
		if checkProb != nil {
			convergence.Abort(ctx)
			m.etag = etag
			return m, checkProb
		}
	}
	row, err := s.Host.Store().CreateEdgeRuleIfUnderQuota(ctx, state.CreateEdgeRuleParams{
		AccountID:    acct.ID,
		AppID:        app.ID,
		MatchHost:    strings.ToLower(req.MatchHost),
		MatchPath:    matchPath,
		MatchMethods: req.MatchMethods,
		MatchHeaders: req.MatchHeaders,
		Priority:     priority,
		Enabled:      enabled,
		Kind:         state.EdgeRuleKind(req.Kind),
		Action:       withPenalty(ActionFromBody(req.Kind, req.Action), req.Penalty),
		// ValidateMode (ADR-128 D1): the top-level wire
		// field is the source of truth for kind=validate.
		// If the wire field is empty (client didn't send
		// the new top-level field), fall back to the
		// deprecated action-level field for the
		// back-compat window (D2). Empty result is fine —
		// the SQL coalesce forces 'block' on insert.
		ValidateMode: resolveValidateMode(req.Kind, req.ValidateMode, req.Action),
		Name:         req.Name,
		Description:  req.Description,
		ExpiresAt:    utcTimePtr(req.ExpiresAt),
		Match:        req.Match,
		Mode:         req.Mode,
	}, limits)
	if err != nil {
		convergence.Abort(ctx)
		var qe *state.EdgeRuleQuotaError
		switch {
		case errors.As(err, &qe):
			if qe.PerKind {
				return m, api.ErrPlanEdgeRuleKindQuotaReached(acct.Plan, qe.Kind, qe.Limit, qe.Observed)
			}
			return m, api.ErrPlanLimitEdgeRules(acct.Plan, qe.Limit, qe.Observed)
		case errors.Is(err, state.ErrNotFound):
			return m, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such app")
		case errors.Is(err, state.ErrConflict):
			return m, api.ErrEdgeRuleConflict(err.Error())
		default:
			return m, api.ErrCapacity("could not create edge rule")
		}
	}
	s.Host.Logger().Info("edge rule created",
		"rule", logsanitize.Field(row.ID),
		"app", app.Slug,
		"account", acct.ID,
		"kind", logsanitize.Field(string(row.Kind)),
		slog.Int64("priority", int64(row.Priority)),
	)
	s.Host.EmitAudit(ctx, "edge_rule.created", &acct.ID, map[string]any{
		httpkit.AuditKeyRuleID:       row.ID,
		httpkit.AuditKeyAppID:        row.AppID,
		httpkit.AuditKeyMatchHost:    row.MatchHost,
		httpkit.AuditKeyMatchPath:    row.MatchPath,
		httpkit.AuditKeyMatchMethods: row.MatchMethods,
		httpkit.AuditKeyPriority:     row.Priority,
		httpkit.AuditKeyEnabled:      row.Enabled,
		httpkit.AuditKeyKind:         row.Kind,
	})
	m.row, m.convergence = row, convergence
	if err := convergence.Apply(ctx, row.ID); err != nil {
		m.converging = true
		s.Host.Logger().Error("edge rule persisted but fleet convergence is incomplete", "rule", row.ID, "generation", convergence.Generation, "err", err)
		return m, api.ErrCapacity("edge rule was saved but the serving fleet has not acknowledged it; read the current rule state before retrying this mutation")
	}
	return m, nil
}

func (s *Service) updateRule(ctx context.Context, acct state.Account, row state.EdgeRule, req api.UpdateEdgeRuleRequest, check precondition) (ruleMutation, *api.Problem) {
	var m ruleMutation
	// Validate the optional fields if present.
	if req.MatchHost != nil {
		if prob := api.ValidateEdgeRuleMatchHost(*req.MatchHost); prob != nil {
			return m, prob
		}
	}
	if req.MatchPath != nil {
		if !strings.HasPrefix(*req.MatchPath, "/") || len(*req.MatchPath) > 2048 {
			return m, api.ErrValidation("match_path must start with '/' and be ≤ 2048 chars")
		}
		if prob := TemplatedMatchPathProblem(*req.MatchPath); prob != nil {
			return m, prob
		}
	}
	if req.MatchHeaders != nil {
		matchHeaders, headerErr := api.NormalizeEdgeRuleMatchHeaders(*req.MatchHeaders)
		if headerErr != nil {
			return m, api.ErrValidation(headerErr.Error())
		}
		req.MatchHeaders = &matchHeaders
	}
	if req.Priority != nil {
		if *req.Priority < 0 || *req.Priority > 10000 {
			return m, api.ErrValidation(fmt.Sprintf("priority must be in 0..10000 (got %d)", *req.Priority))
		}
	}
	if req.ExpiresAt != nil && req.ClearExpiresAt {
		return m, api.ErrValidation("expires_at and clear_expires_at are mutually exclusive")
	}
	if prob := api.ValidateEdgeRuleMetadata(req.Name, req.Description, req.ExpiresAt, time.Now()); prob != nil {
		return m, prob
	}
	if req.Match != nil && req.ClearMatch {
		return m, api.ErrValidation("match and clear_match are mutually exclusive")
	}
	if prob := api.ValidateEdgeRuleMatch(req.Match); prob != nil {
		return m, prob
	}
	if prob := s.validateEdgeRuleMatchLists(ctx, acct.ID, req.Match); prob != nil {
		return m, prob
	}
	if req.Mode != nil {
		if prob := api.ValidateEdgeRuleMode(*req.Mode); prob != nil {
			return m, prob
		}
	}
	if prob := validatePenaltyUpdate(row, req); prob != nil {
		return m, prob
	}
	if prob := s.updateReleaseProblem(acct, row, req); prob != nil {
		return m, prob
	}
	matchPath := row.MatchPath
	if req.MatchPath != nil {
		matchPath = *req.MatchPath
	}
	if req.Action != nil {
		prob := ValidateEdgeRuleAction(string(row.Kind), *req.Action, matchPath, acct.Plan)
		if prob != nil {
			return m, prob
		}
		if prob := s.ValidateEdgeRuleAsyncDestinations(ctx, row.AppID, acct.ID, string(row.Kind), *req.Action); prob != nil {
			return m, prob
		}
	} else if req.MatchPath != nil && row.Action.Validate != nil && row.Action.Validate.Parameters != nil {
		// A path-only update must keep the stored path parameters aligned.
		if prob := row.Action.Validate.Parameters.Validate(matchPath); prob != nil {
			return m, prob
		}
	}
	if row.Kind == state.EdgeRuleKindJWT {
		host, path, methods, headers := row.MatchHost, row.MatchPath, row.MatchMethods, row.MatchHeaders
		if req.MatchHost != nil {
			host = *req.MatchHost
		}
		if req.MatchPath != nil {
			path = *req.MatchPath
		}
		if req.MatchMethods != nil {
			methods = *req.MatchMethods
		}
		if req.MatchHeaders != nil {
			headers = *req.MatchHeaders
		}
		raw, _ := json.Marshal(row.Action.JWT)
		if req.Action != nil {
			raw = *req.Action
		}
		if prob := ValidateMCPRuleSelectors(string(row.Kind), host, path, methods, headers, raw); prob != nil {
			return m, prob
		}
	}
	if row.Kind == state.EdgeRuleKindJWT {
		host, path, methods, headers := row.MatchHost, row.MatchPath, row.MatchMethods, row.MatchHeaders
		if req.MatchHost != nil {
			host = *req.MatchHost
		}
		if req.MatchPath != nil {
			path = *req.MatchPath
		}
		if req.MatchMethods != nil {
			methods = *req.MatchMethods
		}
		if req.MatchHeaders != nil {
			headers = *req.MatchHeaders
		}
		raw, _ := json.Marshal(row.Action.JWT)
		if req.Action != nil {
			raw = *req.Action
		}
		if prob := ValidateMCPRuleSelectors(string(row.Kind), host, path, methods, headers, raw); prob != nil {
			return m, prob
		}
	}
	// ValidateMode (ADR-128 D1): top-level wire field on
	// update must be the closed enum when set. nil == "leave
	// alone" (no row update). The action-level field is
	// validated inside ValidateEdgeRuleAction via the
	// EdgeRuleValidateAction.Validate() path.
	if req.ValidateMode != nil &&
		*req.ValidateMode != "" &&
		*req.ValidateMode != api.ValidateModeBlock &&
		*req.ValidateMode != api.ValidateModeObserve &&
		*req.ValidateMode != api.ValidateModeWarn {
		return m, api.ErrValidation(fmt.Sprintf(
			"validate_mode must be one of %q, %q, %q (got %q)",
			api.ValidateModeBlock, api.ValidateModeObserve, api.ValidateModeWarn, *req.ValidateMode))
	}
	// Build the update params. Lowercase the host on write so the
	// gateway matcher can do case-insensitive compares without
	// per-request ToLower.
	if req.MatchHost != nil {
		lowered := strings.ToLower(*req.MatchHost)
		req.MatchHost = &lowered
	}
	hosts := []string{row.MatchHost}
	if req.MatchHost != nil {
		hosts = append(hosts, *req.MatchHost)
	}
	convergence, err := s.PrepareEdgeRuleMutation(ctx, row.AppID, row.ID, "updated", hosts...)
	if err != nil {
		return m, api.ErrCapacity("edge-rule fleet convergence is unavailable; the rule was not updated")
	}
	if check != nil {
		etag, checkProb := check(ctx)
		if checkProb != nil {
			convergence.Abort(ctx)
			m.etag = etag
			return m, checkProb
		}
	}
	updated, err := s.Host.Store().UpdateEdgeRule(ctx, row.ID, penaltyUpdateParams(row, req, edgeRuleUpdateParamsFrom(req, row.Kind)))
	if err != nil {
		convergence.Abort(ctx)
		if errors.Is(err, state.ErrNotFound) {
			return m, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such edge rule")
		}
		return m, api.ErrCapacity("could not update edge rule")
	}
	s.Host.Logger().Info("edge rule updated",
		"rule", logsanitize.Field(updated.ID),
		"app", updated.AppID,
		"account", acct.ID,
		"kind", logsanitize.Field(string(updated.Kind)),
	)
	s.Host.EmitAudit(ctx, "edge_rule.updated", &acct.ID, map[string]any{
		httpkit.AuditKeyRuleID:   updated.ID,
		httpkit.AuditKeyAppID:    updated.AppID,
		httpkit.AuditKeyKind:     updated.Kind,
		httpkit.AuditKeyPriority: updated.Priority,
		httpkit.AuditKeyEnabled:  updated.Enabled,
	})
	m.row, m.convergence = updated, convergence
	if err := convergence.Apply(ctx, updated.ID); err != nil {
		m.converging = true
		s.Host.Logger().Error("edge rule persisted but fleet convergence is incomplete", "rule", updated.ID, "generation", convergence.Generation, "err", err)
		return m, api.ErrCapacity("edge rule was saved but the serving fleet has not acknowledged it; read the current rule state before retrying this mutation")
	}
	return m, nil
}

// withPenalty sets a stored action's penalty box (ADR-20261022105846).
func withPenalty(action state.EdgeRuleAction, p *api.EdgeRulePenalty) state.EdgeRuleAction {
	action.Penalty = stateEdgeRulePenalty(p)
	return action
}

// validatePenaltyUpdate checks a PATCH against the penalty box: a new
// penalty must be valid for the rule's kind, and a waf rule that keeps a
// penalty must stay in block mode.
func validatePenaltyUpdate(row state.EdgeRule, req api.UpdateEdgeRuleRequest) *api.Problem {
	if req.Penalty != nil && req.ClearPenalty {
		return api.ErrValidation("penalty and clear_penalty are mutually exclusive")
	}
	keeps := req.Penalty != nil || (row.Action.Penalty != nil && !req.ClearPenalty)
	if !keeps {
		return nil
	}
	wafMode := ""
	if row.Kind == state.EdgeRuleKindWAF {
		if row.Action.WAF != nil {
			wafMode = row.Action.WAF.Mode
		}
		if req.Action != nil {
			wafMode = wafModeFromAction(string(row.Kind), *req.Action)
		}
	}
	if prob := api.ValidateEdgeRulePenaltyKind(string(row.Kind), wafMode); prob != nil {
		return prob
	}
	if req.Penalty != nil {
		return req.Penalty.Validate()
	}
	return nil
}

// penaltyUpdateParams carries the penalty box through a PATCH: an action
// replacement keeps the stored penalty, penalty replaces it, clear_penalty
// removes it.
func penaltyUpdateParams(row state.EdgeRule, req api.UpdateEdgeRuleRequest, params state.UpdateEdgeRuleParams) state.UpdateEdgeRuleParams {
	if params.Action == nil && req.Penalty == nil && !req.ClearPenalty {
		return params
	}
	action := row.Action
	if params.Action != nil {
		action = *params.Action
	}
	switch {
	case req.ClearPenalty:
		action.Penalty = nil
	case req.Penalty != nil:
		action.Penalty = stateEdgeRulePenalty(req.Penalty)
	default:
		action.Penalty = row.Action.Penalty
	}
	params.Action = &action
	return params
}
