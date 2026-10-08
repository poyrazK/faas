/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteLifecycleMapping } from './RouteLifecycleMapping.js';
export type ApproveRouteLifecycleRequest = {
  expected_gate_revision: number;
  expected_requirements_revision: number;
  expected_removal_policy_revision: number;
  /**
   * Configuration digest from the server's saved route requirements check.
   */
  configuration_sha256: string;
  baseline_deployment_id: string;
  candidate_deployment_id: string;
  /**
   * Authoritative doc_sha256 capture metadata; normalized document hashes are not accepted.
   */
  baseline_contract_sha256: string;
  candidate_contract_sha256: string;
  mappings: Array<RouteLifecycleMapping>;
};

