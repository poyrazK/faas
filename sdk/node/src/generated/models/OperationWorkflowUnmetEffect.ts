/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowEffectRequirement } from './OperationWorkflowEffectRequirement.js';
/**
 * A required effect confirmation that is absent or incompatible with the planned evidence.
 */
export type OperationWorkflowUnmetEffect = {
  requirement: OperationWorkflowEffectRequirement;
  reason: 'missing' | 'mismatched' | 'pending' | 'failed';
};

