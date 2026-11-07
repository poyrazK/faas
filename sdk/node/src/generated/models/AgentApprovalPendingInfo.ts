/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The approval request behind an approval_required problem: a person opens review_url to decide, then the agent repeats the same request once.
 */
export type AgentApprovalPendingInfo = {
  /**
   * The id of the pending approval request.
   */
  id: string;
  /**
   * The kind of action that needs approval.
   */
  category: 'delete' | 'production' | 'credentials' | 'billing';
  /**
   * The page where a signed-in member who may decide approves or denies the request.
   */
  review_url: string;
  /**
   * When the request and any approval of it expire.
   */
  expires_at: string;
};

