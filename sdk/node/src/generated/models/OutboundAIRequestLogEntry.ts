/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One settled AI call's metadata. Its bodies are kept only when the integration captures content.
 */
export type OutboundAIRequestLogEntry = {
  /**
   * What went wrong with the answer.
   */
  quality_flags?: Array<'truncated' | 'refused' | 'empty' | 'invalid_json' | 'tool_error'>;
  /**
   * The call's rating.
   */
  feedback?: 'good' | 'bad';
  /**
   * The rating's label.
   */
  feedback_label?: string;
  /**
   * The online evaluation's score.
   */
  evaluation_score?: number;
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
   * The call's X-Gregale-AI-User value.
   */
  end_user: string | null;
  /**
   * Whether the call's bodies can be read from its content endpoint.
   */
  content_captured: boolean;
  /**
   * The call's X-Gregale-AI-Tags as key=value, sorted by key.
   */
  tags: Array<string>;
  /**
   * The call's AI trace, when the integration records traces.
   */
  trace_id?: string;
  /**
   * The names of the tools the call's response requested, in order.
   */
  tool_calls?: Array<string>;
};

