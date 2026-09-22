/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for PATCH /v1/apps/{slug}/mirrors/{id}. All fields are
 * optional; pointer-style patches mean an absent key keeps the
 * existing value, while an explicit zero/empty overrides. Setting
 * `redact_headers` to `[]` clears the customer's list (leaving
 * only the always-stripped list).
 *
 */
export type UpdateMirrorRuleRequest = {
  /**
   * New fan-out percent in [0, 100]. 0 = rule stays but doesn't fire.
   */
  percent?: number;
  /**
   * Set false to pause the rule without removing it.
   */
  enabled?: boolean;
  /**
   * Toggle request/response body-hash comparison in the ledger. Raw bodies are never stored.
   */
  include_body?: boolean;
  /**
   * Replace the customer's redact list. Empty array clears it.
   */
  redact_headers?: Array<string>;
};

