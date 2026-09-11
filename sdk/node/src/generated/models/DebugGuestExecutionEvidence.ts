/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded, platform-owned runtime execution evidence. Omitted when the runner signal was unavailable.
 */
export type DebugGuestExecutionEvidence = {
  runtime: 'node22' | 'node24' | 'python312' | 'python313' | 'go124';
  duration_ms: number;
  outcome: 'ok' | 'http_error' | 'handler_error' | 'timeout' | 'canceled';
  error_class?: 'http_5xx' | 'handler_exec' | 'handler_protocol' | 'timeout' | 'canceled';
};

