/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Ordered native step summary from the captured workflow contract and durable step state.
 */
export type OperationRecoveryStep = {
  name: string;
  state: string;
  attempt: number;
  confirmed: boolean;
  /**
   * A dispatched action lacks confirmed success; operator reconciliation must establish its external effects.
   */
  outcome_unknown: boolean;
};

