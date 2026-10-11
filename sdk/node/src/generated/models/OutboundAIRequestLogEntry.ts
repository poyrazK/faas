/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One settled AI call's metadata (ADR-984). No request or response content is kept.
 */
export type OutboundAIRequestLogEntry = {
  id: string;
  created_at: string;
  /**
   * App UUID, or "runs".
   */
  caller: string;
  route_id: string | null;
  model: string;
  status: number;
  outcome: 'known' | 'unknown' | 'error';
  input_tokens: number;
  cached_input_tokens: number;
  cache_write_tokens: number;
  output_tokens: number;
  cost_millicents: number;
  duration_ms: number;
  ttfb_ms: number | null;
  attempts: number;
};

