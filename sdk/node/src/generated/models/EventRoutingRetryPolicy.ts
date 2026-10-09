/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Routing policy before invocation admission. Duration budgets include routing attempt time and scheduled retry delays. Admission waits add no budget cost. API replacement accepts explicit settings; manifests and CLI default configured policies to jitter enabled.
 */
export type EventRoutingRetryPolicy = {
  /**
   * Wall-clock age from platform acceptance before routing admission. Includes pauses, capacity, backoff, and circuit waits. Zero disables expiry. Rejected for strictly ordered subscriptions.
   */
  max_delivery_age_ms?: number;
  max_attempts: number;
  /**
   * Zero disables the duration bound. Another retry is scheduled only when its entire delay fits within the remaining budget.
   */
  max_retry_duration_ms?: number;
  initial_backoff_ms: number;
  /**
   * Must be at least initial_backoff_ms.
   */
  max_backoff_ms: number;
  /**
   * Deterministic full jitter between one millisecond and the capped exponential delay.
   */
  jitter?: boolean;
};

