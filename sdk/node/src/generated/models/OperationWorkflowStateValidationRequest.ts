/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowStateReport } from './OperationWorkflowStateReport.js';
/**
 * Batch of application-reported workflow updates to validate before their transaction commits.
 */
export type OperationWorkflowStateValidationRequest = {
  workflow_states: Array<OperationWorkflowStateReport>;
};

