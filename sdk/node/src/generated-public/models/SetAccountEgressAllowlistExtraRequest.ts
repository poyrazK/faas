/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body of PATCH /v1/account/egress_allowlist_extra (issue #679 / PR-B / ADR-082). `extra` is the per-account additive budget on top of the plan's `apps.egress_allowlist` cap. `extra=0` clears the override (the plan cap is authoritative again); negative values or values above `max_extra` (1024) are rejected with `account_egress_allowlist_extra_out_of_range`.
 */
export type SetAccountEgressAllowlistExtraRequest = {
  /**
   * Requested additive budget, in CIDR count. 0 = clear the override; values above max_extra are rejected.
   */
  extra: number;
};

