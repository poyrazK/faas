package gateway

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// requestBudgetCancelKey carries the timer cancellation returned by
// reqbudget.WithRemaining until Handler.ServeHTTP has finished. The budget
// helper runs in the middle of the handler, so cancelling here would abort
// the downstream wake/proxy work the budget is meant to bound.
type requestBudgetCancelKey struct{}

func cancelStampedRequestBudget(ctx context.Context) {
	if cancel, ok := ctx.Value(requestBudgetCancelKey{}).(context.CancelFunc); ok && cancel != nil {
		cancel()
	}
}

// applyEdgeRuleBudget (ADR-093 §Decision) consults the per-host
// edge-rule matcher for a `kind=budget` rule and stamps the
// per-request wall-clock budget onto r.Context() via
// `reqbudget.WithRemaining`. The stamped Budget is the source of
// truth for every downstream hop's remaining-time propagation
// (JWT verify, forward, gRPC, DB — each wraps its outgoing ctx via
// `reqbudget.WithOverhead` / `WithCeiling`).
//
// r is passed as **http.Request so the helper can re-bind the
// wrapped ctx to r before returning — Go cannot mutate r via the
// caller's local copy, and re-assigning through a pointer is the
// standard pattern for "stamp something on r.Context() and have
// the caller see it" (mirrors withSidecarPort at handler.go).
//
// Resolution order (load-bearing):
//
//  1. nil-safe: h.edgeRules nil → fall through, plan default applies
//     downstream via the BudgetMiddleware. Returns false.
//  2. Match miss → type-aware plan-level default budget
//     (limitsFor(app.Plan).RequestBudgetForType(app.Type)),
//     clamped to per-plan max. Stamp via WithRemaining, return false.
//  3. Match hit, same account → rule.BudgetMs. If the rule specifies
//     AllowOverrideHeader and the inbound request carries that
//     header with a parsable positive integer in
//     [1, plan.RequestBudgetMaxDuration()], the parsed value
//     OVERRIDES rule.BudgetMs for this single request (the
//     per-customer-tunable knob). Otherwise rule.BudgetMs wins.
//  4. Match hit, cross account → defense-in-depth no-op (mirrors
//     applyEdgeRuleIP / applyEdgeRuleLimit / applyEdgeRuleValidate).
//     Audit "blocked" + apply "success", plan default applies.
//
// The ceiling for the stamped budget is
// limitsFor(app.Plan).RequestBudgetMaxDuration() — never wider
// than the plan-level ceiling regardless of what the rule (or
// header override) carries. cmd-side compileBudgetRules already
// clamped the rule at api.RequestBudgetMaxMs; this is the second
// line of defense for direct-DB writes that bypassed
// apid-Validate.
//
// Returns false in every case (the budget middleware observes the
// deadline fire and writes 504 + RFC 7807 `code:
// request_budget_exceeded` — there is no early-return path here;
// kind=budget never short-circuits, it always stamps a budget).
func (h *Handler) applyEdgeRuleBudget(w http.ResponseWriter, r *http.Request, app App) bool {
	if h.edgeRules == nil {
		return false
	}
	limits, _ := api.LimitsFor(app.Plan)
	rule := h.edgeRules.MatchBudget(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
	if rule == nil {
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("budget", "miss")
		}
		h.stampRequestBudget(w, r, app, limits.RequestBudgetForType(string(app.Type)), "plan_default")
		return false
	}
	if rule.AccountID != app.AccountID {
		if h.edgeRuleAudit != nil {
			h.edgeRuleAudit.Emit(r.Context(), "edge_rule.budget_blocked", &rule.AccountID, map[string]any{
				"rule_id":         rule.ID,
				"from_host":       r.Host,
				"rule_account_id": rule.AccountID,
				"app_account_id":  app.AccountID,
			})
		}
		if h.metrics != nil {
			h.metrics.ObserveEdgeRuleMatch("budget", "blocked")
			h.metrics.ObserveEdgeRuleApply("budget", "success")
		}
		// Cross-account is a defense-in-depth no-op — fall through
		// to the plan default rather than honouring a rule that
		// belongs to a different account. Mirrors applyEdgeRuleIP /
		// applyEdgeRuleValidate / applyEdgeRuleLimit.
		h.stampRequestBudget(w, r, app, limits.RequestBudgetForType(string(app.Type)), "plan_default")
		return false
	}
	// Resolve the budget value: rule.BudgetMs, optionally overridden
	// by the per-customer-tunable header.
	budgetMs := rule.BudgetMs
	source := "rule"
	headerName := rule.AllowOverrideHeader
	if headerName == "" {
		headerName = api.RequestBudgetDefaultOverrideHeader
	}
	if v := r.Header.Get(headerName); v != "" {
		if n, ok := parseBudgetHeaderMs(v); ok {
			budgetMs = n
			source = "header_override"
		}
		// Unparseable / non-positive → fall through to rule.BudgetMs.
	}
	total := time.Duration(budgetMs) * time.Millisecond
	ceiling := limits.RequestBudgetMaxDuration()
	if total <= 0 || total > ceiling {
		// Defence-in-depth: a direct-DB rule with budget_ms out of
		// range should never pin the budget to zero or to a value
		// larger than the per-plan ceiling. Clamp silently to the
		// ceiling; the customer never sees the warning (their rule
		// still fires, just at the platform ceiling).
		total = ceiling
		source = "ceiling_clamp"
	}
	if h.edgeRuleAudit != nil {
		h.edgeRuleAudit.Emit(r.Context(), "edge_rule.budget_matched", &rule.AccountID, map[string]any{
			"rule_id":   rule.ID,
			"from_host": r.Host,
			"budget_ms": total.Milliseconds(),
			"source":    source,
			"header":    headerName,
		})
	}
	if h.metrics != nil {
		h.metrics.ObserveEdgeRuleMatch("budget", "match")
		h.metrics.ObserveEdgeRuleApply("budget", "success")
	}
	h.stampRequestBudget(w, r, app, total, source)
	return false
}

// stampRequestBudget stamps reqbudget.WithRemaining onto r.Context()
// and re-binds r via the **http.Request pointer the caller passed.
// source is a diagnostic tag ("rule" / "header_override" /
// "ceiling_clamp" / "plan_default") emitted to logs only — it
// does NOT change the stamped Budget's behaviour.
//
// route + endpoint labels follow the §12 metric convention:
// route = "forward", endpoint = "POST:/path". The endpoint value
// comes from r.Method + r.URL.Path (path-aware labels live in the
// budget middleware's MatchEndpointFunc; applyEdgeRuleBudget is
// the per-rule applier and uses the method+path shorthand).
//
// codeql[go/log-injection] false-positive: endpoint is sanitised
// through logsanitize.Field at the log site below. CodeQL does not
// recognise logsanitize.Field as a sanitizer in its model, but the
// strip is observable (CR / LF / control chars in the path are
// replaced with U+00B7 middle dots). Precedent:
// pkg/gateway/cert_expiry.go:330-334, pkg/gateway/metrics.go:1226,
// pkg/gateway/synth.go:223-225.
func (h *Handler) stampRequestBudget(w http.ResponseWriter, r *http.Request, app App, total time.Duration, source string) {
	limits, _ := api.LimitsFor(app.Plan)
	ceiling := limits.RequestBudgetMaxDuration()
	route := "forward"
	endpoint := r.Method + ":" + r.URL.Path
	ctx, cancel, _ := reqbudget.WithRemaining(r.Context(), total, ceiling, route, endpoint)
	if cancel != nil {
		ctx = context.WithValue(ctx, requestBudgetCancelKey{}, cancel)
	}
	*r = *r.WithContext(ctx)
	if h.log != nil {
		h.log.Info("budget_stamped",
			"app_id", app.ID,
			"account_id", app.AccountID,
			"route", route,
			"endpoint", logsanitize.Field(endpoint),
			"budget_ms", total.Milliseconds(),
			"ceiling_ms", ceiling.Milliseconds(),
			"source", source,
		)
	}
}

// parseBudgetHeaderMs parses the per-customer-tunable header value
// into a positive integer milliseconds value. Accepts decimal
// integers (e.g. "3000") only — floats, ranges, and trailing units
// (e.g. "3s", "3000ms") are rejected. Returns (n, true) when n is
// in [1, math.MaxInt32]; (0, false) on parse failure or out-of-
// range. The ceiling check against per-plan RequestBudgetMaxDuration
// lives in the caller so this helper stays stateless.
func parseBudgetHeaderMs(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	if n < 1 {
		return 0, false
	}
	return n, true
}
