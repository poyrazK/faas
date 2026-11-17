/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagExperimentGuardrailVariant } from './FlagExperimentGuardrailVariant.js';
/**
 * One guardrail across the variants.
 */
export type FlagExperimentGuardrail = {
  name: 'server_error_rate' | 'p95_latency';
  tolerance: number;
  status: 'ok' | 'breached' | 'insufficient_data';
  variants: Array<FlagExperimentGuardrailVariant>;
};

