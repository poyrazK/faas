/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Availability and admission limits for one execution class.
 */
export type ExecutionClassCapability = {
  execution_class: 'standard' | 'extended';
  default_timeout_ms: number;
  max_timeout_ms: number;
  max_concurrent_runs: number;
  admission_available: boolean;
};

