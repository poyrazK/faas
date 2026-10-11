/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteRemovalMapping } from './RouteRemovalMapping.js';
/**
 * Pinned route retirement evidence and explicit acknowledgement of telemetry limits.
 */
export type ApproveRouteRemovalRequest = {
  expected_policy_revision: number;
  baseline_deployment_id: string;
  candidate_deployment_id: string;
  /**
   * Authoritative doc_sha256 capture metadata, not a reserialized document digest.
   */
  baseline_contract_sha256: string;
  candidate_contract_sha256: string;
  mappings: Array<(RouteRemovalMapping & Record<string, any>)>;
  /**
   * Explicit owner acknowledgement that telemetry is sampled and asynchronous and cannot establish absence of every client.
   */
  acknowledge_observed_only: boolean;
};

