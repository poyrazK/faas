/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteLifecycleMapping } from './RouteLifecycleMapping.js';
/**
 * Time-limited lifecycle receipt bound to configuration and captured successors.
 */
export type RouteLifecycleApproval = {
  id: string;
  app_id: string;
  gate_revision: number;
  requirements_revision: number;
  removal_policy_revision: number;
  configuration_sha256: string;
  baseline_deployment_id: string;
  candidate_deployment_id: string;
  baseline_contract_sha256: string;
  candidate_contract_sha256: string;
  mapping_sha256: string;
  mappings: Array<RouteLifecycleMapping>;
  /**
   * Server comparison found no supported declared request, response, method, path parameter or security breaks. This is not proof of runtime equivalence.
   */
  compatibility: 'no_supported_breaks';
  checker_version: 1;
  /**
   * Authenticated owner/admin account and key or session identity.
   */
  approved_by: string;
  approved_at: string;
  valid_until: string;
  /**
   * Permanently invalidated by capture replacement or deletion; old bytes cannot revive the receipt.
   */
  invalidated_at?: string;
};

