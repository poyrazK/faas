/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowEffectRequirement } from './OperationWorkflowEffectRequirement.js';
export type OperationWorkflowUnmetEffect = {
  requirement: OperationWorkflowEffectRequirement;
  reason: 'missing' | 'mismatched' | 'pending' | 'failed';
};

