/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current workload selected or left running by a proposed response.
 */
export type FinancialBudgetTarget = {
  kind: 'app' | 'job';
  id: string;
  name: string;
  environment_id?: string;
  deployment_id?: string;
  effect: string;
};

