/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Opt-in sampled CPU profiling baked into each deployment (ADR-819).
 */
export type ProfilingConfig = {
  enabled: boolean;
  window_seconds?: number;
  /**
   * Continuous profile kinds (ADR-1155); defaults to cpu.
   */
  kinds?: Array<'cpu' | 'heap'>;
};

