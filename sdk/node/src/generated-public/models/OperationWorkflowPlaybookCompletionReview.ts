/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookBusinessOutcome } from './OperationWorkflowPlaybookBusinessOutcome.js';
/**
 * OperationWorkflowPlaybookCompletionReview. Bounded retained post-completion monitoring; updates never reopen or rewrite the original closure.
 */
export type OperationWorkflowPlaybookCompletionReview = {
  /**
   * OperationWorkflowPlaybookCompletionReview.id. Exact durable review identity.
   */
  id: string;
  /**
   * OperationWorkflowPlaybookCompletionReview.revision. Worker observation fence.
   */
  revision: number;
  /**
   * OperationWorkflowPlaybookCompletionReview.owner. Coordination owner frozen at completion.
   */
  owner: string;
  /**
   * OperationWorkflowPlaybookCompletionReview.status. Monitoring lifetime; expired reviews no longer publish.
   */
  status: 'active' | 'expired' | 'stopped';
  /**
   * OperationWorkflowPlaybookCompletionReview.expires_at. Original bounded window end from actual completion.
   */
  expires_at: string;
  /**
   * OperationWorkflowPlaybookCompletionReview.state_revision. Highest comparable retained application revision.
   */
  state_revision: number;
  current: OperationWorkflowPlaybookBusinessOutcome;
};

