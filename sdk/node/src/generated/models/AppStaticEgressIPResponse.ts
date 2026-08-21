/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * GET /v1/apps/{slug}/static-egress-ip response body (ADR-119).
 * IP / SetAt are pointers so the wire shape is stable: a Scale
 * customer with no pin yet sees `ip=null`, `set_at=null`,
 * `plan_cap=1`, `plan_allowed=true`. PlanCap is the
 * Limits.StaticEgressIPsPerApp value (1 in v1) so the dashboard
 * can render "you can use 1 static IP per app" without the CLI
 * round-tripping the plan table.
 *
 */
export type AppStaticEgressIPResponse = {
  /**
   * The pinned IPv4 (dotted-quad). `null` when the customer
   * has not pinned an IP yet. The DB family=4 CHECK
   * guarantees this is never IPv6.
   *
   */
  ip: string | null;
  /**
   * RFC 3339 timestamp for when the customer pinned the IP.
   * `null` when IP is `null`.
   *
   */
  set_at: string | null;
  /**
   * Per-app quota cap (Limits.StaticEgressIPsPerApp). 1 in v1
   * for Scale; 0 for plans that don't allow static egress IPs.
   *
   */
  plan_cap: number;
  /**
   * Whether the account's plan permits static egress IPs
   * (Plan.StaticEgressIPAllowed). `true` for Scale; `false`
   * for Free / Hobby / Pro so the CLI can render the upsell.
   *
   */
  plan_allowed: boolean;
};

