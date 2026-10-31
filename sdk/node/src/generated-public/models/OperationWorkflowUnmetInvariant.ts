/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowInvariantRequirement } from './OperationWorkflowInvariantRequirement.js';
/**
 * An invariant requirement that the proposed evidence cannot satisfy, with its failure reason.
 */
export type OperationWorkflowUnmetInvariant = {
  requirement: OperationWorkflowInvariantRequirement;
  reason: 'missing' | 'mismatched' | 'failed' | 'unknown';
};

