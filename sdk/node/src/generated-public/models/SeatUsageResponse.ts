/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * GET /v1/orgs/{slug}/seat_usage response. Visibility-only —
 * PR 9 ships the per-seat pricing cut-over per ADR-061
 * §"Out of scope". `used` counts active members only (the
 * store's `CountActiveOrgMembers` filters `removed_at IS
 * NULL`). `limit` comes from `org.Plan.OrgMembersMax()` —
 * the `free` plan returns `0` so the dashboard can render
 * "personal org only" instead of "0 of 0 used".
 *
 */
export type SeatUsageResponse = {
  /**
   * Active member count.
   */
  used: number;
  /**
   * Plan cap on active members (`Plan.OrgMembersMax()`).
   * Returns `0` for the `free` plan — the fail-closed
   * accessor shape.
   *
   */
  limit: number;
  plan: 'free' | 'hobby' | 'pro' | 'scale';
};

