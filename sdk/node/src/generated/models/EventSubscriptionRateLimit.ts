/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Projected token-bucket capacity at observed_at. Read-only metadata reserves no tokens. Missing rate_limit on older responses means unknown.
 */
export type EventSubscriptionRateLimit = {
  enabled: boolean;
  revision: number;
  interval_millis: number;
  burst: number;
  available_tokens: number;
  rate_limited: boolean;
  next_available_at?: string;
  observed_at: string;
};

