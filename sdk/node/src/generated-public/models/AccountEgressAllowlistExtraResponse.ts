/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body of GET /v1/account/egress_allowlist_extra and PATCH /v1/account/egress_allowlist_extra. The trio (extra, plan_cap, max_extra) lets the dashboard render the override + plan cap + global ceiling in a single round-trip.
 */
export type AccountEgressAllowlistExtraResponse = {
  /**
   * Effective additive budget currently in force. 0 = no override; the plan cap is authoritative.
   */
  extra: number;
  /**
   * Plan cap on apps.egress_allowlist CIDR count (Hobby 8, Pro 16, Scale 64; Free 0).
   */
  plan_cap: number;
  /**
   * Global ceiling on the per-account override (api.MaxAccountEgressAllowlistExtra = 1024). Flat across plans; the validator rejects out-of-range values with `account_egress_allowlist_extra_out_of_range`.
   */
  max_extra: number;
};

