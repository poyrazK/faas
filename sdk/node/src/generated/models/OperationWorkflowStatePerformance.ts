/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDurationDistribution } from './OperationWorkflowDurationDistribution.js';
export type OperationWorkflowStatePerformance = {
  sla_evaluated_visit_count: number;
  sla_breached_visit_count: number;
  contract_version: number;
  state: string;
  duration: OperationWorkflowDurationDistribution;
};

