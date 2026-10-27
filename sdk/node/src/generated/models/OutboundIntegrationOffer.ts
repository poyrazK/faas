/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIContentPolicy } from './OutboundAIContentPolicy.js';
import type { OutboundAIOnlineEvaluation } from './OutboundAIOnlineEvaluation.js';
import type { OutboundRequestPolicy } from './OutboundRequestPolicy.js';
/**
 * Account-owned managed integration metadata; never contains a provider key or gateway token.
 */
export type OutboundIntegrationOffer = {
  id: string;
  name: string;
  origin: string;
  allowed_methods: Array<string>;
  allowed_path_prefixes: Array<string>;
  enabled: boolean;
  /**
   * Whether account policy grants use by stateless Runs.
   */
  runs_enabled: boolean;
  /**
   * Who supplies the provider Authorization value.
   */
  credential_source: 'operator_env' | 'customer_sealed';
  /**
   * Whether the selected source currently has a credential; never reveals its value.
   */
  credential_configured: boolean;
  /**
   * Whether the integration is operator-provisioned or customer-created.
   */
  owner_kind: 'operator' | 'customer';
  /**
   * Effective per-integration UTC-day admitted-request limit; null means no configured limit.
   */
  daily_request_limit: number | null;
  request_policy: OutboundRequestPolicy;
  /**
   * Present for AI gateway integrations. openai is the OpenAI-compatible wire format; the provider also selects how the managed credential is applied (header, AWS SigV4 for bedrock, Google service-account token for vertex) and how token usage is read.
   */
  ai_provider?: 'openai' | 'anthropic' | 'gemini' | 'azure_openai' | 'bedrock' | 'vertex';
  /**
   * UTC-day input plus output token limit; admission stops new calls once settled usage reaches it. null means no limit.
   */
  ai_daily_token_limit?: number | null;
  /**
   * UTC-day cost limit priced from the integration's price list.
   */
  ai_daily_cost_limit_millicents?: number | null;
  /**
   * Calendar-month cost limit priced from the integration's price list.
   */
  ai_monthly_cost_limit_millicents?: number | null;
  /**
   * Whether every settled AI call writes one metadata row to the request log.
   */
  ai_request_log_enabled?: boolean;
  /**
   * Whether logged calls also keep the first 32 KiB of their request and response bodies for 3 days.
   */
  ai_request_log_content?: boolean;
  ai_content_policy?: OutboundAIContentPolicy;
  /**
   * Whether each settled app call writes one metadata line to the app's log drains.
   */
  ai_log_export?: boolean;
  /**
   * Whether writes the gateway can neither meter nor knows to be free go through, outside the caps.
   */
  ai_allow_unmetered?: boolean;
  /**
   * Whether the gateway adds prompt-cache breakpoints to Claude requests that carry none.
   */
  ai_auto_prompt_cache?: boolean;
  /**
   * Calls each AI trace may make, settled and in flight. Absent when unlimited.
   */
  ai_trace_max_calls?: number;
  /**
   * Cost each AI trace may reach, with live reservations. Absent when unlimited.
   */
  ai_trace_max_cost_millicents?: number;
  /**
   * Consecutive calls of a trace whose responses may request the same tools with the same arguments. Absent when unlimited.
   */
  ai_trace_max_repeated_tool_calls?: number;
  /**
   * UTC-day input plus output token limit for each end user named by X-Gregale-AI-User.
   */
  ai_user_daily_token_limit?: number | null;
  /**
   * UTC-day cost limit for each end user named by X-Gregale-AI-User.
   */
  ai_user_daily_cost_limit_millicents?: number | null;
  /**
   * The sample of answered app calls a judge model scores.
   */
  ai_online_evaluation?: OutboundAIOnlineEvaluation;
  /**
   * Drift alerts on the integration's quality signals are on.
   */
  ai_quality_alerts?: boolean;
};

