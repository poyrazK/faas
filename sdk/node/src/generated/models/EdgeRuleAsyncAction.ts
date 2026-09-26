/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Marks the matched route for durable asynchronous execution (ADR-215).
 * on_success and on_failure optionally select app webhook subscription
 * IDs that receive the terminal job.finished invocation outcome. An
 * optional retry_policy overrides the app retry curve for invocations
 * accepted by this route. max_age_seconds starts when Gregale accepts a
 * request; values above the plan's invocation deadline are clamped.
 * Omitted values preserve existing app and plan defaults.
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
  /**
   * Optional per-route retry override; attempts are capped by the account plan.
   */
  retry_policy?: {
    /**
     * Total attempts including the first; 0 inherits the plan default.
     */
    max_attempts?: number;
    /**
     * Base delay for exponential retry backoff.
     */
    base_seconds?: number;
    /**
     * Maximum exponential backoff delay; must be at least base_seconds when both are positive.
     */
    max_seconds?: number;
    /**
     * Symmetric retry jitter fraction.
     */
    jitter_seconds?: number;
  };
  /**
   * Maximum lifetime from acceptance; 0 or omission inherits the account-plan default, and lower plan ceilings clamp this value.
   */
  max_age_seconds?: number;
};

