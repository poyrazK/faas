/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowInvariantRequirement } from './OperationWorkflowInvariantRequirement.js';
export type OperationWorkflowUnmetInvariant = {
  requirement: OperationWorkflowInvariantRequirement;
  reason: 'missing' | 'mismatched' | 'failed' | 'unknown';
};

