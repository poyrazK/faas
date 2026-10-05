/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained execution generation and ledger attempt count without private invocation data.
 */
export type OperationExecution = {
  generation: number;
  invocation_id: string;
  state: string;
  attempts: number;
  created_at: string;
  completed_at?: string;
};

