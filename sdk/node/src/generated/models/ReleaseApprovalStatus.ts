/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ReleaseApprovalDecision } from './ReleaseApprovalDecision.js';
/**
 * A release's approval state.
 */
export type ReleaseApprovalStatus = {
  /**
   * The release approval applies before its release command.
   */
  before_release_command?: boolean;
  /**
   * The release requires current passing sandbox evidence in addition to human approval.
   */
  require_passing_sandbox_suite?: boolean;
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

