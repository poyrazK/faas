/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteRemovalMapping } from './RouteRemovalMapping.js';
import type { RouteRemovalPolicy } from './RouteRemovalPolicy.js';
/**
 * Current retirement evaluation with blockers and recovery guidance.
 */
export type RouteRemovalCheck = {
  /**
   * Earliest approval time for the current observed baseline and capture.
   */
  earliest_approval_at?: string;
  /**
   * Expiry of the currently valid matching approval.
   */
  approval_valid_until?: string;
  /**
   * Recovery guidance for the current check.
   */
  next_actions?: Array<string>;
  /**
   * Authoritative baseline capture digest to pin in an approval request.
   */
  baseline_contract_sha256?: string;
  /**
   * Authoritative candidate capture digest to pin in an approval request.
   */
  candidate_contract_sha256?: string;
  policy: RouteRemovalPolicy;
  candidate_deployment_id: string;
  status: 'not_configured' | 'not_required' | 'passed' | 'blocked';
  removed: Array<RouteRemovalMapping>;
  blockers: Array<string>;
  approval_id?: string;
};

