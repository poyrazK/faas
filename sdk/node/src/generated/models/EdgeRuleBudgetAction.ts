/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-route end-to-end request budget (ADR-093 / §4.1.2.16).
 * The primitive for "POST /payment → 3 s, POST /signup → 10 s"
 * without writing timeout-propagation code in the customer's
 * app. The hot-path applier (cmd/gatewayd-public/main.go:PR-B
 * + pkg/gateway/handler_apply_edge_rule_budget.go) installs a
 * per-request `Budget` onto `r.Context()` via
 * `reqbudget.WithRemaining`; every downstream hop (DB, gRPC,
 * HTTP) tightens itself against the budget via
 * `reqbudget.WithOverhead` / `WithCeiling`.
 *
 * On expiry the platform returns 504 + RFC 7807 problem
 * envelope (`code: request_budget_exceeded`) BEFORE the
 * customer's handler runs — the goal is bounded resource
 * pin per request, not customer-visible timer logic. Customer
 * code can read `reqbudget.FromContext(r.Context()).Remaining`
 * if it wants to short-circuit its own work early.
 *
 * Field-by-field:
 * * `budget_ms` — required wall-clock budget. Must be > 0
 * and ≤ 15 min (`RequestBudgetPlanMax`). The plan's ceiling
 * (`Plan.RequestBudgetMaxMs`: Free 30 s, Hobby 60 s, Pro
 * 5 min, Scale 15 min; ADR-1150) clamps it at request time.
 * A 0 or negative value is rejected at create-time with 422.
 * * `allow_override_header` — optional HTTP request header
 * that lets a customer set a per-request override
 * (default `x-faas-budget-ms`). Empty (default) =
 * no override accepted; the edge-rule value is the
 * authoritative budget. The header is parsed as a
 * decimal integer in `[1, math.MaxInt32]`; out-of-range
 * values are ignored and the edge-rule value wins
 * (silent clamp, no 400 — the budget is a quality
 * primitive, not a security gate).
 *
 * Rejections never reach the wake gate, the auth chain, or
 * the rate limiter — same posture as the other kind=budget
 * edge-rule appliers. Free-and-above (no plan gate).
 *
 */
export type EdgeRuleBudgetAction = {
  /**
   * Per-request wall-clock budget in milliseconds (1 ms – 15 min; the plan's ceiling applies at request time).
   */
  budget_ms: number;
  /**
   * Optional RFC 7230 token header name for per-request override (default `x-faas-budget-ms`). Empty = no override.
   */
  allow_override_header?: string;
};

