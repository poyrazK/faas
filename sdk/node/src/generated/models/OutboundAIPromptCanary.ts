/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A prompt canary: version serves percent of unpinned calls. It is rolled back when, after min_calls attempts in the window, its error rate exceeds the live version's by more than max_error_rate_increase percentage points, or its mean time to first byte by more than max_latency_increase_percent.
 */
export type OutboundAIPromptCanary = {
  version: number;
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

