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
  /**
   * Whether the platform may start automatic on-demand captures of the app when memory runs low, a request is in flight for too long, or an instance uses its whole CPU limit. Unset means on. Automatic captures count against their own hourly limit and at most half of the account's capture storage.
   */
  automatic_captures?: boolean;
};

