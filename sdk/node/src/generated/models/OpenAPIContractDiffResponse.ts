/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OpenAPIContractAddition } from './OpenAPIContractAddition.js';
import type { OpenAPIContractBreak } from './OpenAPIContractBreak.js';
/**
 * Read-only OpenAPI contract comparison for the authoritative imported
 * app document (or the projected edge-rule fallback) and the latest
 * captured deployment snapshot.
 * `blocking` is true when a production promotion would be rejected
 * while the contract-diff feature flag is enabled.
 *
 */
export type OpenAPIContractDiffResponse = {
  app_id: string;
  scope: string;
  /**
   * Contract source used for the proposed snapshot.
   */
  source: 'manual_import' | 'edge_rules';
  baseline_deployment_id?: string;
  baseline_sha256?: string;
  proposed_sha256: string;
  baseline_captured_at?: string;
  blocking: boolean;
  breaks: Array<OpenAPIContractBreak>;
  additions: Array<OpenAPIContractAddition>;
};

