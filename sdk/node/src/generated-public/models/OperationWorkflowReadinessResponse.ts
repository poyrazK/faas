/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowTransitionReadiness } from './OperationWorkflowTransitionReadiness.js';
/**
 * Readiness evaluation for the selected business subject and workflow instance at the reported time.
 */
export type OperationWorkflowReadinessResponse = {
  subject: OperationSubject;
  workflow: string;
  instance_id: string;
  evaluated_at: string;
  readiness: OperationWorkflowTransitionReadiness;
};

