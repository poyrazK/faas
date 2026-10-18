/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An app's overdue-invoice rule (ADR-1315).
 */
export type PutAPIConsumerBillingPolicyRequest = {
  /**
   * Days past an invoice's due date before its consumer is refused; 0 refuses at once.
   */
  block_overdue_after_days: number;
};

