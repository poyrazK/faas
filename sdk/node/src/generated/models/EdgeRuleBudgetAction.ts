/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-route execution budget plus an optional total deadline (ADR-570).
 * budget_ms starts after upload, wake and capacity admission. Its
 * configured override header (default x-faas-budget-ms) may alter the
 * execution allowance within the plan ceiling.
 *
 * total_deadline_ms starts when the public proxy receives the request
 * and includes upload, routing, auth, queueing, wake, retries and the
 * ordinary response exchange. It cannot be increased by an execution
 * override. Zero or omission leaves existing execution semantics.
 * Plan limits cap both values. Expiry returns a 504 problem with code
 * request_budget_exceeded before response commitment; a committed
 * ordinary response is terminated on expiry. Streaming and upgrades
 * use their idle/session contract after successful response headers.
 * Detached work and unmediated guest sockets are excluded. Nested
 * managed-service deadline transport remains pending acceptance.
 *
 */
export type EdgeRuleBudgetAction = {
  /**
   * Execution allowance in milliseconds, after upload/wake/admission.
   */
  budget_ms: number;
  /**
   * Optional total deadline from trusted public ingress; zero leaves it unset.
   */
  total_deadline_ms?: number;
  /**
   * Header for the execution override; default x-faas-budget-ms. It never increases total_deadline_ms.
   */
  allow_override_header?: string;
};

