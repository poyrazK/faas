/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OpenAPIContractAddition } from './OpenAPIContractAddition.js';
import type { OpenAPIContractBreak } from './OpenAPIContractBreak.js';
import type { OpenAPIContractUnknown } from './OpenAPIContractUnknown.js';
/**
 * Read-only OpenAPI contract comparison for the authoritative imported
 * app document (or the projected edge-rule fallback) and the latest
 * captured deployment snapshot.
 * `blocking` is true when a production promotion would be rejected
 * while the contract-diff feature flag is enabled. This includes
 * confirmed response breaks and changed response schemas the comparator
 * cannot classify as breaking or additive; those findings appear in
 * `unknowns` and remain distinct from confirmed breaks.
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
  /**
   * Changed unsupported response-schema features or incomplete legacy baselines whose compatibility could not be classified; these block promotion while the contract gate is enabled.
   */
  unknowns: Array<OpenAPIContractUnknown>;
  additions: Array<OpenAPIContractAddition>;
};

