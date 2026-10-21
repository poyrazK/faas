/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One settled AI call's metadata (ADR-991). Its bodies are kept only when the integration captures content (ADR-996).
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
  /**
   * The call's X-Gregale-AI-User value (ADR-994).
   */
  end_user: string | null;
  /**
   * Whether the call's bodies can be read from its content endpoint (ADR-996).
   */
  content_captured: boolean;
  /**
   * The call's X-Gregale-AI-Tags as key=value, sorted by key (ADR-1436).
   */
  tags: Array<string>;
  /**
   * The call's AI trace, when the integration records traces (ADR 20261020202035).
   */
  trace_id?: string;
  /**
   * The names of the tools the call's response requested, in order (ADR 20261020202035).
   */
  tool_calls?: Array<string>;
};

