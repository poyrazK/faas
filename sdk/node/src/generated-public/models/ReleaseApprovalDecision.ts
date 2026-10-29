/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A decision recorded on a release.
 */
export type ReleaseApprovalDecision = {
  approver: string;
  /**
   * The approver's organization role when they decided; personal for an app outside an organization.
   */
  role: 'owner' | 'admin' | 'developer' | 'personal';
  decision: 'approve' | 'reject';
  comment?: string;
  /**
   * Whether the approval counts toward the policy; the deployer's own approval does not unless self-approval is allowed.
   */
  counted: boolean;
  decided_at: string;
};

