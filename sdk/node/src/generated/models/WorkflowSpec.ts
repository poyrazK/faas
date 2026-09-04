/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowStepSpec } from './WorkflowStepSpec.js';
import type { WorkflowTriggerSpec } from './WorkflowTriggerSpec.js';
/**
 * A named workflow DAG submitted with a deployment (ADR-081).
 */
export type WorkflowSpec = {
  name: string;
  trigger?: (WorkflowTriggerSpec | null);
  steps: Array<WorkflowStepSpec>;
};

