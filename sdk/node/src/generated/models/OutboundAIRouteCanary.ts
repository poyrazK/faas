/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A route's model canary. target is the 1-based route position tried first by percent of the route's calls, chosen per X-Gregale-AI-Session, else X-Gregale-AI-User, else at random. The outbound gateway rolls the canary back when, after min_calls canary attempts in the window, its error rate exceeds the first target's by more than max_error_rate_increase percentage points, or its mean time to first byte by more than max_latency_increase_percent.
 */
export type OutboundAIRouteCanary = {
  target: number;
  percent: number;
  max_error_rate_increase: number;
  max_latency_increase_percent: number | null;
  min_calls: number;
  window_minutes: number;
  state: 'active' | 'rolled_back';
  reason: ('error_rate' | 'latency' | null);
  started_at: string;
  decided_at: string | null;
};

