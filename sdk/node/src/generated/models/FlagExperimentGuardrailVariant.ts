/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One variant's guardrail rate against control's; difference is variant minus control, bounded by a 99% confidence sequence.
 */
export type FlagExperimentGuardrailVariant = {
  variant: string;
  control_rate: number;
  variant_rate: number;
  control_units: number;
  variant_units: number;
  difference?: number;
  low?: number;
  high?: number;
  status: 'ok' | 'breached' | 'insufficient_data';
};

