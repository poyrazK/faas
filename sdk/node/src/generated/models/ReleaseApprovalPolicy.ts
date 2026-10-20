/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An app's release approval policy (ADR-20261020085706).
 */
export type ReleaseApprovalPolicy = {
  app: string;
  /**
   * Distinct eligible people who must approve each production release.
   */
  required_approvals: number;
  /**
   * The canary stage a release waits in, counted from 1.
   */
  after_stage: number;
  /**
   * How long a release may wait before its rollout is aborted.
   */
  timeout_minutes: number;
  /**
   * admin: organization owners and admins; developer: developers too.
   */
  approvers: 'admin' | 'developer';
  /**
   * Whether the deployer's own approval counts.
   */
  allow_self_approval: boolean;
  /**
   * Notification channels told when a release waits, is approved or rejected, or times out.
   */
  notify_channels: Array<string>;
  updated_by: string;
  created_at: string;
  updated_at: string;
};

