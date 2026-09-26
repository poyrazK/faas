/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Marks the matched route for durable asynchronous execution (ADR-215).
 * on_success and on_failure optionally select app webhook subscription
 * IDs that receive the terminal job.finished invocation outcome. Retry,
 * deadline, retention, and payload limits come from the existing
 * invocation and account-plan contracts.
 *
 */
export type EdgeRuleAsyncAction = {
  /**
   * Optional app webhook subscription ID for successful invocations.
   */
  on_success?: string;
  /**
   * Optional app webhook subscription ID for failed or dead-lettered invocations.
   */
  on_failure?: string;
};

