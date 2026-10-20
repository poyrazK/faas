/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Sets an app's approval policy. Omitted fields take the defaults: stage 1, a 24-hour timeout, owner or admin approvers, no self-approval.
 */
export type SetReleaseApprovalPolicyRequest = {
  required_approvals: number;
  /**
   * Default 1.
   */
  after_stage?: number;
  /**
   * Default 1440.
   */
  timeout_minutes?: number;
  /**
   * Default admin.
   */
  approvers?: 'admin' | 'developer';
  /**
   * Default false; always true for an app outside an organization.
   */
  allow_self_approval?: boolean;
};

