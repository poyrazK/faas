/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookEvidenceCheck } from './OperationWorkflowPlaybookEvidenceCheck.js';
/**
 * OperationWorkflowPlaybookEvidenceChange. Opt-in durable checklist evidence monitoring; advisory observations preserve human reviews and application business authority.
 */
export type OperationWorkflowPlaybookEvidenceChange = {
  /**
   * OperationWorkflowPlaybookEvidenceChange.before_status. Pinned case identity or retained watch metadata.
   */
  before_status: string;
  /**
   * OperationWorkflowPlaybookEvidenceChange.before_reason. Pinned case identity or retained watch metadata.
   */
  before_reason: string;
  /**
   * OperationWorkflowPlaybookEvidenceChange.after. Pinned case identity or retained watch metadata.
   */
  after: OperationWorkflowPlaybookEvidenceCheck;
};

