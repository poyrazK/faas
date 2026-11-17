/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Turns one guardrail off or sets its tolerance.
 */
export type FlagExperimentGuardrailRequest = {
  /**
   * Turn the guardrail off.
   */
  disabled?: boolean;
  /**
   * Absolute increase over control that breaches the guardrail.
   */
  max_increase?: number;
};

