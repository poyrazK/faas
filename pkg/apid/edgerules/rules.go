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
package edgerules

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// EdgeRuleResponse builds the wire shape. Action is re-marshalled
// verbatim — the kind-specific struct has already been validated
// and round-tripped through jsonb on the read path.
//
// ValidateMode is surfaced at the top level (ADR-128 D1).
// The SQL column is NOT NULL DEFAULT 'block', so a pgstore
// round-trip always carries a value; the memstore preserves
// empty verbatim (memstore.go:CreateEdgeRule). We default to
// 'block' here so the wire response always carries a resolved
// value regardless of backing store. The action-level field is
// also re-emitted for the back-compat window (ADR-128 D2) so
// older clients reading action.validate_mode still see the
// same value.
func EdgeRuleResponse(r state.EdgeRule) api.EdgeRuleResponse {
	actionBytes, _ := json.Marshal(r.Action)
	matchHeaders := r.MatchHeaders
	if matchHeaders == nil {
		matchHeaders = map[string]string{}
	}
	mode := r.ValidateMode
	if mode == "" {
		mode = api.ValidateModeBlock
	}
	return api.EdgeRuleResponse{
		ID:           r.ID,
		AccountID:    r.AccountID,
		AppID:        r.AppID,
		MatchHost:    r.MatchHost,
		MatchPath:    r.MatchPath,
		MatchMethods: r.MatchMethods,
		MatchHeaders: matchHeaders,
		Priority:     r.Priority,
		Enabled:      r.Enabled,
		Kind:         string(r.Kind),
		ValidateMode: mode,
		Action:       actionBytes,
		Name:         r.Name,
		Description:  r.Description,
		ExpiresAt:    r.ExpiresAt,
		Expired:      r.EdgeRuleExpired(time.Now()),
		Match:        r.Match,
		Mode:         edgeRuleResponseMode(r.Mode),
		Penalty:      apiEdgeRulePenalty(r.Action.Penalty),
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}
}

// apiEdgeRulePenalty and stateEdgeRulePenalty convert the penalty box
// (ADR-20261022105846) between its wire and stored shapes.
func apiEdgeRulePenalty(p *state.EdgeRulePenalty) *api.EdgeRulePenalty {
	if p == nil {
		return nil
	}
	return &api.EdgeRulePenalty{Seconds: p.Seconds, After: p.After, WithinSeconds: p.WithinSeconds}
}

func stateEdgeRulePenalty(p *api.EdgeRulePenalty) *state.EdgeRulePenalty {
	if p == nil {
		return nil
	}
	return &state.EdgeRulePenalty{Seconds: p.Seconds, After: p.After, WithinSeconds: p.WithinSeconds}
}

// wafModeFromAction is a waf rule's effective mode from its wire action
// ("" for other kinds); an unset mode is observe.
func wafModeFromAction(kind string, raw json.RawMessage) string {
	if kind != string(state.EdgeRuleKindWAF) {
		return ""
	}
	var a api.EdgeRuleWAFAction
	_ = json.Unmarshal(raw, &a)
	if a.Mode == "" {
		return api.EdgeWAFModeObserve
	}
	return a.Mode
}

// cloneValidateParameters copies the raw parameter schemas so the stored
// action does not alias the request buffer; Empty() collapses to nil.
func cloneValidateParameters(p *api.EdgeRuleValidateParameters) *api.EdgeRuleValidateParameters {
	if p.Empty() {
		return nil
	}
	return &api.EdgeRuleValidateParameters{
		PathTemplate: p.PathTemplate,
		Path:         append(json.RawMessage(nil), p.Path...),
		Query:        append(json.RawMessage(nil), p.Query...),
		Headers:      append(json.RawMessage(nil), p.Headers...),
	}
}

func edgeRuleResponseMode(mode string) string {
	if mode == "" {
		return api.EdgeRuleModeEnforce
	}
	return mode
}

func utcTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	utc := t.UTC()
	return &utc
}

// ValidateEdgeRuleAction dispatches the kind-specific Validate()
// based on the wire `kind` field. A wire body whose kind doesn't
// match the action shape surfaces here as 422 before the store
// sees it. Returns the *Problem so the handler can pass it
// straight to api.WriteProblem.
//
// plan carries the per-plan ceiling context that kind=throttle
// requires (sub-plan ceiling enforcement). Other kinds ignore it;
// the explicit ThrottleValidationContext keeps the boundary clear
// without forcing a global lookup. matchPath is the rule's effective
// match_path; kind=validate path parameters must align with it.
func ValidateEdgeRuleAction(kind string, raw json.RawMessage, matchPath string, plan api.Plan) *api.Problem {
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
		if prob := a.Validate(); prob != nil {
			return prob
		}
		return a.Parameters.Validate(matchPath)
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
	case state.EdgeRuleKindCache:
		var a api.EdgeRuleCacheAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("cache action: %v", err))
		}
		// Validate applies apid-side defaults (60 s fresh /
		// 300 s stale-on-error) and enforces the closed
		// vary_on / methods vocabulary. The plan-level quota
		// (Free 0 / Hobby 1 / Pro 5 / Scale 20) is enforced
		// separately in CreateEdgeRuleIfUnderQuota — see
		// pkg/state/pgstore.go::CreateEdgeRuleIfUnderQuota
		// and memstore.go::CreateEdgeRuleIfUnderQuota.
		return a.Validate()
	case state.EdgeRuleKindRespond:
		var a api.EdgeRuleRespondAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("respond action: %v", err))
		}
		return a.Validate()
	case state.EdgeRuleKindRetry:
		var a api.EdgeRuleRetryAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("retry action: %v", err))
		}
		// Validate applies the ADR-201 §1 defaults in place. The
		// per-plan rule count (Free 0 / Hobby 3 / Pro 10 / Scale 25)
		// is enforced separately in CreateEdgeRuleIfUnderQuota via
		// Limits.EdgeRulesRetryPerApp, mirroring kind=cache.
		return a.Validate()
	case state.EdgeRuleKindCircuitBreaker:
		var a api.EdgeRuleCircuitBreakerAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("circuit_breaker action: %v", err))
		}
		return a.Validate()
	case state.EdgeRuleKindAsync:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return api.ErrValidation(fmt.Sprintf("async action: %v", err))
		}
		for field := range fields {
			if field != "on_success" && field != "on_failure" && field != "retry_policy" && field != "max_age_seconds" {
				return api.ErrValidation(fmt.Sprintf("async action does not accept field %q", field))
			}
		}
		if rawPolicy, ok := fields["retry_policy"]; ok && string(rawPolicy) != "null" {
			var policyFields map[string]json.RawMessage
			if err := json.Unmarshal(rawPolicy, &policyFields); err != nil {
				return api.ErrValidation(fmt.Sprintf("async action retry_policy: %v", err))
			}
			for field := range policyFields {
				if field != "max_attempts" && field != "base_seconds" && field != "max_seconds" && field != "jitter_seconds" {
					return api.ErrValidation(fmt.Sprintf("async action retry_policy does not accept field %q", field))
				}
			}
		}
		var a api.EdgeRuleAsyncAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("async action: %v", err))
		}
		return a.Validate()
	case state.EdgeRuleKindWAF:
		// Per-plan availability (Free/Hobby 0) is enforced in
		// CreateEdgeRuleIfUnderQuota via Limits.EdgeRulesWAFPerApp.
		var a api.EdgeRuleWAFAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("waf action: %v", err))
		}
		return a.Validate()
	case state.EdgeRuleKindChallenge:
		// Per-plan availability (Free 0) is enforced in
		// CreateEdgeRuleIfUnderQuota via Limits.EdgeRulesChallengePerApp.
		var a api.EdgeRuleChallengeAction
		if err := json.Unmarshal(raw, &a); err != nil {
			return api.ErrValidation(fmt.Sprintf("challenge action: %v", err))
		}
		return a.Validate()
	}
	return api.ErrValidation("edge rule action validation fell through — internal bug")
}

// validateEdgeRuleAsyncDestinations keeps async-route callbacks scoped to the
// same app and account as the edge rule. Invocation destinations use the same
// ownership boundary, so a route cannot turn a webhook ID into a cross-tenant
// delivery primitive.
func (s *Service) ValidateEdgeRuleAsyncDestinations(ctx context.Context, appID, accountID, kind string, raw json.RawMessage) *api.Problem {
	if kind != string(state.EdgeRuleKindAsync) {
		return nil
	}
	var action api.EdgeRuleAsyncAction
	if err := json.Unmarshal(raw, &action); err != nil {
		return api.ErrValidation(fmt.Sprintf("async action: %v", err))
	}
	for _, destination := range []struct {
		field string
		id    string
	}{
		{field: "on_success", id: action.OnSuccess},
		{field: "on_failure", id: action.OnFailure},
	} {
		if destination.id == "" {
			continue
		}
		hook, err := s.Host.Store().AppWebhookByID(ctx, destination.id)
		if err != nil || hook.AppID != appID || hook.AccountID != accountID {
			return api.ErrValidation(fmt.Sprintf("async action.%s must reference a webhook owned by this app", destination.field))
		}
	}
	return nil
}

// --- list ------------------------------------------------------------------

// listEdgeRulesForApp returns every rule bound to the slug's app,
// ordered by priority ASC then created_at DESC (the same shape the
// gateway matcher sees so the dashboard's "match order" preview
// matches the wire behaviour).
func (s *Service) ListEdgeRulesForApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	rules, err := s.Host.Store().ListEdgeRulesForApp(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list edge rules"))
		return
	}
	out := make([]api.EdgeRuleResponse, 0, len(rules))
	for _, rule := range rules {
		out = append(out, EdgeRuleResponse(rule))
	}
	s.setEdgeRuleSetETag(r.Context(), w, app.ID)
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// listEdgeRules returns every rule owned by the account across all
// apps. The dashboard uses this for the "Edge Rules" overview pane;
// the CLI uses it for `gregale edge-rules list`.
func (s *Service) ListEdgeRules(w http.ResponseWriter, r *http.Request, acct state.Account) {
	rules, err := s.Host.Store().ListEdgeRulesForAccount(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list edge rules"))
		return
	}
	out := make([]api.EdgeRuleResponse, 0, len(rules))
	for _, rule := range rules {
		out = append(out, EdgeRuleResponse(rule))
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
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
func (s *Service) CreateEdgeRule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateEdgeRuleRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
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
	if prob := planKindProblem(acct, req.Kind); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	m, prob := s.createRule(r.Context(), acct, app, req, func(ctx context.Context) (string, *api.Problem) {
		return s.ifMatchProblem(ctx, r, app.ID)
	})
	s.writeRuleMutation(r.Context(), w, m, prob, http.StatusCreated)
}

// ValidateEdgeRuleBody checks the non-action fields (match_host /
// match_path / match_methods / priority) and dispatches the
// per-kind action Validate(). plan is the account's billing plan;
// kind=throttle needs it to enforce the sub-plan ceiling
// (rps ≤ plan.RateLimitRPS, burst ≤ plan.RateLimitBurst). Returns
// the first *Problem it finds.
// TemplatedMatchPathProblem rejects an OpenAPI-style {param} placeholder in a
// match_path sent to the edge-rule create or update endpoint. Edge-rule paths
// are globs, so the placeholder would only match the literal braces and the
// rule would never run. Manifest- and environment-owned rules are not checked
// here so existing deployments keep applying.
func TemplatedMatchPathProblem(matchPath string) *api.Problem {
	glob, templated := api.EdgeRuleTemplatedPath(matchPath)
	if !templated {
		return nil
	}
	return api.ErrValidation(fmt.Sprintf(
		"match_path %q uses an OpenAPI {param} placeholder, but edge-rule paths are globs and would only match the literal braces; use %q", matchPath, glob))
}

func ValidateEdgeRuleBody(req *api.CreateEdgeRuleRequest, plan api.Plan) *api.Problem {
	matchHeaders, headerErr := api.NormalizeEdgeRuleMatchHeaders(req.MatchHeaders)
	if headerErr != nil {
		return api.ErrValidation(headerErr.Error())
	}
	req.MatchHeaders = matchHeaders
	if req.MatchHost == "" {
		return api.ErrValidation("match_host is required")
	}
	if len(req.MatchHost) > 253 {
		return api.ErrValidation(fmt.Sprintf("match_host exceeds 253 chars (got %d)", len(req.MatchHost)))
	}
	if prob := api.ValidateEdgeRuleMatchHost(req.MatchHost); prob != nil {
		return prob
	}
	if !strings.HasPrefix(req.MatchPath, "/") {
		return api.ErrValidation("match_path must start with '/'")
	}
	if len(req.MatchPath) > 2048 {
		return api.ErrValidation(fmt.Sprintf("match_path exceeds 2048 chars (got %d)", len(req.MatchPath)))
	}
	if prob := api.ValidateEdgeRuleMetadata(&req.Name, &req.Description, req.ExpiresAt, time.Now()); prob != nil {
		return prob
	}
	if prob := api.ValidateEdgeRuleMatch(req.Match); prob != nil {
		return prob
	}
	if prob := api.ValidateEdgeRuleMode(req.Mode); prob != nil {
		return prob
	}
	if req.Priority != nil {
		if *req.Priority < 0 || *req.Priority > 10000 {
			return api.ErrValidation(fmt.Sprintf("priority must be in 0..10000 (got %d)", *req.Priority))
		}
	}
	// ValidateMode (ADR-128 D1): the top-level wire field
	// must be one of the closed enum if set. Empty == 'block'
	// (the SQL-side default). The action-level field is also
	// validated by ValidateEdgeRuleAction via the
	// EdgeRuleValidateAction.Validate() path.
	if req.ValidateMode != "" &&
		req.ValidateMode != api.ValidateModeBlock &&
		req.ValidateMode != api.ValidateModeObserve &&
		req.ValidateMode != api.ValidateModeWarn {
		return api.ErrValidation(fmt.Sprintf(
			"validate_mode must be one of %q, %q, %q (got %q)",
			api.ValidateModeBlock, api.ValidateModeObserve, api.ValidateModeWarn, req.ValidateMode))
	}
	if len(req.Action) == 0 {
		return api.ErrValidation("action is required")
	}
	if prob := ValidateEdgeRuleAction(req.Kind, req.Action, req.MatchPath, plan); prob != nil {
		return prob
	}
	if req.Penalty != nil {
		if prob := api.ValidateEdgeRulePenaltyKind(req.Kind, wafModeFromAction(req.Kind, req.Action)); prob != nil {
			return prob
		}
		if prob := req.Penalty.Validate(); prob != nil {
			return prob
		}
	}
	return ValidateMCPRuleSelectors(req.Kind, req.MatchHost, req.MatchPath, req.MatchMethods, req.MatchHeaders, req.Action)
}

// ActionFromBody re-marshals the wire json.RawMessage back into the
// typed state.EdgeRuleAction so the pgstore boundary can marshal it
// to jsonb once. The wire action has already been validated above;
// this pass is just the structural decode.
func ActionFromBody(kind string, raw json.RawMessage) state.EdgeRuleAction {
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
			out.Redirect = &state.EdgeRuleRedirectAction{StatusCode: a.StatusCode, To: a.To, Headers: a.Headers, Template: a.Template}
		}
	case state.EdgeRuleKindHeaders:
		var a api.EdgeRuleHeadersAction
		if err := json.Unmarshal(raw, &a); err == nil {
			ops := func(in []api.EdgeRuleHeaderOp) []state.EdgeRuleHeaderOp {
				out := make([]state.EdgeRuleHeaderOp, len(in))
				for i, op := range in {
					out[i] = state.EdgeRuleHeaderOp{Name: op.Name, Value: op.Value, Action: op.Action, Template: op.Template}
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
				PlatformTenantExternalRefClaim: a.PlatformTenantExternalRefClaim,
				MCP:                            a.MCP,
				RequireExp:                     a.RequireExp,
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
				Parameters:   cloneValidateParameters(a.Parameters),
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
			// no payload validation here (ValidateEdgeRuleAction
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
				KeyBy:             a.KeyBy,
				JWTClaimName:      a.JWTClaimName,
				MaxKeysPerRule:    a.MaxKeysPerRule,
				MissingKeyPolicy:  a.MissingKeyPolicy,
				KeyFields:         a.KeyFields,
				CountStatuses:     a.CountStatuses,
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
	case state.EdgeRuleKindCache:
		var a api.EdgeRuleCacheAction
		if err := json.Unmarshal(raw, &a); err == nil {
			// Mirror the budget / limit path's pattern: structural
			// decode only. Validate (ValidateEdgeRuleAction above)
			// already applied apid-side defaults (60 s fresh /
			// 300 s stale-on-error), so the values carried into
			// the state mirror are the explicitly-stored ones
			// the gateway compile step (commit 10) will re-read.
			// The gateway compile path does NOT re-default — a
			// zero field at compile time is the apid-submitted
			// value, not "default", so an audit can ask "did
			// the customer ask for stale-on-error" by reading
			// the row.
			out.Cache = &state.EdgeRuleCacheAction{
				MaxAgeSeconds:               a.MaxAgeSeconds,
				StaleWhileRevalidateSeconds: a.StaleWhileRevalidateSeconds,
				StaleIfErrorSeconds:         a.StaleIfErrorSeconds,
				VaryOn:                      a.VaryOn,
				Methods:                     a.Methods,
			}
		}
	case state.EdgeRuleKindRespond:
		var a api.EdgeRuleRespondAction
		if err := json.Unmarshal(raw, &a); err == nil {
			out.Respond = &state.EdgeRuleRespondAction{
				StatusCode: a.StatusCode,
				Body:       append([]byte(nil), a.Body...),
			}
		}
	case state.EdgeRuleKindRetry:
		var a api.EdgeRuleRetryAction
		if err := json.Unmarshal(raw, &a); err == nil {
			// Re-run Validate so the mirror carries EFFECTIVE values,
			// not the customer's zeros. ValidateEdgeRuleAction above
			// ran on its own decode of the same bytes, and its
			// in-place defaulting does not reach this one. Storing
			// zeros here would make the row unreadable without
			// knowing the defaults, and would hand the gateway
			// compile step a rule it has to re-default identically.
			_ = a.Validate()
			out.Retry = &state.EdgeRuleRetryAction{
				MaxAttempts:        a.MaxAttempts,
				AllowNonIdempotent: a.AllowNonIdempotent,
				MinRemainingMs:     a.MinRemainingMs,
				BackoffMs:          a.BackoffMs,
				BudgetPercent:      a.BudgetPercent,
				BudgetMinRetries:   a.BudgetMinRetries,
			}
		}
	case state.EdgeRuleKindCircuitBreaker:
		var a api.EdgeRuleCircuitBreakerAction
		if err := json.Unmarshal(raw, &a); err == nil {
			_ = a.Validate()
			out.CircuitBreaker = &state.EdgeRuleCircuitBreakerAction{
				FailureThreshold: a.FailureThreshold,
				MinRequests:      a.MinRequests,
				WindowSeconds:    a.WindowSeconds,
				OpenSeconds:      a.OpenSeconds,
				MaxOpenSeconds:   a.MaxOpenSeconds,
			}
		}
	case state.EdgeRuleKindAsync:
		var a api.EdgeRuleAsyncAction
		if err := json.Unmarshal(raw, &a); err == nil {
			out.Async = &state.EdgeRuleAsyncAction{
				OnSuccess:     a.OnSuccess,
				OnFailure:     a.OnFailure,
				RetryPolicy:   a.RetryPolicy,
				MaxAgeSeconds: a.MaxAgeSeconds,
			}
		}
	case state.EdgeRuleKindWAF:
		var a api.EdgeRuleWAFAction
		if err := json.Unmarshal(raw, &a); err == nil && a.Validate() == nil {
			out.WAF = &state.EdgeRuleWAFAction{
				Mode:             a.Mode,
				ParanoiaLevel:    a.ParanoiaLevel,
				AnomalyThreshold: a.AnomalyThreshold,
				ExcludeRuleIDs:   a.ExcludeRuleIDs,
				InspectBodyBytes: a.InspectBodyBytes,
			}
		}
	case state.EdgeRuleKindChallenge:
		var a api.EdgeRuleChallengeAction
		if err := json.Unmarshal(raw, &a); err == nil && a.Validate() == nil {
			out.Challenge = &state.EdgeRuleChallengeAction{
				Difficulty:     a.Difficulty,
				PassTTLSeconds: a.PassTTLSeconds,
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
func (s *Service) GetEdgeRule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	row, err := s.Host.Store().GetEdgeRuleByID(r.Context(), id)
	if err != nil {
		httpkit.NotFound(w, "no such edge rule")
		return
	}
	if row.AccountID != acct.ID {
		httpkit.NotFound(w, "no such edge rule")
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, EdgeRuleResponse(row))
}

// --- update ----------------------------------------------------------------

// updateEdgeRule mutates the optional fields. The kind is NOT
// patchable — rotating kind mid-life would break the action union
// (a 'cors' action has no fields a 'route' rule expects). The
// customer deletes + recreates if they need a different kind.
// Matches the UpdateAlertRule design.
func (s *Service) UpdateEdgeRule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	row, prob := s.ruleForUpdate(r.Context(), acct, r.PathValue("id"))
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	var req api.UpdateEdgeRuleRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	m, prob := s.updateRule(r.Context(), acct, row, req, func(ctx context.Context) (string, *api.Problem) {
		return s.ifMatchProblem(ctx, r, row.AppID)
	})
	s.writeRuleMutation(r.Context(), w, m, prob, http.StatusOK)
}

// edgeRuleUpdateParamsFrom adapts the wire UpdateEdgeRuleRequest
// into the store's UpdateEdgeRuleParams. Action needs special
// handling because the store accepts *state.EdgeRuleAction (typed)
// while the wire carries *json.RawMessage. ValidateEdgeRuleAction
// has already verified the body matches `kind`; we decode one more
// time so the store boundary gets a fully-typed payload.
func edgeRuleUpdateParamsFrom(req api.UpdateEdgeRuleRequest, kind state.EdgeRuleKind) state.UpdateEdgeRuleParams {
	out := state.UpdateEdgeRuleParams{
		MatchHost:    req.MatchHost,
		MatchPath:    req.MatchPath,
		MatchMethods: req.MatchMethods,
		MatchHeaders: req.MatchHeaders,
		Priority:     req.Priority,
		Enabled:      req.Enabled,
		Name:         req.Name,
		Description:  req.Description,
		Match:        req.Match,
		ClearMatch:   req.ClearMatch,
		Mode:         req.Mode,
	}
	switch {
	case req.ClearExpiresAt:
		var cleared *time.Time
		out.ExpiresAt = &cleared
	case req.ExpiresAt != nil:
		expiresAt := req.ExpiresAt.UTC()
		set := &expiresAt
		out.ExpiresAt = &set
	}
	if req.Action != nil {
		decoded := ActionFromBody(string(kind), *req.Action)
		out.Action = &decoded
	}
	// ValidateMode (ADR-128 D1): top-level wire field wins.
	// If absent (nil pointer) AND Action carries a deprecated
	// action-level value, fall back to that for the
	// back-compat window (D2). A non-nil empty string is
	// the customer's explicit "reset to default" — pgstore
	// Update coalesces '' to validate_mode = validate_mode
	// (leave alone), so empty still leaves the column
	// untouched unless the action JSON supplied a value.
	// This matches the shape of Priority / Enabled.
	if req.ValidateMode != nil {
		out.ValidateMode = req.ValidateMode
	} else if req.Action != nil {
		if mode := validateModeFromAction(string(kind), *req.Action); mode != "" {
			out.ValidateMode = &mode
		}
	}
	return out
}

// resolveValidateMode picks the source of truth for the
// kind=validate validate_mode field on the create path
// (ADR-128 D1+D2). Top-level wire field wins when set; the
// deprecated action-level value is the fallback for the
// back-compat window. Returns "" for non-validate kinds
// (the column is NOT NULL DEFAULT 'block' so the pgstore
// coalesce handles the empty case correctly).
func resolveValidateMode(kind string, topLevel string, actionRaw json.RawMessage) string {
	if topLevel != "" {
		return topLevel
	}
	return validateModeFromAction(kind, actionRaw)
}

// validateModeFromAction extracts the kind=validate mode from
// a wire action JSON, returning an empty string for non-validate
// kinds or for validate kinds without the field set. The pgstore
// INSERT coalesces an empty value to 'block' (the SQL-side
// default at 00293 is also 'block') so empty is the right
// sentinel for "use the strictest mode".
func validateModeFromAction(kind string, raw json.RawMessage) string {
	if kind != string(state.EdgeRuleKindValidate) {
		return ""
	}
	var a api.EdgeRuleValidateAction
	if err := json.Unmarshal(raw, &a); err != nil {
		// ActionFromBody would have already surfaced this
		// error path; reaching here means the body decoded
		// at least syntactically. Treat malformed fragments
		// as "use the default" rather than 500ing on the
		// re-decode — the primary decode already cleared
		// the ValidateEdgeRuleAction gate.
		return ""
	}
	return a.ValidateMode
}

// --- delete ----------------------------------------------------------------

func (s *Service) DeleteEdgeRule(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	row, err := s.Host.Store().GetEdgeRuleByID(r.Context(), id)
	if err != nil {
		httpkit.NotFound(w, "no such edge rule")
		return
	}
	if row.AccountID != acct.ID {
		httpkit.NotFound(w, "no such edge rule")
		return
	}
	convergence, err := s.PrepareEdgeRuleMutation(r.Context(), row.AppID, row.ID, "deleted", row.MatchHost)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("edge-rule fleet convergence is unavailable; the rule was not deleted"))
		return
	}
	if s.edgeRuleIfMatchFailed(r.Context(), w, r, row.AppID) {
		convergence.Abort(r.Context())
		return
	}
	if err := s.Host.Store().DeleteEdgeRule(r.Context(), id); err != nil {
		convergence.Abort(r.Context())
		api.WriteProblem(w, api.ErrCapacity("could not delete edge rule"))
		return
	}
	s.Host.Logger().Info("edge rule deleted",
		"rule", logsanitize.Field(id),
		"app", row.AppID,
		"account", acct.ID,
		"kind", logsanitize.Field(string(row.Kind)),
	)
	s.Host.EmitAudit(r.Context(), "edge_rule.deleted", &acct.ID, map[string]any{
		httpkit.AuditKeyRuleID: id,
		httpkit.AuditKeyAppID:  row.AppID,
		httpkit.AuditKeyKind:   row.Kind,
	})
	if err := convergence.Apply(r.Context(), row.ID); err != nil {
		convergence.SetResponseState(w, "converging")
		s.Host.Logger().Error("edge rule deleted but fleet convergence is incomplete", "rule", row.ID, "generation", convergence.Generation, "err", err)
		api.WriteProblem(w, api.ErrCapacity("edge rule was deleted but the serving fleet has not acknowledged it; verify the rule is absent before retrying this mutation"))
		return
	}
	convergence.SetResponseState(w, "active")
	s.setEdgeRuleSetETag(r.Context(), w, row.AppID)
	w.WriteHeader(http.StatusNoContent)
}

func ValidateMCPRuleSelectors(kind, host, path string, methods []string, headers map[string]string, raw json.RawMessage) *api.Problem {
	if kind != "jwt" {
		return nil
	}
	var action api.EdgeRuleJWTAction
	if err := json.Unmarshal(raw, &action); err != nil {
		return api.ErrValidation(fmt.Sprintf("jwt action: %v", err))
	}
	if action.MCP == nil {
		return nil
	}
	resource, err := url.Parse(action.MCP.Resource)
	if err != nil || host != resource.Host || path != "/**" || len(methods) != 0 || len(headers) != 0 {
		return api.ErrValidation("MCP resource policies require the canonical host, match_path=/**, and no method or header selectors")
	}
	return nil
}
