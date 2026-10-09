/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteRemovalMapping } from './RouteRemovalMapping.js';
/**
 * Time-limited retirement receipt bound to captures and observed route usage.
 */
export type RouteRemovalApproval = {
  id: string;
  app_id: string;
  policy_revision: number;
  baseline_deployment_id: string;
  candidate_deployment_id: string;
  baseline_contract_sha256: string;
  candidate_contract_sha256: string;
  /**
   * Digest of server-normalized mapping entries.
   */
  mapping_sha256: string;
  mappings: Array<RouteRemovalMapping>;
  /**
   * Authenticated account and key or session identity.
   */
  approved_by: string;
  approved_at: string;
  valid_until: string;
  observation_from: string;
  observation_until: string;
  coverage: 'observed_only';
};

