/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One synthetic check probe.
 */
export type SyntheticCheckRun = {
  started_at: string;
  ok: boolean;
  /**
   * HTTP status received; absent when no response arrived.
   */
  status_code?: number;
  latency_ms: number;
  /**
   * Why the run failed; absent on success. status means a response arrived with an unexpected code.
   */
  error_class?: 'status' | 'timeout' | 'dns' | 'connect' | 'tls' | 'other';
};

