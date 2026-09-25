/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
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
};

