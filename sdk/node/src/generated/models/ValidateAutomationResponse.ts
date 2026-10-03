/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Validation issues, execution order and nominal next schedule occurrence.
 */
export type ValidateAutomationResponse = {
  valid: boolean;
  issues: Array<string>;
  step_order: Array<string>;
  next_fire_at?: string;
};

