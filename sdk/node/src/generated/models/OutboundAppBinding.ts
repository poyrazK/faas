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
};

