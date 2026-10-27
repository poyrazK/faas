/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIOnlineEvaluation } from './OutboundAIOnlineEvaluation.js';
/**
 * Replace an integration's AI gateway settings. An empty ai_provider turns AI handling off and requires a null daily_token_limit.
 */
export type PutOutboundAIPolicyRequest = {
  ai_provider: '' | 'openai' | 'anthropic' | 'gemini' | 'azure_openai' | 'bedrock' | 'vertex';
  daily_token_limit: number | null;
  daily_cost_limit_millicents?: number | null;
  monthly_cost_limit_millicents?: number | null;
  /**
   * Log per-call metadata (caller, route, model, status, tokens, cost, timing) for 7 days. Requires ai_provider.
   */
  request_log_enabled?: boolean;
  /**
   * Also keep the first 32 KiB of each logged call's request and response bodies, never headers, for 3 days and at most 1,000 calls per UTC day. Requires request_log_enabled.
   */
  request_log_content?: boolean;
  /**
   * Send each settled app call to the app's log drains as one JSON line of metadata, never prompts or completions, about 15 seconds after it settles. Requires ai_provider.
   */
  log_export?: boolean;
  /**
   * Let through writes the gateway can neither meter nor knows to be free, such as realtime client secrets, vector stores, containers, fine-tuning jobs and context caches. They are otherwise refused with 403 outbound_ai_endpoint_unmetered; let through, they carry X-Gregale-AI-Unmetered and their spend is outside the caps. Requires ai_provider.
   */
  allow_unmetered?: boolean;
  /**
   * Add prompt-cache breakpoints to Claude requests on the Anthropic API, Vertex AI and Amazon Bedrock that carry none: after a system prompt or tool list seen again within five minutes, and after the last message of a conversation that carries earlier turns or names X-Gregale-AI-Session. Prompts under about 1,024 tokens are not marked; the response names the added breakpoints in X-Gregale-AI-Prompt-Cache. Requires ai_provider.
   */
  auto_prompt_cache?: boolean;
  /**
   * Refuse a trace's call once the trace made this many calls, settled and in flight, with 429 outbound_budget_exhausted and X-Gregale-Outbound-Rejection trace_call_limit. Requires ai_provider.
   */
  trace_max_calls?: number;
  /**
   * Refuse a trace's call that would take the trace's cost past this, with trace_cost_limit. Requires ai_provider.
   */
  trace_max_cost_millicents?: number;
  /**
   * Refuse a trace's call once this many consecutive calls requested the same tools with the same arguments, with trace_tool_loop: the agent is looping. Requires ai_provider.
   */
  trace_max_repeated_tool_calls?: number;
  /**
   * Per-end-user UTC-day token limit for calls that send X-Gregale-AI-User. Requires ai_provider.
   */
  user_daily_token_limit?: number | null;
  /**
   * Per-end-user UTC-day cost limit for calls that send X-Gregale-AI-User. Requires ai_provider.
   */
  user_daily_cost_limit_millicents?: number | null;
  /**
   * Score a sample of answered app calls with a judge model. Requires ai_provider and request_log_content.
   */
  online_evaluation?: OutboundAIOnlineEvaluation;
  /**
   * Alert bound apps with outbound.ai_quality.drift when a model's quality signals move away from its previous 7 days. Requires ai_provider.
   */
  quality_alerts?: boolean;
};

