/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An external broker trigger's binding to a named app work policy.
 */
export type TriggerWorkBinding = {
  /**
   * Named policy in the trigger's app.
   */
  policy_name: string;
  /**
   * Scalar dot path into the broker message JSON payload.
   */
  key: string;
  /**
   * Optional scalar dot path for the fairness group.
   */
  fairness_key?: string;
};

