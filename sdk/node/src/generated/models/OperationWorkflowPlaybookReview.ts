/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookApplicability } from './OperationWorkflowPlaybookApplicability.js';
/**
 * OperationWorkflowPlaybookReview. Durable human choice with server reviewer, observation context and immutable replacement reference. Not an application outcome or execution permission.
 */
export type OperationWorkflowPlaybookReview = {
  /**
   * OperationWorkflowPlaybookReview.revision. Expected current source revision for requests, committed new shared revision for retained decisions.
   */
  revision: number;
  /**
   * OperationWorkflowPlaybookReview.decision. Human coordination choice; retire and replace permanently close this source checklist.
   */
  decision: 'continue' | 'retire' | 'replace';
  /**
   * OperationWorkflowPlaybookReview.reason. Required nonempty UTF-8 review reason, at most 1024 bytes and without control characters.
   */
  reason: string;
  /**
   * OperationWorkflowPlaybookReview.replacement_progress_id. Only for replace: distinct already-started active applicable checklist in the exact same case.
   */
  replacement_progress_id?: string;
  /**
   * OperationWorkflowPlaybookReview.replacement_revision. Only for replace: expected target revision, rechecked with its active disposition under the writer lock.
   */
  replacement_revision?: number;
  /**
   * OperationWorkflowPlaybookReview.actor. Authenticated account or API-key reviewer assigned by the server.
   */
  actor: string;
  /**
   * OperationWorkflowPlaybookReview.at. Server decision clock; application evidence was independently read.
   */
  at: string;
  /**
   * OperationWorkflowPlaybookReview.applicability. Retained exact-case observation reviewed by this decision; distinct from the live applicability field.
   */
  applicability: OperationWorkflowPlaybookApplicability;
};

