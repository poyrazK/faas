/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundIntegrationOffer } from './OutboundIntegrationOffer.js';
/**
 * An app's attachment to a managed integration.
 */
export type OutboundAppBinding = {
  integration: OutboundIntegrationOffer;
  app_id: string;
  /**
   * App-specific methods, bounded by the integration ceiling.
   */
  allowed_methods: Array<string>;
  /**
   * App-specific path prefixes, bounded by the integration ceiling.
   */
  allowed_path_prefixes: Array<string>;
  /**
   * Optional per-binding daily admitted-request limit.
   */
  daily_request_limit: number | null;
  created_at: string;
  /**
   * This app's own UTC-day AI input plus output token limit on an AI integration.
   */
  ai_daily_token_limit?: number | null;
  /**
   * Admitted calls per UTC minute for this app.
   */
  ai_requests_per_minute?: number | null;
  /**
   * Settled tokens plus live reservations per UTC minute for this app.
   */
  ai_tokens_per_minute?: number | null;
  /**
   * Exact model names or prefixes ending in "*" this app may call; null allows every model.
   */
  ai_allowed_models?: any[] | null;
  /**
   * Per-request output-token ceiling; added to requests that name none.
   */
  ai_max_output_tokens?: number | null;
};

