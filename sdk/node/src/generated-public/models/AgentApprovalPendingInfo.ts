/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The approval request behind an approval_required, approval_denied or approval_expired problem. On approval_required a person opens review_url to decide, then the agent repeats the same request once; on the other two the agent should stop repeating it.
 */
export type AgentApprovalPendingInfo = {
  /**
   * The id of the approval request.
   */
  id: string;
  /**
   * The kind of action that needs approval; budget when the change would take the agent past its monthly budget.
   */
  category: 'delete' | 'production' | 'credentials' | 'billing' | 'budget';
  /**
   * The page where a signed-in member who may decide approves or denies the request.
   */
  review_url: string;
  /**
   * When the request and any approval of it expire.
   */
  expires_at: string;
  /**
   * Set on approval_denied and approval_expired: what became of the request.
   */
  status?: 'denied' | 'expired';
  /**
   * The person's reason for denying the request, when they gave one.
   */
  note?: string;
};

