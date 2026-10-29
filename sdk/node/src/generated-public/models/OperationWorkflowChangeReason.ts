/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationBusinessDecision } from './OperationBusinessDecision.js';
/**
 * Optional application-reported explanation tied to a business decision fact in the same transaction and Operation and workflow instance and pinned version. It describes application reasoning and grants no authority.
 */
export type OperationWorkflowChangeReason = {
  milestone_id: string;
  decision: OperationBusinessDecision;
};

