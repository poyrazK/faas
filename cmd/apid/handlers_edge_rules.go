// Edge rules (ADR-089, planned). Customer-configurable resource that
// runs in pkg/gateway BEFORE host→app resolution. See the per-CRUD
// handler godocs for the phase ordering; mirrors createDomain
// (handlers_ext.go:1466) + createAlertRule (handlers_alerts.go:142)
// for the auth/quota/audit triad.
//
// The action body is a kind-tagged union (RouteAction / RewriteAction
// / RedirectAction / HeadersAction / CORSAction / JWTAction /
// IPAction) and is decoded as json.RawMessage so the wire shape
// stays open to future kinds without a Go SDK rename. The kind-
// specific Validate() runs after the json.Unmarshal so a stray
// field on the wire surfaces as 422 before the store is touched.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// Audit-map JSON keys (ADR-089). The literal `"app_id"` etc. also
// appear in the pg_notify payload below; promoting to consts here
// keeps goconst quiet without sacrificing readability of the
// audit map literal.
const (
	auditKeyRuleID       = "rule_id"
	auditKeyAppID        = "app_id"
	auditKeyMatchHost    = "match_host"
	auditKeyMatchPath    = "match_path"
	auditKeyMatchMethods = "match_methods"
	auditKeyPriority     = "priority"
	auditKeyEnabled      = "enabled"
	auditKeyKind         = "kind"
	auditKeyDeploymentID = "deployment_id"
	auditKeyBuildID      = "build_id"
	auditKeyRepo         = "repo"
	auditKeyRef          = "ref"
	auditKeySourceBytes  = "source_bytes"
	auditKeyTrustRoot    = "trust_root"
)

// edgeRuleResponse builds the wire shape. Action is re-marshalled
// verbatim — the kind-specific struct has already been validated
// and round-tripped through jsonb on the read path.
func edgeRuleResponse(r state.EdgeRule) api.EdgeRuleResponse {
	actionBytes, _ := json.Marshal(r.Action)
	return api.EdgeRuleResponse{
		ID:           r.ID,
		AccountID:    r.AccountID,
		AppID:        r.AppID,
		MatchHost:    r.MatchHost,
		MatchPath:    r.MatchPath,
		MatchMethods: r.MatchMethods,
		Priority:     r.Priority,
		Enabled:      r.Enabled,
		Kind:         string(r.Kind),
		Action:       actionBytes,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}
}

// validateEdgeRuleAction dispatches the kind-specific Validate()
// based on the wire `kind` field. A wire body whose kind doesn't
// match the action shape surfaces here as 422 before the store
// sees it. Returns the *Problem so the handler can pass it
// straight to api.WriteProblem.
//
// plan carries the per-plan ceiling context that kind=throttle
// requires (sub-plan ceiling enforcement). Other kinds ignore it;
// the explicit ThrottleValidationContext keeps the boundary clear
// without forcing a global lookup.
func validateEdgeRuleAction(kind string, raw json.RawMessage, plan api.Plan) *api.Problem {
	k := state.EdgeRuleKind(kind)
	if !k.IsValid() {
		return api.ErrValidation(fmt.Sprintf("edge rule kind %q is not in the closed vocabulary", kind))
	}
	switch k {
	case state.EdgeRuleKindRoute:
		var a api.EdgeRuleRouteAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("route action: %v", err))
		}
		return a.Validate()
	case state.EdgeRuleKindRewrite:
		var a api.EdgeRuleRewriteAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("rewrite action: %v", err))
		}
		return a.Validate()
	case state.EdgeRuleKindRedirect:
		var a api.EdgeRuleRedirectAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("redirect action: %v", err))
		}
		return a.Validate()
	case state.EdgeRuleKindHeaders:
		var a api.EdgeRuleHeadersAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("headers action: %v", err))
		}
		return a.Validate()
	case state.EdgeRuleKindCORSA:
		var a api.EdgeRuleCORSAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("cors action: %v", err))
		}
		return a.Validate()
	case state.EdgeRuleKindJWT:
		var a api.EdgeRuleJWTAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("jwt action: %v", err))
		}
		return a.Validate()
	case state.EdgeRuleKindIP:
		var a api.EdgeRuleIPAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("ip action: %v", err))
		}
		return a.Validate()
	case state.EdgeRuleKindValidate:
		var a api.EdgeRuleValidateAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("validate action: %v", err))
		}
		return a.Validate()
	case state.EdgeRuleKindLimit:
		var a api.EdgeRuleLimitAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("limit action: %v", err))
		}
		return a.Validate()
	case state.EdgeRuleKindMaintenance:
		var a api.EdgeRuleMaintenanceAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("maintenance action: %v", err))
		}
		return a.Validate()
	case state.EdgeRuleKindThrottle:
		var a api.EdgeRuleThrottleAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("throttle action: %v", err))
		}
		// Build the per-plan ceiling context. An unknown plan
		// (cap=0) is fail-open at the validator and fail-closed
		// at the gateway compile step — that double-check is the
		// defence-in-depth pattern from compileLimitRules.
		limits, ok := api.LimitsFor(plan)
		if !ok {
			return api.ErrValidation(fmt.Sprintf("throttle action: plan %q has no limits table entry", plan))
		}
		return a.Validate(api.ThrottleValidationContext{
			PlanMaxRPS:         float64(limits.RateLimitRPS),
			PlanMaxBurst:       limits.RateLimitBurst,
			PlanMaxKeysPerRule: limits.ThrottleMaxKeysPerRule,
		})
	case state.EdgeRuleKindGeo:
		var a api.EdgeRuleGeoAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("geo action: %v", err))
		}
		return a.Validate()
	case state.EdgeRuleKindBudget:
		var a api.EdgeRuleBudgetAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("budget action: %v", err))
		}
		return a.Validate()
	}
	return api.ErrValidation("edge rule action validation fell through — internal bug")
}

// --- list ------------------------------------------------------------------

// listEdgeRulesForApp returns every rule bound to the slug's app,
// ordered by priority ASC then created_at DESC (the same shape the
// gateway matcher sees so the dashboard's "match order" preview
// matches the wire behaviour).
func (s *server) listEdgeRulesForApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	rules, err := s.store.ListEdgeRulesForApp(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list edge rules"))
		return
	}
	out := make([]api.EdgeRuleResponse, 0, len(rules))
	for _, rule := range rules {
		out = append(out, edgeRuleResponse(rule))
	}
	writeJSON(w, http.StatusOK, out)
}

// listEdgeRules returns every rule owned by the account across all
// apps. The dashboard uses this for the "Edge Rules" overview pane;
// the CLI uses it for `gregale edge-rules list`.
func (s *server) listEdgeRules(w http.ResponseWriter, r *http.Request, acct state.Account) {
	rules, err := s.store.ListEdgeRulesForAccount(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list edge rules"))
		return
	}
	out := make([]api.EdgeRuleResponse, 0, len(rules))
	for _, rule := range rules {
		out = append(out, edgeRuleResponse(rule))
	}
	writeJSON(w, http.StatusOK, out)
}

// --- create ----------------------------------------------------------------

// createEdgeRule validates the body, plan-gates paid-only kinds,
// enforces the per-app quota, and persists via
// CreateEdgeRuleIfUnderQuota.
//
// Phase order (matches createDomain + createAlertRule):
//  1. decode JSON body
//  2. plan-kind gate (Free + jwt|ip → 402)
//  3. loadApp (404 on unknown)
//  4. validate edge rule body (422 on bad action shape)
//  5. quota check (the store's TX-with-FOR-UPDATE re-checks)
//  6. persist
//  7. pg_notify (gatewayd LRU flush; lands in PR 8)
//  8. audit
//  9. log (sanitised)
//
// 10. respond 201
func (s *server) createEdgeRule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateEdgeRuleRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	// Plan-kind gate. Hobby+ unlocks jwt and ip; Free customers get
	// a clean 402 BEFORE loadApp (mirrors ErrPlanAlertRulesNotAllowed)
	// so a Free customer doesn't get a 404 leak.
	//
	// EdgeRuleKindGeo is NOT paid-only (ADR-091 D21 sub-decision
	// hop) — Free customers can post a single geo rule (cap=1).
	// The per-kind cap is enforced inside the store's
	// CreateEdgeRuleIfUnderQuota FOR UPDATE lock and surfaced
	// below via qe.PerKind / ErrPlanEdgeRuleKindQuotaReached.
	if state.EdgeRuleKind(req.Kind).IsPaidOnly() {
		limits, ok := api.LimitsFor(acct.Plan)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("plan limits not loaded"))
			return
		}
		if req.Kind == string(state.EdgeRuleKindJWT) && !limits.EdgeRulesJWTAllowed {
			api.WriteProblem(w, api.ErrPlanEdgeRuleKindNotAllowed(acct.Plan, req.Kind))
			return
		}
		if req.Kind == string(state.EdgeRuleKindIP) && !limits.EdgeRulesIPAllowed {
			api.WriteProblem(w, api.ErrPlanEdgeRuleKindNotAllowed(acct.Plan, req.Kind))
			return
		}
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if prob := validateEdgeRuleBody(&req, acct.Plan); prob != nil {
		api.WriteProblem(w, prob)
		return
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
		api.WriteProblem(w, api.ErrCapacity("plan limits not loaded"))
		return
	}
	row, err := s.store.CreateEdgeRuleIfUnderQuota(r.Context(), state.CreateEdgeRuleParams{
		AccountID:    acct.ID,
		AppID:        app.ID,
		MatchHost:    strings.ToLower(req.MatchHost),
		MatchPath:    matchPath,
		MatchMethods: req.MatchMethods,
		Priority:     priority,
		Enabled:      enabled,
		Kind:         state.EdgeRuleKind(req.Kind),
		Action:       actionFromBody(req.Kind, req.Action),
	}, limits)
	if err != nil {
		var qe *state.EdgeRuleQuotaError
		switch {
		case errors.As(err, &qe):
			if qe.PerKind {
				api.WriteProblem(w, api.ErrPlanEdgeRuleKindQuotaReached(acct.Plan, qe.Kind, qe.Limit, qe.Observed))
			} else {
				api.WriteProblem(w, api.ErrPlanLimitEdgeRules(acct.Plan, qe.Limit, qe.Observed))
			}
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "no such app")
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.ErrEdgeRuleConflict(err.Error()))
		default:
			api.WriteProblem(w, api.ErrCapacity("could not create edge rule"))
		}
		return
	}
	// pg_notify lands the LRU flush in PR 8; the channel constant is
	// reserved here so the wire format stabilises before the
	// gatewayd consumer lands.
	_ = s.notif.Notify(r.Context(), db.NotifyEdgeRuleChanged,
		fmt.Sprintf(`{"app_id":%q,"rule_id":%q,"op":"created"}`, app.ID, row.ID))
	s.log.Info("edge rule created",
		"rule", logsanitize.Field(row.ID),
		"app", app.Slug,
		"account", acct.ID,
		"kind", logsanitize.Field(string(row.Kind)),
		"priority", row.Priority,
	)
	s.audit.Emit(r.Context(), "edge_rule.created", &acct.ID, map[string]any{
		auditKeyRuleID:       row.ID,
		auditKeyAppID:        row.AppID,
		auditKeyMatchHost:    row.MatchHost,
		auditKeyMatchPath:    row.MatchPath,
		auditKeyMatchMethods: row.MatchMethods,
		auditKeyPriority:     row.Priority,
		auditKeyEnabled:      row.Enabled,
		auditKeyKind:         row.Kind,
	})
	writeJSON(w, http.StatusCreated, edgeRuleResponse(row))
}

// validateEdgeRuleBody checks the non-action fields (match_host /
// match_path / match_methods / priority) and dispatches the
// per-kind action Validate(). plan is the account's billing plan;
// kind=throttle needs it to enforce the sub-plan ceiling
// (rps ≤ plan.RateLimitRPS, burst ≤ plan.RateLimitBurst). Returns
// the first *Problem it finds.
func validateEdgeRuleBody(req *api.CreateEdgeRuleRequest, plan api.Plan) *api.Problem {
	if req.MatchHost == "" {
		return api.ErrValidation("match_host is required")
	}
	if len(req.MatchHost) > 253 {
		return api.ErrValidation(fmt.Sprintf("match_host exceeds 253 chars (got %d)", len(req.MatchHost)))
	}
	if !strings.HasPrefix(req.MatchPath, "/") {
		return api.ErrValidation("match_path must start with '/'")
	}
	if len(req.MatchPath) > 2048 {
		return api.ErrValidation(fmt.Sprintf("match_path exceeds 2048 chars (got %d)", len(req.MatchPath)))
	}
	if req.Priority != nil {
		if *req.Priority < 0 || *req.Priority > 10000 {
			return api.ErrValidation(fmt.Sprintf("priority must be in 0..10000 (got %d)", *req.Priority))
		}
	}
	if len(req.Action) == 0 {
		return api.ErrValidation("action is required")
	}
	prob := validateEdgeRuleAction(req.Kind, req.Action, plan)
	return prob
}

// actionFromBody re-marshals the wire json.RawMessage back into the
// typed state.EdgeRuleAction so the pgstore boundary can marshal it
// to jsonb once. The wire action has already been validated above;
// this pass is just the structural decode.
func actionFromBody(kind string, raw json.RawMessage) state.EdgeRuleAction {
	out := state.EdgeRuleAction{Kind: state.EdgeRuleKind(kind)}
	switch state.EdgeRuleKind(kind) {
	case state.EdgeRuleKindRoute:
		var a api.EdgeRuleRouteAction
		if err := json.Unmarshal(raw, &a); err == nil {
			out.Route = &state.EdgeRuleRouteAction{TargetAppSlug: a.TargetAppSlug}
		}
	case state.EdgeRuleKindRewrite:
		var a api.EdgeRuleRewriteAction
		if err := json.Unmarshal(raw, &a); err == nil {
			out.Rewrite = &state.EdgeRuleRewriteAction{From: a.From, To: a.To}
		}
	case state.EdgeRuleKindRedirect:
		var a api.EdgeRuleRedirectAction
		if err := json.Unmarshal(raw, &a); err == nil {
			out.Redirect = &state.EdgeRuleRedirectAction{StatusCode: a.StatusCode, To: a.To, Headers: a.Headers}
		}
	case state.EdgeRuleKindHeaders:
		var a api.EdgeRuleHeadersAction
		if err := json.Unmarshal(raw, &a); err == nil {
			ops := func(in []api.EdgeRuleHeaderOp) []state.EdgeRuleHeaderOp {
				out := make([]state.EdgeRuleHeaderOp, len(in))
				for i, op := range in {
					out[i] = state.EdgeRuleHeaderOp{Name: op.Name, Value: op.Value, Action: op.Action}
				}
				return out
			}
			out.Headers = &state.EdgeRuleHeadersAction{
				RequestHeaders:  ops(a.RequestHeaders),
				ResponseHeaders: ops(a.ResponseHeaders),
			}
		}
	case state.EdgeRuleKindCORSA:
		var a api.EdgeRuleCORSAction
		if err := json.Unmarshal(raw, &a); err == nil {
			out.CORS = &state.EdgeRuleCORSAction{
				AllowOrigins: a.AllowOrigins, AllowMethods: a.AllowMethods,
				AllowHeaders: a.AllowHeaders, ExposeHeaders: a.ExposeHeaders,
				AllowCredentials: a.AllowCredentials, MaxAgeSeconds: a.MaxAgeSeconds,
			}
		}
	case state.EdgeRuleKindJWT:
		var a api.EdgeRuleJWTAction
		if err := json.Unmarshal(raw, &a); err == nil {
			out.JWT = &state.EdgeRuleJWTAction{
				Issuer: a.Issuer, Audience: a.Audience, JWKSURL: a.JWKSURL,
				Algorithms: a.Algorithms, RequiredClaims: a.RequiredClaims,
			}
		}
	case state.EdgeRuleKindIP:
		var a api.EdgeRuleIPAction
		if err := json.Unmarshal(raw, &a); err == nil {
			out.IP = &state.EdgeRuleIPAction{Allow: a.Allow, Deny: a.Deny}
		}
	case state.EdgeRuleKindValidate:
		var a api.EdgeRuleValidateAction
		if err := json.Unmarshal(raw, &a); err == nil {
			// Schema is preserved byte-exact (json.RawMessage round-trip).
			// pkg/edgevalidate re-validates at compile time on the
			// gateway hot path; this decoder is the structural
			// decode pass only.
			out.Validate = &state.EdgeRuleValidateAction{
				Schema:              append([]byte(nil), a.Schema...),
				ContentTypes:        a.ContentTypes,
				ApplyWhileStreaming: a.ApplyWhileStreaming,
				RejectOnUnknown:     a.RejectOnUnknownFields,
				MaxBodyBytes:        a.MaxBodyBytes,
				// Empty 'block' coercion happens at the
				// gateway-side handler; the value is
				// carried verbatim so a PATCH that
				// explicitly clears the field round-trips
				// (issue #975 #3 / Mega-Foundation #979-a).
				ValidateMode: a.ValidateMode,
			}
		}
	case state.EdgeRuleKindLimit:
		var a api.EdgeRuleLimitAction
		if err := json.Unmarshal(raw, &a); err == nil {
			// Mirror the validate path's pattern: structural decode
			// only, no schema here. The gateway re-compiles the
			// cap (cmd-side compileLimitRules, cmd/gatewayd-internal/
			// edge_rules.go) and clamps out-of-range values as
			// defence-in-depth against a direct-DB row that
			// bypassed apid-Validate.
			out.Limit = &state.EdgeRuleLimitAction{
				MaxBodyBytes:          a.MaxBodyBytes,
				MaxBodyBytesStreaming: a.MaxBodyBytesStreaming,
			}
		}
	case state.EdgeRuleKindMaintenance:
		var a api.EdgeRuleMaintenanceAction
		if err := json.Unmarshal(raw, &a); err == nil {
			// Same mirror pattern as kind=limit: structural decode,
			// no payload validation here (validateEdgeRuleAction
			// already ran). The gateway re-compiles the action body
			// (cmd-side compileMaintenanceRules, PR-B) and clamps
			// out-of-range values as defence-in-depth against a
			// direct-DB row that bypassed apid-Validate
			// (cmd/e2e/edge_rules_common_test.go::seedEdgeRuleDirect).
			out.Maintenance = &state.EdgeRuleMaintenanceAction{
				RetryAfterSeconds: a.RetryAfterSeconds,
				Message:           a.Message,
			}
		}
	case state.EdgeRuleKindThrottle:
		var a api.EdgeRuleThrottleAction
		if err := json.Unmarshal(raw, &a); err == nil {
			// Same pattern as Limit/Maintenance: structural decode
			// only, no schema here. The gateway re-compiles the cap
			// (cmd-side compileThrottleRules, cmd/gatewayd-internal/
			// edge_rules.go) and clamps out-of-range values as
			// defence-in-depth against a direct-DB row that
			// bypassed apid-Validate.
			out.Throttle = &state.EdgeRuleThrottleAction{
				RequestsPerSecond: a.RequestsPerSecond,
				Burst:             a.Burst,
			}
		}
	case state.EdgeRuleKindGeo:
		var a api.EdgeRuleGeoAction
		if err := json.Unmarshal(raw, &a); err == nil {
			out.Geo = &state.EdgeRuleGeoAction{Allow: a.Allow, Deny: a.Deny}
		}
	case state.EdgeRuleKindBudget:
		var a api.EdgeRuleBudgetAction
		if err := json.Unmarshal(raw, &a); err == nil {
			// Mirror the validate / limit path's pattern: structural
			// decode only. The gateway re-compiles the budget
			// (cmd-side compileBudgetRules, cmd/gatewayd-internal/
			// edge_rules.go) and clamps out-of-range values as
			// defence-in-depth against a direct-DB row that bypassed
			// apid-Validate.
			out.Budget = &state.EdgeRuleBudgetAction{
				BudgetMs:            a.BudgetMs,
				AllowOverrideHeader: a.AllowOverrideHeader,
			}
		}
	}
	return out
}

// --- get --------------------------------------------------------------------

// getEdgeRule resolves the rule + verifies the customer owns it.
// GetEdgeRuleByID does NOT filter by account, so the IDOR check is
// load-bearing — a stolen API key must not be able to read a
// foreign account's rule by id. Mirrors getAlertRule at
// handlers_alerts.go:266.
func (s *server) getEdgeRule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	row, err := s.store.GetEdgeRuleByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such edge rule")
		return
	}
	if row.AccountID != acct.ID {
		s.notFound(w, "no such edge rule")
		return
	}
	writeJSON(w, http.StatusOK, edgeRuleResponse(row))
}

// --- update ----------------------------------------------------------------

// updateEdgeRule mutates the optional fields. The kind is NOT
// patchable — rotating kind mid-life would break the action union
// (a 'cors' action has no fields a 'route' rule expects). The
// customer deletes + recreates if they need a different kind.
// Matches the UpdateAlertRule design.
func (s *server) updateEdgeRule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	row, err := s.store.GetEdgeRuleByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such edge rule")
		return
	}
	if row.AccountID != acct.ID {
		s.notFound(w, "no such edge rule")
		return
	}
	var req api.UpdateEdgeRuleRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	// Validate the optional fields if present.
	if req.MatchHost != nil {
		if *req.MatchHost == "" || len(*req.MatchHost) > 253 {
			api.WriteProblem(w, api.ErrValidation("match_host must be 1..253 chars"))
			return
		}
	}
	if req.MatchPath != nil {
		if !strings.HasPrefix(*req.MatchPath, "/") || len(*req.MatchPath) > 2048 {
			api.WriteProblem(w, api.ErrValidation("match_path must start with '/' and be ≤ 2048 chars"))
			return
		}
	}
	if req.Priority != nil {
		if *req.Priority < 0 || *req.Priority > 10000 {
			api.WriteProblem(w, api.ErrValidation(fmt.Sprintf("priority must be in 0..10000 (got %d)", *req.Priority)))
			return
		}
	}
	if req.Action != nil {
		prob := validateEdgeRuleAction(string(row.Kind), *req.Action, acct.Plan)
		if prob != nil {
			api.WriteProblem(w, prob)
			return
		}
	}
	// Build the update params. Lowercase the host on write so the
	// gateway matcher can do case-insensitive compares without
	// per-request ToLower.
	if req.MatchHost != nil {
		lowered := strings.ToLower(*req.MatchHost)
		req.MatchHost = &lowered
	}
	updated, err := s.store.UpdateEdgeRule(r.Context(), id, edgeRuleUpdateParamsFrom(req, row.Kind))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such edge rule")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not update edge rule"))
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyEdgeRuleChanged,
		fmt.Sprintf(`{"app_id":%q,"rule_id":%q,"op":"updated"}`, updated.AppID, updated.ID))
	s.log.Info("edge rule updated",
		"rule", logsanitize.Field(updated.ID),
		"app", updated.AppID,
		"account", acct.ID,
		"kind", logsanitize.Field(string(updated.Kind)),
	)
	s.audit.Emit(r.Context(), "edge_rule.updated", &acct.ID, map[string]any{
		auditKeyRuleID:   updated.ID,
		auditKeyAppID:    updated.AppID,
		auditKeyKind:     updated.Kind,
		auditKeyPriority: updated.Priority,
		auditKeyEnabled:  updated.Enabled,
	})
	writeJSON(w, http.StatusOK, edgeRuleResponse(updated))
}

// edgeRuleUpdateParamsFrom adapts the wire UpdateEdgeRuleRequest
// into the store's UpdateEdgeRuleParams. Action needs special
// handling because the store accepts *state.EdgeRuleAction (typed)
// while the wire carries *json.RawMessage. validateEdgeRuleAction
// has already verified the body matches `kind`; we decode one more
// time so the store boundary gets a fully-typed payload.
func edgeRuleUpdateParamsFrom(req api.UpdateEdgeRuleRequest, kind state.EdgeRuleKind) state.UpdateEdgeRuleParams {
	out := state.UpdateEdgeRuleParams{
		MatchHost:    req.MatchHost,
		MatchPath:    req.MatchPath,
		MatchMethods: req.MatchMethods,
		Priority:     req.Priority,
		Enabled:      req.Enabled,
	}
	if req.Action != nil {
		decoded := actionFromBody(string(kind), *req.Action)
		out.Action = &decoded
	}
	return out
}

// --- delete ----------------------------------------------------------------

func (s *server) deleteEdgeRule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	row, err := s.store.GetEdgeRuleByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such edge rule")
		return
	}
	if row.AccountID != acct.ID {
		s.notFound(w, "no such edge rule")
		return
	}
	if err := s.store.DeleteEdgeRule(r.Context(), id); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not delete edge rule"))
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyEdgeRuleChanged,
		fmt.Sprintf(`{"app_id":%q,"rule_id":%q,"op":"deleted"}`, row.AppID, row.ID))
	s.log.Info("edge rule deleted",
		"rule", logsanitize.Field(id),
		"app", row.AppID,
		"account", acct.ID,
		"kind", logsanitize.Field(string(row.Kind)),
	)
	s.audit.Emit(r.Context(), "edge_rule.deleted", &acct.ID, map[string]any{
		auditKeyRuleID: id,
		auditKeyAppID:  row.AppID,
		auditKeyKind:   row.Kind,
	})
	w.WriteHeader(http.StatusNoContent)
}
