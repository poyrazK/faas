/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReviewResponseLink } from './OperationWorkflowReviewResponseLink.js';
/**
 * Immutable human completion of escalation coordination work, with linked review references and server actor and time. Existing overdue markers remain historical.
 */
export type OperationWorkflowReviewResponseEscalationCompletion = {
  acknowledged_at: string;
  summary: string;
  evidence: Array<OperationWorkflowReviewResponseLink>;
  at: string;
  actor: string;
};

