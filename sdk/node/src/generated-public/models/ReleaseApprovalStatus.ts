/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ReleaseApprovalDecision } from './ReleaseApprovalDecision.js';
/**
 * A release's approval state (ADR-20261020085706).
 */
export type ReleaseApprovalStatus = {
  deployment_id: string;
  app: string;
  state: 'not_required' | 'waiting' | 'approved' | 'rejected' | 'expired';
  required_approvals?: number;
  /**
   * Approvals that count toward the policy.
   */
  approvals: number;
  after_stage?: number;
  deployed_by?: string;
  /**
   * When the canary entered its waiting stage.
   */
  waiting_since?: string;
  /**
   * When a release still waiting is aborted.
   */
  expires_at?: string;
  decisions: Array<ReleaseApprovalDecision>;
};

