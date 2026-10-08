/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Advice on switching an observe-mode guard to enforce, judged on the
 * response range (ADR-732 amendment 1). Present only for observe mode
 * with a healthy metrics source; nothing is applied automatically.
 * `ready` needs a range of 24h or longer, at least 1000 requests, and no
 * successful (2xx/3xx) request among those the guard would have blocked.
 *
 */
export type PreAuthEnforcementSuggestion = {
  status: 'ready' | 'review' | 'insufficient_data';
  reason: string;
  /**
   * Every gateway request to the app in the range.
   */
  requests: number;
  /**
   * Sum over the app and route policies.
   */
  would_block: number;
  /**
   * Would-block requests whose final response was 2xx or 3xx.
   */
  would_block_succeeded: number;
};

