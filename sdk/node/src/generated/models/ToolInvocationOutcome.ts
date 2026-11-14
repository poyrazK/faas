/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Worker-reported complete MCP result or explicit preflight/transport uncertainty. Unknown outcomes never authorize a replay.
 */
export type ToolInvocationOutcome = {
  status: 'completed' | 'tool_error' | 'not_started' | 'outcome_unknown';
  code: 'result_received' | 'preflight_failed' | 'version_unavailable' | 'catalog_changed' | 'transport_error' | 'timeout' | 'cancel_requested' | 'worker_interrupted';
  /**
   * Sanitized complete MCP content and structuredContent only; at most 64 KiB decoded
   */
  result_base64?: string;
  result_sha256?: string;
  result_bytes?: number;
  /**
   * True when a complete result exceeded retention size; only its digest and length are retained
   */
  result_omitted?: boolean;
  redacted?: boolean;
};

