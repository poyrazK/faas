/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Opt-in sampled CPU profiling baked into each deployment.
 */
export type ProfilingConfig = {
  enabled: boolean;
  window_seconds?: number;
  /**
   * Continuous profile kinds; defaults to cpu.
   */
  kinds?: Array<'cpu' | 'heap'>;
  /**
   * Guest-local port where a Go application serves net/http/pprof. The platform then collects CPU, heap and goroutine profiles from it, so the application needs no Gregale SDK. 9191 and 4318 are reserved.
   */
  pprof_port?: number;
};

