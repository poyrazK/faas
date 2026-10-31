/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowPlaybookEvidenceWatchRequest. Opt-in durable checklist evidence monitoring; advisory observations preserve human reviews and application business authority.
 */
export type OperationWorkflowPlaybookEvidenceWatchRequest = {
  /**
   * OperationWorkflowPlaybookEvidenceWatchRequest.watch_id. Required current watch UUID when changing configuration; omit for a new watch. Prevents stale edits after a stopped slot is reclaimed.
   */
  watch_id?: string;
  /**
   * OperationWorkflowPlaybookEvidenceWatchRequest.subscription_id. Existing inbox destination in the same application, environment, and caller access boundary.
   */
  subscription_id: string;
  /**
   * OperationWorkflowPlaybookEvidenceWatchRequest.progress_revision. Expected exact human checklist revision; stale observations conflict.
   */
  progress_revision: number;
  /**
   * OperationWorkflowPlaybookEvidenceWatchRequest.revision. Watch configuration revision. Zero creates; polling leaves it unchanged.
   */
  revision: number;
  /**
   * OperationWorkflowPlaybookEvidenceWatchRequest.enabled. Explicit monitoring preference; enabling establishes a silent baseline.
   */
  enabled: boolean;
  /**
   * OperationWorkflowPlaybookEvidenceWatchRequest.owner. Responsible team label assigned to new inbox deliveries; no external destination.
   */
  owner: string;
};

